package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMigration256bAddsUsageLogsRequestIDTrigramIndexes(t *testing.T) {
	content, err := FS.ReadFile("256b_add_usage_logs_request_id_trgm_indexes_notx.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_usage_logs_request_id_trgm")
	require.Contains(t, sql, "ON usage_logs USING gin (request_id gin_trgm_ops)")
	require.Contains(t, sql, "CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_usage_logs_upstream_request_id_trgm")
	require.Contains(t, sql, "ON usage_logs USING gin (upstream_request_id gin_trgm_ops)")
	// *_notx.sql 只允许纯 CREATE/DROP INDEX CONCURRENTLY，且不得出现事务控制语句。
	require.NotContains(t, strings.ToUpper(sql), "DO $$")
	require.NotContains(t, strings.ToUpper(sql), "BEGIN")
	require.NotContains(t, strings.ToUpper(sql), "COMMIT")
}

func TestMigration256aEnsuresPgTrgmBestEffort(t *testing.T) {
	content, err := FS.ReadFile("256a_ensure_usage_logs_pg_trgm_extension.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "CREATE EXTENSION IF NOT EXISTS pg_trgm")
	require.Contains(t, sql, "EXCEPTION WHEN OTHERS THEN")
	// 事务型迁移里不能出现 CONCURRENTLY。
	require.NotContains(t, strings.ToUpper(sql), "CONCURRENTLY")
}
