package cryptopay

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
)

// Page size bounds for the admin sweep history. The handler rejects values
// outside 1..SweepRecordsMaxLimit; the service clamps as a second line.
const (
	SweepRecordsDefaultLimit = 100
	SweepRecordsMaxLimit     = 200
)

// lookupTokenSymbol resolves a watched token contract to its symbol and
// returns "" when the contract is not watched. Unlike tokenSymbol it never
// guesses: the sweep history may contain contracts that were later removed
// from watchedTokens, and showing "USDT" for them would be wrong.
func lookupTokenSymbol(chain, contract string) string {
	for _, t := range watchedTokens[chain] {
		if strings.EqualFold(t.Contract, contract) {
			return t.Symbol
		}
	}
	return ""
}

// ListSweepRecordsPage returns one page of sweep records, newest first.
// beforeID > 0 keeps only rows with a smaller id, so the caller pages by
// passing the last id it received. ListSweepRecords is left as it was; this is
// the variant the admin console uses.
func (s *Service) ListSweepRecordsPage(ctx context.Context, chain string, limit int, beforeID int64) ([]SweepRecord, error) {
	if limit <= 0 || limit > SweepRecordsMaxLimit {
		limit = SweepRecordsDefaultLimit
	}
	query := `SELECT id, chain, from_address, to_address, token, amount::text,
	                 gas_tx_hash, sweep_tx_hash, status, error, created_at, updated_at
	            FROM sweep_records WHERE 1=1`
	args := []any{}
	if chain != "" {
		args = append(args, chain)
		query += ` AND chain = $` + strconv.Itoa(len(args))
	}
	if beforeID > 0 {
		args = append(args, beforeID)
		query += ` AND id < $` + strconv.Itoa(len(args))
	}
	args = append(args, limit)
	query += ` ORDER BY id DESC LIMIT $` + strconv.Itoa(len(args))
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list sweep records: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]SweepRecord, 0, limit)
	for rows.Next() {
		var r SweepRecord
		var created, updated time.Time
		if err := rows.Scan(&r.ID, &r.Chain, &r.FromAddress, &r.ToAddress, &r.Token,
			&r.Amount, &r.GasTxHash, &r.SweepTxHash, &r.Status, &r.Error, &created, &updated); err != nil {
			return nil, err
		}
		r.Amount = trimDecimalZeros(r.Amount)
		r.Error = rpc.SafeRecordedError(r.Error)
		r.TokenSymbol = lookupTokenSymbol(r.Chain, r.Token)
		r.CreatedAt = created.UTC().Format(time.RFC3339)
		r.UpdatedAt = updated.UTC().Format(time.RFC3339)
		out = append(out, r)
	}
	return out, rows.Err()
}
