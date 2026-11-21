//go:build unit

package service

import (
	"context"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// SHA-256 标准测试向量：空串的摘要固定，用来证明实现没被加盐/改算法。
func TestHashAPIKeyIsPlainSHA256Hex(t *testing.T) {
	require.Equal(t,
		"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		HashAPIKey(""))
	digest := HashAPIKey("sk-0123456789abcdef")
	require.Len(t, digest, APIKeyHashHexLen)
	require.Equal(t, strings.ToLower(digest), digest)
	require.True(t, IsAPIKeyHash(digest))
	require.NotEqual(t, digest, HashAPIKey("sk-0123456789abcdee"))
	require.False(t, IsAPIKeyHash("sk-0123456789abcdef"))
	require.False(t, IsAPIKeyHash(strings.ToUpper(digest)[:63]+"z"))
}

func TestAPIKeyPrefixFromPlaintext(t *testing.T) {
	require.Equal(t, "sk-abcde", APIKeyPrefixFromPlaintext("sk-abcdefgh1234567890"))
	require.Equal(t, "", APIKeyPrefixFromPlaintext("short"))
	require.Equal(t, "", APIKeyPrefixFromPlaintext(""))
}

// 创建：服务层必须先把哈希/前缀补齐再交给仓库，响应里保留明文（唯一一次回执）。
func TestAPIKeyService_CreateFillsHashForStorage(t *testing.T) {
	repo := &createLimitAPIKeyRepoStub{apiKeyRepoStub: &apiKeyRepoStub{}}
	cache := &apiKeyCacheStub{}
	svc := &APIKeyService{
		apiKeyRepo: repo,
		userRepo:   &userRepoStub{user: &User{ID: 7}},
		cache:      cache,
		cfg:        &config.Config{},
	}

	key, err := svc.Create(context.Background(), 7, CreateAPIKeyRequest{Name: "hash-create"})
	require.NoError(t, err)
	require.NotEmpty(t, key.Key)
	require.Len(t, repo.created, 1)

	stored := repo.created[0]
	require.Equal(t, HashAPIKey(key.Key), stored.KeyHash)
	require.Equal(t, APIKeyPrefixFromPlaintext(key.Key), stored.KeyPrefix)
	require.Equal(t, key.Key, stored.Key, "创建回执仍然要在内存对象里带一次明文")
}

// 轮换：旧哈希与新哈希的认证缓存都要立刻失效，且新哈希写回对象。
func TestAPIKeyService_RotateInvalidatesOldAndNewHash(t *testing.T) {
	oldHash := HashAPIKey("sk-old-plaintext")
	repo := &createLimitAPIKeyRepoStub{apiKeyRepoStub: &apiKeyRepoStub{apiKey: &APIKey{
		ID:        10,
		UserID:    7,
		KeyHash:   oldHash,
		KeyPrefix: "sk-old-p",
		Name:      "rotate",
		Status:    StatusActive,
	}}}
	cache := &apiKeyCacheStub{}
	svc := &APIKeyService{
		apiKeyRepo: repo,
		userRepo:   &userRepoStub{user: &User{ID: 7}},
		cache:      cache,
		cfg:        &config.Config{},
	}

	rotated, err := svc.Update(context.Background(), 10, 7, UpdateAPIKeyRequest{RegenerateKey: true})
	require.NoError(t, err)
	require.NotEmpty(t, rotated.Key)
	require.NotEqual(t, oldHash, rotated.KeyHash)
	require.Equal(t, HashAPIKey(rotated.Key), rotated.KeyHash)

	require.Len(t, repo.updatedKeys, 1)
	require.Equal(t, rotated.KeyHash, repo.updatedKeys[0].KeyHash)
	require.Equal(t, APIKeyPrefixFromPlaintext(rotated.Key), repo.updatedKeys[0].KeyPrefix)

	require.Contains(t, cache.deleteAuthKeys, oldHash, "old credential cache must be dropped")
	require.Contains(t, cache.deleteAuthKeys, rotated.KeyHash, "new credential cache must be dropped")
}

// 删除：库里读出来的行没有明文，只能按 key_hash 失效缓存。
func TestAPIKeyService_DeleteInvalidatesStoredHash(t *testing.T) {
	storedHash := HashAPIKey("sk-deleted-plaintext")
	repo := &apiKeyRepoStub{apiKey: &APIKey{
		ID:      22,
		UserID:  7,
		KeyHash: storedHash,
		Status:  StatusActive,
	}}
	cache := &apiKeyCacheStub{}
	svc := &APIKeyService{apiKeyRepo: repo, cache: cache}

	require.NoError(t, svc.Delete(context.Background(), 22, 7))
	require.Equal(t, []int64{22}, repo.deletedIDs)
	require.Contains(t, cache.deleteAuthKeys, storedHash)
}

// 鉴权路径：请求里的明文会回填到对象上，同时哈希必须可用（供后续失效）。
func TestAPIKeyService_GetByKeyKeepsHashForInvalidation(t *testing.T) {
	plaintext := "sk-" + strings.Repeat("ab12cd34", 8)
	storedHash := HashAPIKey(plaintext)
	repo := &authRepoStub{
		getByKeyForAuth: func(context.Context, string) (*APIKey, error) {
			return &APIKey{ID: 33, UserID: 7, Status: StatusActive, KeyHash: storedHash,
				User: &User{ID: 7, Status: StatusActive}}, nil
		},
	}
	svc := &APIKeyService{apiKeyRepo: repo, cfg: &config.Config{}}

	key, err := svc.GetByKey(context.Background(), plaintext)
	require.NoError(t, err)
	require.Equal(t, plaintext, key.Key)
	require.Equal(t, storedHash, key.KeyHash)
	require.Equal(t, plaintext[:8], key.KeyPrefix)
}

// 未回填哈希的历史行：鉴权回退后对象上仍要能算出哈希与前缀。
func TestAPIKeyService_GetByKeyFillsHashForLegacyRow(t *testing.T) {
	plaintext := "sk-" + strings.Repeat("ef56ab78", 8)
	repo := &authRepoStub{
		getByKeyForAuth: func(context.Context, string) (*APIKey, error) {
			return &APIKey{ID: 34, UserID: 7, Status: StatusActive,
				User: &User{ID: 7, Status: StatusActive}}, nil
		},
	}
	svc := &APIKeyService{apiKeyRepo: repo, cfg: &config.Config{}}

	key, err := svc.GetByKey(context.Background(), plaintext)
	require.NoError(t, err)
	require.Equal(t, HashAPIKey(plaintext), key.KeyHash)
	require.Equal(t, plaintext[:8], key.KeyPrefix)
}

func TestAPIKeyService_InvalidateAuthCacheByHash(t *testing.T) {
	cache := &apiKeyCacheStub{}
	svc := &APIKeyService{cache: cache}
	storedHash := HashAPIKey("sk-cache-target")

	svc.InvalidateAuthCacheByHash(context.Background(), storedHash)
	svc.InvalidateAuthCacheByHash(context.Background(), "not-a-hash")
	require.Equal(t, []string{storedHash}, cache.deleteAuthKeys)
}

// 删除路径只知道「凭据标识」一种值：哈希与明文两种解释都要清掉缓存。
func TestAPIKeyService_InvalidateAuthCacheByCredential(t *testing.T) {
	cache := &apiKeyCacheStub{}
	svc := &APIKeyService{cache: cache}

	plaintext := "sk-" + strings.Repeat("99887766", 8)
	svc.InvalidateAuthCacheByCredential(context.Background(), plaintext)
	require.Contains(t, cache.deleteAuthKeys, HashAPIKey(plaintext))
}
