package cryptopay

import (
	"context"
	"database/sql"
	"fmt"
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
		SELECT d.to_address, d.token, SUM(d.amount),
		       MAX(a.derivation_index)
		  FROM deposit_transactions d
		  -- deposit_addresses.chain stores the FAMILY (evm/solana), not the
		  -- chain id, so match on the address alone; one address belongs to
		  -- exactly one assignment.
		  LEFT JOIN deposit_addresses a
		         ON lower(a.address) = lower(d.to_address)
		 WHERE d.chain = $1
		   AND d.status = 'credited'
		   AND NOT EXISTS (
		        SELECT 1 FROM sweep_records s
		         WHERE s.chain = d.chain
		           AND s.from_address = d.to_address
		           AND s.token = d.token
		           AND s.status = 'swept'
		   )
		 GROUP BY d.to_address, d.token`, chain)
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
		// unknown. Skipping is the only safe move: defaulting to 0 would sign
		// the sweep with the HOT wallet seed, not the child's.
		if !derivationIndex.Valid || derivationIndex.Int64 <= 0 {
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
