package sweeper

import "context"

// ResetRetryableFailures moves failed records that never produced a broadcast
// transaction back to planned so the next manual run retries them. Records
// with a sweep or gas hash are deliberately excluded: a second signature could
// pay twice, and only an explicit on-chain check may decide those.
//
// Rows parked for a permanent business reason (below the per-address minimum,
// chain/token no longer configured) are not retried either.
func (e *Executor) ResetRetryableFailures(ctx context.Context, limit int) (int, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := e.queries().QueryContext(ctx, `
		UPDATE sweep_records SET status='planned', error='', revision=0, updated_at=NOW()
		WHERE id IN (
			SELECT id FROM sweep_records
			WHERE status='failed'
			  AND COALESCE(sweep_tx_hash,'')=''
			  AND COALESCE(gas_tx_hash,'')=''
			  AND COALESCE(error,'') NOT ILIKE '%per-address sweep minimum%'
			ORDER BY updated_at ASC
			LIMIT $1
		)
		RETURNING id`, limit)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	n := 0
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return n, err
		}
		n++
	}
	return n, rows.Err()
}
