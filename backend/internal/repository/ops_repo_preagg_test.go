package repository

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestUpsertHourlyMetricsKeepsUngroupedUsageRows(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectExec(`(?s)`+
		regexp.QuoteMeta("COALESCE(NULLIF(TRIM(g.platform), ''), NULLIF(TRIM(a.platform), ''), 'unknown') AS platform")+
		`.*`+regexp.QuoteMeta("LEFT JOIN groups g ON g.id = ul.group_id")+
		`.*`+regexp.QuoteMeta("LEFT JOIN accounts a ON a.id = ul.account_id")+
		`.*`+regexp.QuoteMeta("HAVING GROUPING(group_id) = 1 OR group_id IS NOT NULL"),
	).WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))

	repo := &opsRepository{db: db}
	start := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	require.NoError(t, repo.UpsertHourlyMetrics(context.Background(), start, start.Add(time.Hour)))
	require.NoError(t, mock.ExpectationsWereMet())
}
