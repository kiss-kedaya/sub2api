package repository

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestRecentCacheHitRate_ComputesRatioAboveMinSamples(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	// 9300 cache_read of 10000 total input across 100 requests -> 0.93.
	mock.ExpectQuery(`(?s)SUM\(cache_read_tokens\).*SUM\(input_tokens \+ cache_read_tokens\).*COUNT\(\*\).*FROM usage_logs.*api_key_id = \$1`).
		WithArgs(int64(24113), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"cache_read", "total_input", "reqs"}).
			AddRow(int64(9300), int64(10000), int64(100)))

	repo := &usageBillingRepository{db: db}
	rate, ok, err := repo.RecentCacheHitRate(context.Background(), 24113)
	require.NoError(t, err)
	require.True(t, ok)
	require.InDelta(t, 0.93, rate, 1e-9)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRecentCacheHitRate_FallsBackBelowMinSamples(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	// Only 5 requests (< min 20) -> ok=false, caller uses conservative estimate.
	mock.ExpectQuery(`(?s)FROM usage_logs.*api_key_id = \$1`).
		WithArgs(int64(7), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"cache_read", "total_input", "reqs"}).
			AddRow(int64(400), int64(500), int64(5)))

	repo := &usageBillingRepository{db: db}
	rate, ok, err := repo.RecentCacheHitRate(context.Background(), 7)
	require.NoError(t, err)
	require.False(t, ok)
	require.Zero(t, rate)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRecentCacheHitRate_GuardsInvalidKey(t *testing.T) {
	repo := &usageBillingRepository{db: nil}
	rate, ok, err := repo.RecentCacheHitRate(context.Background(), 0)
	require.NoError(t, err)
	require.False(t, ok)
	require.Zero(t, rate)
}

func TestRecentCacheHitRate_ClampsRatioToUnitInterval(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	// Degenerate data where cache_read exceeds total is clamped to 1.0.
	mock.ExpectQuery(`(?s)FROM usage_logs.*api_key_id = \$1`).
		WithArgs(int64(99), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"cache_read", "total_input", "reqs"}).
			AddRow(int64(1200), int64(1000), int64(50)))

	repo := &usageBillingRepository{db: db}
	rate, ok, err := repo.RecentCacheHitRate(context.Background(), 99)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, 1.0, rate)
	require.NoError(t, mock.ExpectationsWereMet())
}
