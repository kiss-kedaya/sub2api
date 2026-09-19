package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBatchImageQueueRecoveryIndexMigration(t *testing.T) {
	content, err := FS.ReadFile("243_batch_image_queue_recovery_index_notx.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "CREATE INDEX CONCURRENTLY IF NOT EXISTS batch_image_jobs_queue_recovery_idx")
	require.Contains(t, sql, "ON batch_image_jobs (id) INCLUDE (batch_id)")
	require.Contains(t, sql, "WHERE status = 'submitted'")
	require.Contains(t, sql, "provider_job_name IS NOT NULL")
	require.Contains(t, sql, "last_error_code = 'QUEUE_FAILED'")
}
