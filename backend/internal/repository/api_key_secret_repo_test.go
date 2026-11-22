package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// 迁移 335 的 api_key_secrets 只在「当前这版明文可解」时有意义：
// 轮换必须让旧密文随旧 key 一起消失，删除也必须把密文清掉。
// 这里用 SQLite（与生产同一套 SQL 文本）覆盖级联路径。
func newAPIKeySecretTestObjects(t *testing.T) (*apiKeyRepository, *apiKeySecretRepository, *dbent.Client) {
	t.Helper()
	keyRepo, client := newAPIKeyRepoSQLite(t)
	db, ok := keyRepo.sql.(*sql.DB)
	require.True(t, ok, "sqlite helper must expose *sql.DB")
	return keyRepo, &apiKeySecretRepository{db: db}, client
}

func secretTestPayload(userID, apiKeyID int64) *service.APIKeySecret {
	kdf, _ := json.Marshal(service.APIKeySecretKDF{
		Algo:       service.APIKeySecretKDFAlgoPBKDF2SHA256,
		Iterations: 310000,
		Salt:       "MDEyMzQ1Njc4OWFiY2RlZg==",
	})
	return &service.APIKeySecret{
		APIKeyID: apiKeyID,
		UserID:   userID,
		Version:  service.APIKeySecretSupportedVersion,
		KDF:      kdf,
		Sealed:   "c2VhbGVkLWNpcGhlcnRleHQtZm9yLXRlc3RzLTAwMDE=",
	}
}

func TestAPIKeySecretRepositoryRoundTripAndPresence(t *testing.T) {
	repo, secretRepo, client := newAPIKeySecretTestObjects(t)
	ctx := context.Background()
	user := mustCreateAPIKeyRepoUser(t, ctx, client, "secret-roundtrip@test.com")
	key := &service.APIKey{UserID: user.ID, Key: "sk-" + "a1b2c3d4e5f60718", Name: "sealed", Status: service.StatusActive}
	require.NoError(t, repo.Create(ctx, key))

	secret := secretTestPayload(user.ID, key.ID)
	require.NoError(t, secretRepo.Upsert(ctx, secret))

	loaded, err := secretRepo.GetByAPIKeyID(ctx, key.ID, user.ID)
	require.NoError(t, err)
	require.Equal(t, secret.Sealed, loaded.Sealed)
	require.JSONEq(t, string(secret.KDF), string(loaded.KDF))
	require.Equal(t, service.APIKeySecretSupportedVersion, loaded.Version)

	// 别人的 user_id 读不到（带 user_id 过滤的第二道防线）。
	_, err = secretRepo.GetByAPIKeyID(ctx, key.ID, user.ID+1)
	require.ErrorIs(t, err, service.ErrAPIKeySecretNotFound)

	// upsert 覆盖而不是新增。
	secret.Sealed = "c2VhbGVkLWNpcGhlcnRleHQtcm90YXRlZC0wMDAwMg=="
	require.NoError(t, secretRepo.Upsert(ctx, secret))
	var count int
	require.NoError(t, scanSingleRow(ctx, repo.sql, `SELECT COUNT(*) FROM api_key_secrets WHERE api_key_id = $1`, []any{key.ID}, &count))
	require.Equal(t, 1, count)

	present, err := secretRepo.ListPresenceByAPIKeyIDs(ctx, user.ID, []int64{key.ID, key.ID + 1})
	require.NoError(t, err)
	require.Contains(t, present, key.ID)
	require.NotContains(t, present, key.ID+1)
}

func TestAPIKeySecretRepositoryRotationDropsSealedSecret(t *testing.T) {
	repo, secretRepo, client := newAPIKeySecretTestObjects(t)
	ctx := context.Background()
	user := mustCreateAPIKeyRepoUser(t, ctx, client, "secret-rotate@test.com")
	key := &service.APIKey{UserID: user.ID, Key: "sk-" + "1111222233334444", Name: "rotate-sealed", Status: service.StatusActive}
	require.NoError(t, repo.Create(ctx, key))
	require.NoError(t, secretRepo.Upsert(ctx, secretTestPayload(user.ID, key.ID)))

	loaded, err := repo.GetByID(ctx, key.ID)
	require.NoError(t, err)
	loaded.Key = "sk-" + "5555666677778888"
	loaded.KeyHash = ""
	loaded.KeyPrefix = ""
	require.NoError(t, repo.Update(ctx, loaded, service.APIKeyUpdateFields{Key: true}))

	// 轮换后旧密文必须消失，必须由前端拿新明文重新封装。
	_, err = secretRepo.GetByAPIKeyID(ctx, key.ID, user.ID)
	require.ErrorIs(t, err, service.ErrAPIKeySecretNotFound)
}

func TestAPIKeySecretRepositoryDeleteDropsSealedSecret(t *testing.T) {
	repo, secretRepo, client := newAPIKeySecretTestObjects(t)
	ctx := context.Background()
	user := mustCreateAPIKeyRepoUser(t, ctx, client, "secret-delete@test.com")
	key := &service.APIKey{UserID: user.ID, Key: "sk-" + "aaaabbbbccccdddd", Name: "delete-sealed", Status: service.StatusActive}
	require.NoError(t, repo.Create(ctx, key))
	require.NoError(t, secretRepo.Upsert(ctx, secretTestPayload(user.ID, key.ID)))

	require.NoError(t, repo.DeleteWithAudit(ctx, key.ID))
	_, err := secretRepo.GetByAPIKeyID(ctx, key.ID, user.ID)
	require.ErrorIs(t, err, service.ErrAPIKeySecretNotFound)

	// DeleteByAPIKeyID 重复删除幂等。
	require.NoError(t, secretRepo.DeleteByAPIKeyID(ctx, key.ID, user.ID))
}
