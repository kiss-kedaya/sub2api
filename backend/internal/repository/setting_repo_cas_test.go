package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestSettingRepositoryCompareAndSwapGuardsExpectedValueAndInvalidatesCaches(t *testing.T) {
	for _, affected := range []int64{0, 1} {
		t.Run(map[int64]string{0: "conflict", 1: "updated"}[affected], func(t *testing.T) {
			repo, mock := newSettingRepositorySQLMock(t)
			const key, original, updated = "crypto.config", `{"rpc":{}}`, `{"rpc":{"bsc":"https://fixture.test"}}`
			repo.keyCache[key] = settingCacheEntry{found: true, setting: &service.Setting{Key: key, Value: original}, expiresAt: time.Now().Add(time.Minute).UnixNano()}
			repo.allCache = &settingAllCacheEntry{values: map[string]string{key: original}, expiresAt: time.Now().Add(time.Minute).UnixNano()}
			mock.ExpectExec(`UPDATE "settings" SET .* WHERE .*"key" = \$3 AND .*"value" = \$4`).
				WithArgs(updated, sqlmock.AnyArg(), key, original).WillReturnResult(sqlmock.NewResult(0, affected))
			expected := original
			changed, err := repo.CompareAndSwapValue(context.Background(), key, &expected, updated)
			require.NoError(t, err)
			require.Equal(t, affected == 1, changed)
			_, cached := repo.peekKeyCache(key)
			require.False(t, cached, "a conflict also requires the refresh to read current data")
			require.Nil(t, repo.allCache)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestSettingRepositoryCompareAndSwapInitialInsertNeverOverwritesExistingKey(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(map[bool]string{false: "inserted", true: "already exists"}[conflict], func(t *testing.T) {
			repo, mock := newSettingRepositorySQLMock(t)
			rows := sqlmock.NewRows([]string{"id"})
			if !conflict {
				rows.AddRow(int64(8))
			}
			mock.ExpectQuery(`INSERT INTO "settings" .* ON CONFLICT \("key"\) DO NOTHING RETURNING "id"`).
				WithArgs("crypto.config", "{}", sqlmock.AnyArg()).WillReturnRows(rows)
			changed, err := repo.CompareAndSwapValue(context.Background(), "crypto.config", nil, "{}")
			require.NoError(t, err)
			require.Equal(t, !conflict, changed)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestSettingRepositoryCompareAndSwapPropagatesDatabaseFailure(t *testing.T) {
	repo, mock := newSettingRepositorySQLMock(t)
	failure := errors.New("database offline")
	mock.ExpectExec(`UPDATE "settings"`).WillReturnError(failure)
	expected := "{}"
	changed, err := repo.CompareAndSwapValue(context.Background(), "crypto.config", &expected, "{}")
	require.False(t, changed)
	require.ErrorIs(t, err, failure)
	require.NoError(t, mock.ExpectationsWereMet())
}
