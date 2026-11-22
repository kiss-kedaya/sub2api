//go:build unit

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

// 24h 概览必须是「一条分组查询覆盖全部 Key」，否则列表每行一次聚合就是 N+1。
func TestGetAPIKeysUsage24hUsesOneGroupedQuery(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	rows := sqlmock.NewRows([]string{"api_key_id", "requests", "actual_cost"}).
		AddRow(int64(7), int64(12), 0.25).
		AddRow(int64(9), int64(3), 0.0)
	mock.ExpectQuery(`(?s)FROM usage_logs.*GROUP BY api_key_id`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(rows)

	repo := &usageLogRepository{sql: db}
	since := time.Now().Add(-24 * time.Hour)
	stats, err := repo.GetAPIKeysUsage24h(context.Background(), []int64{7, 8, 9}, since)
	require.NoError(t, err)
	require.Len(t, stats, 3)
	require.Equal(t, int64(12), stats[7].Requests)
	require.InDelta(t, 0.25, stats[7].Cost, 1e-9)
	require.Equal(t, int64(3), stats[9].Requests)
	// 没有用量的 Key 也回 0，前端不用做缺失判断。
	require.Equal(t, int64(0), stats[8].Requests)
	require.Equal(t, 0.0, stats[8].Cost)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetAPIKeysUsage24hSkipsQueryForEmptyInput(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	repo := &usageLogRepository{sql: db}
	stats, err := repo.GetAPIKeysUsage24h(context.Background(), nil, time.Now())
	require.NoError(t, err)
	require.Empty(t, stats)
	require.NoError(t, mock.ExpectationsWereMet())
}
