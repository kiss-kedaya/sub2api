package repository

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestOpsRepositoryBatchInsertErrorLogsUsesOneMultiRowStatement(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	repo := &opsRepository{db: db}
	now := time.Now().UTC()
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO ops_error_logs")).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()

	inserted, err := repo.BatchInsertErrorLogs(context.Background(), []*service.OpsInsertErrorLogInput{
		{ErrorPhase: "upstream", ErrorType: "upstream_error", ErrorMessage: "one", CreatedAt: now},
		{ErrorPhase: "internal", ErrorType: "api_error", ErrorMessage: "two", CreatedAt: now},
		nil,
	})
	require.NoError(t, err)
	require.EqualValues(t, 2, inserted)
	require.NoError(t, mock.ExpectationsWereMet())
}
