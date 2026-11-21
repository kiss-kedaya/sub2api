package repository

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// 迁移 327 之后：新建 key 只落 sha256 哈希，明文列保持 NULL，独立哈希表同事务双写。
func TestAPIKeyRepository_CreateStoresHashOnly(t *testing.T) {
	repo, client := newAPIKeyRepoSQLite(t)
	ctx := context.Background()
	user := mustCreateAPIKeyRepoUser(t, ctx, client, "hash-create@test.com")

	plaintext := "sk-" + strings.Repeat("a1b2c3d4", 8)
	key := &service.APIKey{
		UserID: user.ID,
		Key:    plaintext,
		Name:   "hash-create",
		Status: service.StatusActive,
	}
	require.NoError(t, repo.Create(ctx, key))

	require.Equal(t, service.HashAPIKey(plaintext), key.KeyHash)
	require.Equal(t, plaintext[:8], key.KeyPrefix)

	var storedKey sql.NullString
	var storedHash, storedPrefix string
	require.NoError(t, scanSingleRow(ctx, repo.sql, `SELECT key, key_hash, key_prefix FROM api_keys WHERE id = $1`, []any{key.ID}, &storedKey, &storedHash, &storedPrefix))
	require.False(t, storedKey.Valid, "plaintext column must never be written for new rows")
	require.Equal(t, service.HashAPIKey(plaintext), storedHash)
	require.Equal(t, plaintext[:8], storedPrefix)

	// 独立哈希表同事务双写。
	var sideHash, sidePrefix string
	require.NoError(t, scanSingleRow(ctx, repo.sql, `SELECT key_hash, key_prefix FROM api_key_hashes WHERE api_key_id = $1`, []any{key.ID}, &sideHash, &sidePrefix))
	require.Equal(t, service.HashAPIKey(plaintext), sideHash)
	require.Equal(t, plaintext[:8], sidePrefix)
}

// 鉴权命中走 key_hash：明文列已经是 NULL，仍然能查到。
func TestAPIKeyRepository_GetByKeyHitsStoredHash(t *testing.T) {
	repo, client := newAPIKeyRepoSQLite(t)
	ctx := context.Background()
	user := mustCreateAPIKeyRepoUser(t, ctx, client, "hash-auth@test.com")

	plaintext := "sk-" + strings.Repeat("f00dbabe", 8)
	key := &service.APIKey{UserID: user.ID, Key: plaintext, Name: "hash-auth", Status: service.StatusActive}
	require.NoError(t, repo.Create(ctx, key))

	got, err := repo.GetByKeyForAuth(ctx, plaintext)
	require.NoError(t, err)
	require.Equal(t, key.ID, got.ID)
	require.Equal(t, service.HashAPIKey(plaintext), got.KeyHash)
	require.Empty(t, got.Key, "repository must not load plaintext")

	// 错误凭据（另一个 key）不应该命中。
	_, err = repo.GetByKeyForAuth(ctx, "sk-"+strings.Repeat("deadbeef", 8))
	require.ErrorIs(t, err, service.ErrAPIKeyNotFound)
}

// 过渡期回退：key_hash 还没回填的历史行只能靠明文列命中，且回退成功后不写回。
func TestAPIKeyRepository_GetByKeyFallsBackToLegacyPlaintext(t *testing.T) {
	repo, client := newAPIKeyRepoSQLite(t)
	ctx := context.Background()
	user := mustCreateAPIKeyRepoUser(t, ctx, client, "hash-fallback@test.com")

	legacyPlaintext := "sk-" + strings.Repeat("0badc0de", 8)
	key := &service.APIKey{UserID: user.ID, Key: legacyPlaintext, Name: "legacy", Status: service.StatusActive}
	require.NoError(t, repo.Create(ctx, key))

	// 模拟「327 已上线、生产回填还没跑」：明文还在，哈希为空。
	require.NoError(t, mustExec(ctx, repo.sql,
		`UPDATE api_keys SET key = $1, key_hash = NULL WHERE id = $2`, legacyPlaintext, key.ID))
	require.NoError(t, mustExec(ctx, repo.sql,
		`DELETE FROM api_key_hashes WHERE api_key_id = $1`, key.ID))

	got, err := repo.GetByKeyForAuth(ctx, legacyPlaintext)
	require.NoError(t, err)
	require.Equal(t, key.ID, got.ID)

	// 回退成功后不得把明文写回哈希（也不接受任何明文写回）。
	var stored keyMaterial
	require.NoError(t, scanSingleRow(ctx, repo.sql, `SELECT key, key_hash FROM api_keys WHERE id = $1`, []any{key.ID}, &stored.Key, &stored.KeyHash))
	require.False(t, stored.KeyHash.Valid, "fallback lookup must not write key_hash back")
	require.True(t, stored.Key.Valid)
}

// 轮换：旧 key 立刻失效（哈希被覆盖 + 明文列清 NULL），新 key 可鉴权。
func TestAPIKeyRepository_RotateReplacesStoredHash(t *testing.T) {
	repo, client := newAPIKeyRepoSQLite(t)
	ctx := context.Background()
	user := mustCreateAPIKeyRepoUser(t, ctx, client, "hash-rotate@test.com")

	oldPlaintext := "sk-" + strings.Repeat("11112222", 8)
	key := &service.APIKey{UserID: user.ID, Key: oldPlaintext, Name: "rotate", Status: service.StatusActive}
	require.NoError(t, repo.Create(ctx, key))

	loaded, err := repo.GetByID(ctx, key.ID)
	require.NoError(t, err)
	newPlaintext := "sk-" + strings.Repeat("33334444", 8)
	loaded.Key = newPlaintext
	loaded.KeyHash = ""
	loaded.KeyPrefix = ""
	require.NoError(t, repo.Update(ctx, loaded, service.APIKeyUpdateFields{Key: true}))

	// 新 key 命中；旧 key 的哈希已经被覆盖，明文列为 NULL，回退也命中不了。
	got, err := repo.GetByKeyForAuth(ctx, newPlaintext)
	require.NoError(t, err)
	require.Equal(t, key.ID, got.ID)
	_, err = repo.GetByKeyForAuth(ctx, oldPlaintext)
	require.ErrorIs(t, err, service.ErrAPIKeyNotFound)

	var hash string
	var storedKey sql.NullString
	require.NoError(t, scanSingleRow(ctx, repo.sql, `SELECT key, key_hash FROM api_keys WHERE id = $1`, []any{key.ID}, &storedKey, &hash))
	require.False(t, storedKey.Valid, "rotation must clear the plaintext column")
	require.Equal(t, service.HashAPIKey(newPlaintext), hash)

	var sideHash string
	require.NoError(t, scanSingleRow(ctx, repo.sql, `SELECT key_hash FROM api_key_hashes WHERE api_key_id = $1`, []any{key.ID}, &sideHash))
	require.Equal(t, service.HashAPIKey(newPlaintext), sideHash)
}

// 删除：软删同时清空明文/哈希并释放独立哈希表，同一个自定义 key 可以重新创建。
func TestAPIKeyRepository_DeleteClearsCredentialMaterial(t *testing.T) {
	repo, client := newAPIKeyRepoSQLite(t)
	ctx := context.Background()
	user := mustCreateAPIKeyRepoUser(t, ctx, client, "hash-delete@test.com")

	plaintext := "sk-" + strings.Repeat("55667788", 8)
	key := &service.APIKey{UserID: user.ID, Key: plaintext, Name: "delete", Status: service.StatusActive}
	require.NoError(t, repo.Create(ctx, key))
	require.NoError(t, repo.DeleteWithAudit(ctx, key.ID))

	var storedKey, storedHash sql.NullString
	require.NoError(t, scanSingleRow(ctx, repo.sql, `SELECT key, key_hash FROM api_keys WHERE id = $1`, []any{key.ID}, &storedKey, &storedHash))
	require.False(t, storedKey.Valid)
	require.False(t, storedHash.Valid)

	var sideCount int
	require.NoError(t, scanSingleRow(ctx, repo.sql, `SELECT COUNT(*) FROM api_key_hashes WHERE api_key_id = $1`, []any{key.ID}, &sideCount))
	require.Zero(t, sideCount)

	// 唯一索引只约束活着的 key：同值自定义 key 建回来不应冲突。
	recreated := &service.APIKey{UserID: user.ID, Key: plaintext, Name: "delete-again", Status: service.StatusActive}
	require.NoError(t, repo.Create(ctx, recreated))
}

// ExistsByKey 过渡期必须同时识别哈希（新行）和明文（未回填历史行）。
func TestAPIKeyRepository_ExistsByKeyChecksBothStorages(t *testing.T) {
	repo, client := newAPIKeyRepoSQLite(t)
	ctx := context.Background()
	user := mustCreateAPIKeyRepoUser(t, ctx, client, "hash-exists@test.com")

	hashedPlaintext := "sk-" + strings.Repeat("9999aaaa", 8)
	hashed := &service.APIKey{UserID: user.ID, Key: hashedPlaintext, Name: "hashed", Status: service.StatusActive}
	require.NoError(t, repo.Create(ctx, hashed))
	exists, err := repo.ExistsByKey(ctx, hashedPlaintext)
	require.NoError(t, err)
	require.True(t, exists)

	legacyPlaintext := "sk-" + strings.Repeat("bbbb8888", 8)
	legacy := &service.APIKey{UserID: user.ID, Key: legacyPlaintext, Name: "legacy", Status: service.StatusActive}
	require.NoError(t, repo.Create(ctx, legacy))
	require.NoError(t, mustExec(ctx, repo.sql,
		`UPDATE api_keys SET key = $1, key_hash = NULL WHERE id = $2`, legacyPlaintext, legacy.ID))
	exists, err = repo.ExistsByKey(ctx, legacyPlaintext)
	require.NoError(t, err)
	require.True(t, exists, "unhashed legacy rows are still protected by the plaintext path")
}

// GetKeyHashAndOwnerID：新版行返回哈希，未回填的历史行回退明文，删除路径据此失效缓存。
func TestAPIKeyRepository_GetKeyHashAndOwnerID(t *testing.T) {
	repo, client := newAPIKeyRepoSQLite(t)
	ctx := context.Background()
	user := mustCreateAPIKeyRepoUser(t, ctx, client, "hash-owner@test.com")

	plaintext := "sk-" + strings.Repeat("ccccdddd", 8)
	key := &service.APIKey{UserID: user.ID, Key: plaintext, Name: "owner", Status: service.StatusActive}
	require.NoError(t, repo.Create(ctx, key))

	credential, ownerID, err := repo.GetKeyHashAndOwnerID(ctx, key.ID)
	require.NoError(t, err)
	require.Equal(t, user.ID, ownerID)
	require.Equal(t, service.HashAPIKey(plaintext), credential)

	require.NoError(t, mustExec(ctx, repo.sql,
		`UPDATE api_keys SET key = $1, key_hash = NULL WHERE id = $2`, plaintext, key.ID))
	credential, ownerID, err = repo.GetKeyHashAndOwnerID(ctx, key.ID)
	require.NoError(t, err)
	require.Equal(t, user.ID, ownerID)
	require.Equal(t, plaintext, credential, "legacy rows fall back to plaintext for cache invalidation")
}

type keyMaterial struct {
	Key     sql.NullString
	KeyHash sql.NullString
}

func mustExec(ctx context.Context, db interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, query string, args ...any) error {
	_, err := db.ExecContext(ctx, query, args...)
	return err
}
