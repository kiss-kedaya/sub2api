package repository

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
)

func newSettingRepositorySQLMock(t *testing.T) (*settingRepository, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	return NewSettingRepository(client).(*settingRepository), mock
}

func settingRows(id int64, key, value string) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "key", "value", "updated_at"}).
		AddRow(id, key, value, time.Unix(10, 0))
}

func TestSettingRepositoryGetCachesValue(t *testing.T) {
	repo, mock := newSettingRepositorySQLMock(t)
	mock.ExpectQuery(`SELECT .* FROM "settings"`).
		WithArgs("cached").
		WillReturnRows(settingRows(1, "cached", "value"))

	first, err := repo.Get(context.Background(), "cached")
	require.NoError(t, err)
	require.Equal(t, "value", first.Value)
	second, err := repo.Get(context.Background(), "cached")
	require.NoError(t, err)
	require.Equal(t, "value", second.Value)
	// Callers receive independent values and cannot mutate the cached pointer.
	first.Value = "mutated"
	third, err := repo.Get(context.Background(), "cached")
	require.NoError(t, err)
	require.Equal(t, "value", third.Value)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSettingRepositoryGetCachesMissingKey(t *testing.T) {
	repo, mock := newSettingRepositorySQLMock(t)
	mock.ExpectQuery(`SELECT .* FROM "settings"`).
		WithArgs("missing").
		WillReturnRows(sqlmock.NewRows([]string{"id", "key", "value", "updated_at"}))

	_, err := repo.Get(context.Background(), "missing")
	require.ErrorIs(t, err, service.ErrSettingNotFound)
	_, err = repo.Get(context.Background(), "missing")
	require.ErrorIs(t, err, service.ErrSettingNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSettingRepositoryGetSingleflight(t *testing.T) {
	repo, mock := newSettingRepositorySQLMock(t)
	mock.ExpectQuery(`SELECT .* FROM "settings"`).
		WithArgs("shared").
		WillDelayFor(40 * time.Millisecond).
		WillReturnRows(settingRows(2, "shared", "value"))

	const callers = 12
	start := make(chan struct{})
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			value, err := repo.Get(context.Background(), "shared")
			if err == nil && value.Value != "value" {
				errs <- &unexpectedSettingValue{got: value.Value}
				return
			}
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSettingRepositoryGetSingleflightDoesNotShareCallerCancellation(t *testing.T) {
	repo, mock := newSettingRepositorySQLMock(t)
	mock.ExpectQuery(`SELECT .* FROM "settings"`).
		WithArgs("shared-after-cancel").
		WillDelayFor(60 * time.Millisecond).
		WillReturnRows(settingRows(3, "shared-after-cancel", "value"))

	leaderCtx, cancelLeader := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancelLeader()
	leaderErr := make(chan error, 1)
	go func() {
		_, err := repo.Get(leaderCtx, "shared-after-cancel")
		leaderErr <- err
	}()

	// Join the existing flight before the leader's request deadline expires.
	time.Sleep(5 * time.Millisecond)
	waiterValue, waiterErr := repo.Get(context.Background(), "shared-after-cancel")
	require.NoError(t, waiterErr)
	require.Equal(t, "value", waiterValue.Value)
	require.ErrorIs(t, <-leaderErr, context.DeadlineExceeded)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSettingRepositoryGetInvalidationStartsNewSingleflightGeneration(t *testing.T) {
	repo, mock := newSettingRepositorySQLMock(t)
	mock.MatchExpectationsInOrder(true)
	mock.ExpectQuery(`SELECT .* FROM "settings"`).
		WithArgs("changed-during-load").
		WillDelayFor(80 * time.Millisecond).
		WillReturnRows(settingRows(4, "changed-during-load", "old"))
	mock.ExpectQuery(`SELECT .* FROM "settings"`).
		WithArgs("changed-during-load").
		WillReturnRows(settingRows(4, "changed-during-load", "new"))

	oldRead := make(chan *service.Setting, 1)
	oldErr := make(chan error, 1)
	go func() {
		value, err := repo.Get(context.Background(), "changed-during-load")
		oldRead <- value
		oldErr <- err
	}()

	// Allow the old generation to enter the database query, then model the
	// invalidation performed after a successful Set/Delete. A read that starts
	// after this point must not join the old in-flight query.
	time.Sleep(15 * time.Millisecond)
	repo.invalidateKeyCache("changed-during-load")

	newValue, err := repo.Get(context.Background(), "changed-during-load")
	require.NoError(t, err)
	require.Equal(t, "new", newValue.Value)
	require.NoError(t, <-oldErr)
	require.Equal(t, "old", (<-oldRead).Value)
	require.NoError(t, mock.ExpectationsWereMet())
}

type unexpectedSettingValue struct{ got string }

func (e *unexpectedSettingValue) Error() string { return "unexpected setting value: " + e.got }

func TestSettingRepositoryGetAllReturnsCopyAndCaches(t *testing.T) {
	repo, mock := newSettingRepositorySQLMock(t)
	mock.ExpectQuery(`SELECT .* FROM "settings"`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "key", "value", "updated_at"}).
			AddRow(1, "a", "one", time.Unix(10, 0)).
			AddRow(2, "b", "two", time.Unix(11, 0)))

	first, err := repo.GetAll(context.Background())
	require.NoError(t, err)
	first["a"] = "mutated"
	second, err := repo.GetAll(context.Background())
	require.NoError(t, err)
	require.Equal(t, "one", second["a"])
	require.Equal(t, "two", second["b"])
	require.NoError(t, mock.ExpectationsWereMet())
}
