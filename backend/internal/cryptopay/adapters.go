package cryptopay

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"math/big"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/addresses"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/sweeper"
)

// addressResolver adapts the addresses service to the ledger's interface.
type addressResolver struct {
	svc *addresses.Service
}

func newAddressResolver(svc *addresses.Service) *addressResolver {
	return &addressResolver{svc: svc}
}

// ResolveDeposit maps a deposit address to its owner. Owned-but-unassigned
// (pool) or disabled addresses resolve as ours with userID 0 so funds are
// recorded for manual handling rather than dropped.
func (r *addressResolver) ResolveDeposit(ctx context.Context, chain, address string) (int64, bool, error) {
	family, err := addresses.FamilyForChain(chain)
	if err != nil {
		return 0, false, err
	}
	assignment, err := r.svc.LookupAny(ctx, family, address)
	if err != nil {
		return 0, false, err
	}
	if assignment == nil {
		return 0, false, nil
	}
	if assignment.Status != "active" {
		return 0, true, nil
	}
	return assignment.UserID, true, nil
}

// IsOwnAddress reports whether address belongs to us in any state; sweeps
// and gas top-ups must never credit anyone.
func (r *addressResolver) IsOwnAddress(ctx context.Context, chain, address string) (bool, error) {
	family, err := addresses.FamilyForChain(chain)
	if err != nil {
		return false, err
	}
	assignment, err := r.svc.LookupAny(ctx, family, address)
	if err != nil {
		return false, err
	}
	return assignment != nil, nil
}

// balanceReader feeds the sweep planner: credited-but-unswept totals per
// address, derived from deposit_transactions minus sweep_records.
type balanceReader struct {
	db       *sql.DB
	decimals map[string]int // "chain|lower(contract)" -> token decimals
}

func newBalanceReader(db *sql.DB, decimals map[string]int) *balanceReader {
	return &balanceReader{db: db, decimals: decimals}
}

// tokenDecimalsKey builds the balanceReader/executor lookup key for one
// (chain, contract) pair. Contracts compare case-insensitively because the
// scanner stores them lowercase while config may carry mixed case.
func tokenDecimalsKey(chain, contract string) string {
	return chain + "|" + strings.ToLower(contract)
}

// UnsweptBalances returns (address, token, raw) for credited deposits with
// no successful sweep yet. Raw is reconstructed from the NUMERIC amount and
// the token's decimals.
func (b *balanceReader) UnsweptBalances(ctx context.Context, chain string) ([]sweeper.AddressBalance, error) {
	rows, err := b.db.QueryContext(ctx, `
		SELECT c.to_address, c.token, c.credited - COALESCE(s.swept, 0),
		       MAX(a.derivation_index)
		  FROM (
		        -- Addresses are reused, so "already swept once" cannot mean
		        -- "nothing left": a later deposit to the same address must be
		        -- swept again. Only the unswept remainder is eligible.
		        SELECT d.to_address, d.token, SUM(d.amount) AS credited
		          FROM deposit_transactions d
		         WHERE d.chain = $1 AND d.status = 'credited'
		         GROUP BY d.to_address, d.token
		       ) c
		  LEFT JOIN (
		        SELECT s.from_address, s.token, SUM(s.amount) AS swept
		          FROM sweep_records s
		         WHERE s.chain = $1
		           AND s.status = 'swept'
		           AND s.amount > 0
		         GROUP BY s.from_address, s.token
		       ) s ON lower(s.from_address) = lower(c.to_address) AND s.token = c.token
		  -- deposit_addresses.chain stores the FAMILY (evm/solana), not the
		  -- chain id, so match on the address alone; one address belongs to
		  -- exactly one assignment.
		  LEFT JOIN deposit_addresses a
		         ON lower(a.address) = lower(c.to_address)
		 WHERE c.credited - COALESCE(s.swept, 0) > 0
		   AND NOT EXISTS (
		       SELECT 1 FROM sweep_records pending
		        WHERE pending.chain = $1
		          AND lower(pending.from_address) = lower(c.to_address)
		          AND lower(pending.token) = lower(c.token)
		          AND ((pending.job_version = 1 AND pending.status NOT IN ('swept', 'failed')
		                AND (pending.sweep_raw_tx <> '' OR pending.sweep_tx_hash <> ''
		                     OR pending.status = 'manual_review'))
		               OR (pending.job_version = 0 AND pending.status <> 'swept'
		                   AND (pending.status IN ('manual_review', 'gas_sent')
		                        OR pending.gas_tx_hash <> '' OR pending.sweep_tx_hash <> '')))
		   )
		 GROUP BY c.to_address, c.token, c.credited, s.swept`, chain)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []sweeper.AddressBalance
	for rows.Next() {
		var address, token string
		var amount sql.NullString
		var derivationIndex sql.NullInt64
		if err := rows.Scan(&address, &token, &amount, &derivationIndex); err != nil {
			return nil, err
		}
		// A credited deposit must always map to an assignment row; when it
		// does not (assignment deleted, data drift), the derivation index is
		// unknown (NULL after the LEFT JOIN). Skipping is the only safe move:
		// defaulting to 0 would sign the sweep with the HOT wallet seed, not
		// the child's. Index 0 itself is a legitimate assignment (legacy
		// pools started there) and must not be dropped.
		if !derivationIndex.Valid || derivationIndex.Int64 < 0 {
			continue
		}
		// Decimals come from the chain's token config: deriving them from
		// the stored rows cannot work (the NUMERIC column loses precision
		// information) and a wrong guess shifts the amount by orders of
		// magnitude. Lookup is per token contract because one chain now
		// watches several assets with different decimals.
		decimals := b.decimals[tokenDecimalsKey(chain, token)]
		if decimals == 0 {
			decimals = 6
		}
		raw, err := decimalToRaw(amount.String, decimals)
		if err != nil {
			return nil, fmt.Errorf("decode amount %q: %w", amount.String, err)
		}
		out = append(out, sweeper.AddressBalance{
			Chain: chain, Address: address, Token: token,
			DerivationIndex: uint32(derivationIndex.Int64),
			Raw:             raw, Decimals: decimals,
		})
	}
	return out, rows.Err()
}

// CurrentBalance also reads addresses reserved by unsigned token jobs. A gas
// payment does not freeze the later ERC-20 amount. The ledger remains the limit;
// ignored deposits and arbitrary extra balance are never swept by this query.
func (b *balanceReader) CurrentBalance(ctx context.Context, chain, address, token string) (*sweeper.AddressBalance, error) {
	var amount string
	var index sql.NullInt64
	err := b.db.QueryRowContext(ctx, `SELECT
		COALESCE((SELECT SUM(d.amount) FROM deposit_transactions d
		 WHERE d.chain=$1 AND d.to_address=$2 AND lower(d.token)=lower($3)
		 AND d.status='credited'), 0)
		- COALESCE((SELECT SUM(s.amount) FROM sweep_records s
		 WHERE s.chain=$1 AND lower(s.from_address)=lower($2)
		 AND lower(s.token)=lower($3) AND s.status='swept' AND s.amount>0), 0),
		MAX(a.derivation_index) FROM deposit_addresses a
		WHERE a.chain='evm' AND lower(a.address)=lower($2)`, chain, address, token).Scan(&amount, &index)
	if err != nil {
		return nil, fmt.Errorf("read current sweep balance: %w", err)
	}
	if !index.Valid || index.Int64 < 0 || index.Int64 > math.MaxUint32 {
		return nil, fmt.Errorf("sweep address assignment is missing or invalid")
	}
	decimals := b.decimals[tokenDecimalsKey(chain, token)]
	if decimals == 0 {
		decimals = 6
	}
	raw, err := decimalToRaw(amount, decimals)
	if err != nil {
		return nil, fmt.Errorf("decode current sweep balance: %w", err)
	}
	return &sweeper.AddressBalance{Chain: chain, Address: address, Token: token,
		DerivationIndex: uint32(index.Int64), Raw: raw, Decimals: decimals}, nil
}

// decimalToRaw converts a NUMERIC string ("12.345678") into raw units for
// the token's decimals ("12345678" at 6 decimals).
func decimalToRaw(value string, decimals int) (*big.Int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return big.NewInt(0), nil
	}
	parts := strings.SplitN(value, ".", 2)
	whole := parts[0]
	frac := ""
	if len(parts) == 2 {
		frac = parts[1]
	}
	if len(frac) > decimals {
		frac = frac[:decimals]
	}
	for len(frac) < decimals {
		frac += "0"
	}
	combined := whole + frac
	raw, ok := new(big.Int).SetString(combined, 10)
	if !ok {
		return nil, fmt.Errorf("invalid decimal %q", value)
	}
	return raw, nil
}
