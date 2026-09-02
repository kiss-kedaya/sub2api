package repository

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestReadSchedulerFreshnessCachesShortLivedProjection(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectQuery(`(?s)SELECT\s+a\.id, a\.platform, a\.type, a\.status, a\.schedulable`).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "platform", "type", "status", "schedulable", "expires_at", "auto_pause_on_expired",
			"rate_limited_at", "rate_limit_reset_at", "overload_until", "temp_unschedulable_until",
			"temp_unschedulable_reason", "parent_account_id", "privacy_mode", "group_ids",
		}).AddRow(
			int64(7), "openai", "oauth", "active", true, nil, false,
			nil, nil, nil, nil, "", nil, "", "{}",
		))

	repo := newAccountRepositoryWithSQL(nil, db, nil)
	first, err := repo.ReadSchedulerFreshness(context.Background(), []int64{7})
	require.NoError(t, err)
	require.Contains(t, first, int64(7))

	second, err := repo.ReadSchedulerFreshness(context.Background(), []int64{7})
	require.NoError(t, err)
	require.Equal(t, first, second)
	require.NoError(t, mock.ExpectationsWereMet())
}
