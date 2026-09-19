//go:build unit

package repository

import (
	"context"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestBatchImageRepository_ListPendingQueueRecoveryIsBoundedAndSelective(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := &batchImageRepository{sql: db}

	mock.ExpectQuery(`(?s)` + regexp.QuoteMeta("SELECT batch_id") + `.*` +
		regexp.QuoteMeta("status = 'submitted'") + `.*` +
		regexp.QuoteMeta("provider_job_name IS NOT NULL") + `.*` +
		regexp.QuoteMeta("last_error_code = 'QUEUE_FAILED'") + `.*` +
		regexp.QuoteMeta("ORDER BY id ASC") + `.*` +
		regexp.QuoteMeta("LIMIT $1")).
		WithArgs(100).
		WillReturnRows(sqlmock.NewRows([]string{"batch_id"}).
			AddRow("imgbatch_first").
			AddRow("imgbatch_second"))

	batchIDs, err := repo.ListBatchImageJobsPendingQueueRecovery(context.Background(), 5000)
	require.NoError(t, err)
	require.Equal(t, []string{"imgbatch_first", "imgbatch_second"}, batchIDs)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBatchImageRepository_MarkQueueRecoveredDoesNotOverwriteNewFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := &batchImageRepository{sql: db}

	mock.ExpectExec(`(?s)`+regexp.QuoteMeta("UPDATE batch_image_jobs")+`.*`+
		regexp.QuoteMeta("last_error_code = NULL")+`.*`+
		regexp.QuoteMeta("provider_job_name IS NOT NULL")+`.*`+
		regexp.QuoteMeta("last_error_code = 'QUEUE_FAILED'")).
		WithArgs("imgbatch_recovered", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = repo.MarkBatchImageJobQueueRecovered(context.Background(), "imgbatch_recovered")
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}
