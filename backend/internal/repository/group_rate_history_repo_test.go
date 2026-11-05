package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// The service discovers the history capability through a type assertion, so a
// refactor that drops the method must fail to compile here instead of silently
// disabling the hints at runtime.
var _ service.GroupRateHistoryReader = (*groupRepository)(nil)

const groupRateHistoryQueryPattern = `FROM group_rate_history\s+WHERE group_id = ANY\(\$1\) AND changed_at >= \$2\s+ORDER BY group_id, changed_at, id\s+LIMIT \$3`

func TestListGroupRateChangesSinceMapsRows(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	since := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	changedAt := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	mock.ExpectQuery(groupRateHistoryQueryPattern).
		WithArgs(sqlmock.AnyArg(), since, groupRateHistoryRowCap).
		WillReturnRows(sqlmock.NewRows([]string{"id", "group_id", "old_rate", "new_rate", "changed_at"}).
			AddRow(int64(10), int64(3), 0.06, 0.08, changedAt).
			AddRow(int64(11), int64(5), 1.0, 0.9, changedAt.Add(time.Minute)))

	repo := newGroupRepositoryWithSQL(nil, db)
	got, err := repo.ListGroupRateChangesSince(context.Background(), []int64{3, 5}, since)
	require.NoError(t, err)
	require.Equal(t, []service.GroupRateChange{
		{ID: 10, GroupID: 3, OldRate: 0.06, NewRate: 0.08, ChangedAt: changedAt},
		{ID: 11, GroupID: 5, OldRate: 1.0, NewRate: 0.9, ChangedAt: changedAt.Add(time.Minute)},
	}, got)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestListGroupRateChangesSinceSendsUTCCutoff(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	zone := time.FixedZone("UTC+8", 8*3600)
	since := time.Date(2026, 11, 3, 20, 0, 0, 0, zone)
	mock.ExpectQuery(groupRateHistoryQueryPattern).
		WithArgs(sqlmock.AnyArg(), since.UTC(), groupRateHistoryRowCap).
		WillReturnRows(sqlmock.NewRows([]string{"id", "group_id", "old_rate", "new_rate", "changed_at"}))

	repo := newGroupRepositoryWithSQL(nil, db)
	got, err := repo.ListGroupRateChangesSince(context.Background(), []int64{3}, since)
	require.NoError(t, err)
	require.Empty(t, got)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestListGroupRateChangesSinceWithoutGroupsSkipsTheQuery(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	repo := newGroupRepositoryWithSQL(nil, db)
	got, err := repo.ListGroupRateChangesSince(context.Background(), nil, time.Now())
	require.NoError(t, err)
	require.Empty(t, got)
	require.NoError(t, mock.ExpectationsWereMet(), "no query may be issued without group ids")
}

func TestListGroupRateChangesSinceWithoutSQLExecutorIsEmpty(t *testing.T) {
	var repo *groupRepository
	got, err := repo.ListGroupRateChangesSince(context.Background(), []int64{1}, time.Now())
	require.NoError(t, err)
	require.Empty(t, got)

	got, err = (&groupRepository{}).ListGroupRateChangesSince(context.Background(), []int64{1}, time.Now())
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestListGroupRateChangesSincePropagatesErrors(t *testing.T) {
	t.Run("query error", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()
		mock.ExpectQuery(groupRateHistoryQueryPattern).WillReturnError(errors.New("boom"))

		repo := newGroupRepositoryWithSQL(nil, db)
		_, err = repo.ListGroupRateChangesSince(context.Background(), []int64{1}, time.Now())
		require.EqualError(t, err, "boom")
	})

	t.Run("scan error", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()
		mock.ExpectQuery(groupRateHistoryQueryPattern).
			WillReturnRows(sqlmock.NewRows([]string{"id", "group_id", "old_rate", "new_rate", "changed_at"}).
				AddRow("not-a-number", int64(1), 0.06, 0.08, time.Now()))

		repo := newGroupRepositoryWithSQL(nil, db)
		_, err = repo.ListGroupRateChangesSince(context.Background(), []int64{1}, time.Now())
		require.Error(t, err)
	})

	t.Run("row iteration error", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()
		mock.ExpectQuery(groupRateHistoryQueryPattern).
			WillReturnRows(sqlmock.NewRows([]string{"id", "group_id", "old_rate", "new_rate", "changed_at"}).
				AddRow(int64(1), int64(1), 0.06, 0.08, time.Now()).
				RowError(0, errors.New("broken cursor")))

		repo := newGroupRepositoryWithSQL(nil, db)
		_, err = repo.ListGroupRateChangesSince(context.Background(), []int64{1}, time.Now())
		require.EqualError(t, err, "broken cursor")
	})
}
