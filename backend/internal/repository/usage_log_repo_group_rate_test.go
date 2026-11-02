package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestGetGroupRateBucketsUsesBoundedAggregate(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &usageLogRepository{sql: db}

	start := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	end := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	first := time.Date(2026, 11, 1, 18, 0, 0, 0, time.UTC)
	last := time.Date(2026, 11, 2, 11, 0, 0, 0, time.UTC)

	rows := sqlmock.NewRows([]string{"group_id", "rate_multiplier", "requests", "first_seen_at", "last_seen_at"}).
		AddRow(int64(3), 0.09, int64(42), first, last)

	mock.ExpectQuery("FROM usage_logs").
		WithArgs(int64(42), sqlmock.AnyArg(), start, end).
		WillReturnRows(rows)

	buckets, err := repo.GetGroupRateBuckets(context.Background(), 42, []int64{3, 9}, start, end)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
	require.Len(t, buckets, 1)
	require.Equal(t, int64(3), buckets[0].GroupID)
	require.Equal(t, 0.09, buckets[0].RateMultiplier)
	require.Equal(t, int64(42), buckets[0].Requests)
	require.Equal(t, first, buckets[0].FirstSeenAt)
	require.Equal(t, last, buckets[0].LastSeenAt)
}

func TestGetGroupRateBucketsEmptyGroupListSkipsQuery(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &usageLogRepository{sql: db}

	buckets, err := repo.GetGroupRateBuckets(context.Background(), 42, nil, time.Now(), time.Now())
	require.NoError(t, err)
	require.Empty(t, buckets)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetGroupRateBucketsFiltersNonPositiveMultipliers(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &usageLogRepository{sql: db}

	start := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	end := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	mock.ExpectQuery("rate_multiplier IS NOT NULL").WithArgs(int64(42), sqlmock.AnyArg(), start, end).
		WillReturnRows(sqlmock.NewRows([]string{"group_id", "rate_multiplier", "requests", "first_seen_at", "last_seen_at"}))

	_, err := repo.GetGroupRateBuckets(context.Background(), 42, []int64{3}, start, end)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetGroupActiveUserCountsIsWindowBounded(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &usageLogRepository{sql: db}

	since := time.Date(2026, 11, 2, 11, 55, 0, 0, time.UTC)
	mock.ExpectQuery("COUNT\\(DISTINCT user_id\\)").
		WithArgs(since, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"group_id", "active_users"}).
			AddRow(int64(10), int64(3)).
			AddRow(int64(20), int64(7)))

	counts, err := repo.GetGroupActiveUserCounts(context.Background(), []int64{10, 20}, since)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
	require.Equal(t, map[int64]int{10: 3, 20: 7}, counts)
}

func TestGetGroupActiveUserCountsEmptyGroupListSkipsQuery(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &usageLogRepository{sql: db}

	counts, err := repo.GetGroupActiveUserCounts(context.Background(), nil, time.Now())
	require.NoError(t, err)
	require.Empty(t, counts)
	require.NoError(t, mock.ExpectationsWereMet())
}
