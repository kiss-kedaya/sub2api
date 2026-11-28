//go:build unit

package repository

import (
	"context"
	"errors"
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

func TestBatchImageRepository_SumOutstandingHoldByAPIKey(t *testing.T) {
	t.Run("aggregates unsettled holds in one query", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })
		repo := &batchImageRepository{sql: db}

		mock.ExpectQuery(`(?s)`+regexp.QuoteMeta("SELECT COALESCE(SUM(GREATEST(COALESCE(hold_amount, estimated_cost), 0)), 0)")+`.*`+
			regexp.QuoteMeta("FROM batch_image_jobs")+`.*`+
			regexp.QuoteMeta("WHERE user_id = $1")+`.*`+
			regexp.QuoteMeta("AND api_key_id = $2")+`.*`+
			regexp.QuoteMeta("AND status IN ('created', 'uploading', 'submitted', 'running', 'indexing', 'settling')")).
			WithArgs(int64(11), int64(22)).
			WillReturnRows(sqlmock.NewRows([]string{"coalesce"}).AddRow(1.35))

		total, err := repo.SumOutstandingHoldByAPIKey(context.Background(), 11, 22)
		require.NoError(t, err)
		require.InDelta(t, 1.35, total, 1e-9)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("propagates sql errors for fail-closed handling", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })
		repo := &batchImageRepository{sql: db}

		dbErr := errors.New("connection refused")
		mock.ExpectQuery(`(?s)`+regexp.QuoteMeta("SELECT COALESCE(SUM")).
			WithArgs(int64(11), int64(22)).
			WillReturnError(dbErr)

		total, err := repo.SumOutstandingHoldByAPIKey(context.Background(), 11, 22)
		require.ErrorIs(t, err, dbErr)
		require.Zero(t, total)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}
