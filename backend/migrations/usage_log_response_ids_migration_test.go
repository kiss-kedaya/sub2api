//go:build unit

package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUsageLogResponseIDsMigrationsSafety(t *testing.T) {
	t.Run("346 group save_response_msg_id lock timeout and auth cache trigger", func(t *testing.T) {
		raw, err := FS.ReadFile("346_group_save_response_msg_id.sql")
		require.NoError(t, err)
		sql := string(raw)

		require.Contains(t, sql, "SET LOCAL lock_timeout = '5s';")
		require.Contains(t, sql, "ALTER TABLE groups")
		require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS save_response_msg_id BOOLEAN NOT NULL DEFAULT FALSE")
		require.Contains(t, sql, "enqueue_group_auth_cache_invalidation")
		require.Contains(t, sql, "OLD.save_response_msg_id IS NOT DISTINCT FROM NEW.save_response_msg_id")
		require.NotContains(t, sql, "ALTER TABLE usage_logs", "each transactional migration must touch at most one hot table")
	})

	t.Run("347 usage_logs response id columns single hot table", func(t *testing.T) {
		raw, err := FS.ReadFile("347_usage_log_response_ids.sql")
		require.NoError(t, err)
		sql := string(raw)

		require.Contains(t, sql, "SET LOCAL lock_timeout = '5s';")
		require.Contains(t, sql, "ALTER TABLE usage_logs")
		require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS response_request_id VARCHAR(128)")
		require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS client_request_id VARCHAR(128)")
		require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS response_msg_id VARCHAR(128)")
		require.NotContains(t, sql, "ALTER TABLE groups", "each transactional migration must touch at most one hot table")
	})

	t.Run("348 concurrent partial indexes without transaction blocks", func(t *testing.T) {
		raw, err := FS.ReadFile("348_usage_log_response_ids_indexes_notx.sql")
		require.NoError(t, err)
		sql := string(raw)
		upper := strings.ToUpper(sql)

		require.NotContains(t, upper, "DO $$")
		require.NotContains(t, upper, "BEGIN;")
		require.NotContains(t, upper, "COMMIT;")
		for _, want := range []string{
			"CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_usage_logs_response_request_id",
			"CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_usage_logs_client_request_id",
			"CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_usage_logs_response_msg_id",
			"CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_usage_logs_response_request_id_trgm",
			"CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_usage_logs_client_request_id_trgm",
			"CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_usage_logs_response_msg_id_trgm",
			"gin_trgm_ops",
			"WHERE response_request_id IS NOT NULL",
			"WHERE client_request_id IS NOT NULL",
			"WHERE response_msg_id IS NOT NULL",
		} {
			require.Contains(t, sql, want)
		}
	})
}
