//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func (s *APIKeyRepoSuite) TestInPlaceRotationQualifiesWithoutChangingCreationTime() {
	account := s.mustCreateUser("key-rotation-fixture@example.invalid")
	assertKeyRotationPersistence(s.T(), s.ctx, s.repo, account.ID)
}

func assertKeyRotationPersistence(t *testing.T, ctx context.Context, repo *apiKeyRepository, userID int64) {
	t.Helper()
	cutoff := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	created := cutoff.Add(-time.Hour)
	key := &service.APIKey{UserID: userID, Name: "rotation fixture", Key: "fixture-rotation-original", Status: service.StatusAPIKeyActive}
	require.NoError(t, repo.Create(ctx, key))
	oldValue := key.Key
	_, err := repo.sql.ExecContext(ctx, "UPDATE api_keys SET created_at = $1 WHERE id = $2", created, key.ID)
	require.NoError(t, err)

	key.Name = "ordinary edit"
	require.NoError(t, repo.Update(ctx, key, service.APIKeyUpdateFields{Name: true}))
	require.NoError(t, repo.UpdateLastUsed(ctx, key.ID, time.Now().UTC()))
	read := func() service.APIKey {
		a, err := repo.ListAllByUserID(ctx, userID, service.APIKeyListFilters{Status: service.StatusAPIKeyActive})
		require.NoError(t, err)
		require.Len(t, a, 1)
		require.Equal(t, key.ID, a[0].ID)
		require.True(t, a[0].CreatedAt.Equal(created))
		return a[0]
	}
	prior := read()
	require.Nil(t, prior.KeyRotatedAt)
	require.False(t, service.IsKeyRotationCompliant(prior, cutoff))

	key.Key = "fixture-rotation-replacement"
	require.NoError(t, repo.Update(ctx, key, service.APIKeyUpdateFields{Key: true}))
	_, err = repo.GetByKey(ctx, oldValue)
	require.ErrorIs(t, err, service.ErrAPIKeyNotFound)
	after := read()
	require.NotNil(t, after.KeyRotatedAt)
	require.NotNil(t, key.KeyRotatedAt)
	require.True(t, key.KeyRotatedAt.Equal(*after.KeyRotatedAt), "the rotation receipt must match a fresh list")
	require.True(t, service.IsKeyRotationCompliant(after, cutoff))
	// 明文不再落库：轮换后库里只有新哈希与展示前缀。
	require.Empty(t, after.Key)
	require.Equal(t, service.HashAPIKey(key.Key), after.KeyHash)
	require.Equal(t, service.APIKeyPrefixFromPlaintext(key.Key), after.KeyPrefix)
	key.Name = "ordinary edit after rotation"
	require.NoError(t, repo.Update(ctx, key, service.APIKeyUpdateFields{Name: true}))
	again := read()
	require.NotNil(t, again.KeyRotatedAt)
	require.True(t, again.KeyRotatedAt.Equal(*after.KeyRotatedAt))
}
