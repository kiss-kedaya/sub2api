package repository

import "context"

// Bounded pruning uses expiry indexes and SKIP LOCKED so replicas do not block
// active tool-history writes or scan the account/usage tables.
func (r *excelGrantRepository) SweepExpired(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, `WITH expired AS(SELECT id FROM excel_oauth_sessions WHERE expires_at<=NOW() ORDER BY expires_at FOR UPDATE SKIP LOCKED LIMIT 1000) DELETE FROM excel_oauth_sessions s USING expired e WHERE s.id=e.id`)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `WITH expired AS(SELECT scope,state_key FROM excel_bps_session_states WHERE expires_at<=NOW() ORDER BY expires_at FOR UPDATE SKIP LOCKED LIMIT 1000) DELETE FROM excel_bps_session_states s USING expired e WHERE s.scope=e.scope AND s.state_key=e.state_key`)
	return err
}
