package repository

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func newTwoFactorGrantTestCache(t *testing.T) (*TotpCache, *redis.Client, *miniredis.Miniredis) {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return &TotpCache{rdb: client}, client, server
}

func twoFactorGrantTestRecord(userID int64) *service.TwoFactorGrantRecord {
	now := time.Now()
	return &service.TwoFactorGrantRecord{
		UserID:    userID,
		Scope:     service.TwoFactorGrantScopeLogin,
		IPHash:    "ip-hash",
		UAHash:    "ua-hash",
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(service.TwoFactorGrantTTL).Unix(),
	}
}

// Redis 里只允许出现 sha256(token) 作为 key，value 是不含 token 的绑定记录。
func TestTwoFactorGrantCacheStoresOnlyDigestKey(t *testing.T) {
	cache, client, _ := newTwoFactorGrantTestCache(t)
	ctx := context.Background()
	digest := "a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90"
	record := twoFactorGrantTestRecord(42)

	require.NoError(t, cache.SaveTwoFactorGrant(ctx, digest, record, service.TwoFactorGrantTTL))

	key := twoFactorGrantKey(digest)
	raw, err := client.Get(ctx, key).Bytes()
	require.NoError(t, err)
	require.NotContains(t, string(raw), digest)
	var stored service.TwoFactorGrantRecord
	require.NoError(t, json.Unmarshal(raw, &stored))
	require.Equal(t, int64(42), stored.UserID)
	require.Equal(t, "ip-hash", stored.IPHash)

	// 用户维度索引里是完整 grant key，供按用户撤销。
	members, err := client.SMembers(ctx, twoFactorGrantUserKey(42)).Result()
	require.NoError(t, err)
	require.Equal(t, []string{key}, members)

	loaded, err := cache.LoadTwoFactorGrant(ctx, digest)
	require.NoError(t, err)
	require.NotNil(t, loaded)
	require.Equal(t, record.ExpiresAt, loaded.ExpiresAt)
}

func TestTwoFactorGrantCacheLoadMissingReturnsNil(t *testing.T) {
	cache, _, _ := newTwoFactorGrantTestCache(t)
	record, err := cache.LoadTwoFactorGrant(context.Background(), "missing")
	require.NoError(t, err)
	require.Nil(t, record)
}

// 凭证记录必须随 5 分钟窗口过期，不做无限滑动。
func TestTwoFactorGrantCacheHonoursFixedTTL(t *testing.T) {
	cache, _, server := newTwoFactorGrantTestCache(t)
	ctx := context.Background()
	record := twoFactorGrantTestRecord(42)
	require.NoError(t, cache.SaveTwoFactorGrant(ctx, "digest", record, service.TwoFactorGrantTTL))

	server.FastForward(service.TwoFactorGrantTTL - time.Second)
	loaded, err := cache.LoadTwoFactorGrant(ctx, "digest")
	require.NoError(t, err)
	require.NotNil(t, loaded, "4:59 时凭证仍然存在")

	server.FastForward(2 * time.Second)
	loaded, err = cache.LoadTwoFactorGrant(ctx, "digest")
	require.NoError(t, err)
	require.Nil(t, loaded, "5:01 时凭证必须过期")
}

func TestTwoFactorGrantCacheRevokeForUserDeletesAllRecords(t *testing.T) {
	cache, client, _ := newTwoFactorGrantTestCache(t)
	ctx := context.Background()
	require.NoError(t, cache.SaveTwoFactorGrant(ctx, "digest-a", twoFactorGrantTestRecord(42), service.TwoFactorGrantTTL))
	require.NoError(t, cache.SaveTwoFactorGrant(ctx, "digest-b", twoFactorGrantTestRecord(42), service.TwoFactorGrantTTL))
	require.NoError(t, cache.SaveTwoFactorGrant(ctx, "digest-c", twoFactorGrantTestRecord(43), service.TwoFactorGrantTTL))

	revoked, err := cache.RevokeTwoFactorGrantsForUser(ctx, 42)
	require.NoError(t, err)
	require.EqualValues(t, 2, revoked)

	for _, digest := range []string{"digest-a", "digest-b"} {
		exists, err := client.Exists(ctx, twoFactorGrantKey(digest)).Result()
		require.NoError(t, err)
		require.Zero(t, exists)
	}
	exists, err := client.Exists(ctx, twoFactorGrantKey("digest-c")).Result()
	require.NoError(t, err)
	require.EqualValues(t, 1, exists, "不能误删其他用户的凭证")
	exists, err = client.Exists(ctx, twoFactorGrantUserKey(42)).Result()
	require.NoError(t, err)
	require.Zero(t, exists, "用户索引集合一并删除")
}

func TestTwoFactorGrantCacheDeleteSingleRecord(t *testing.T) {
	cache, client, _ := newTwoFactorGrantTestCache(t)
	ctx := context.Background()
	require.NoError(t, cache.SaveTwoFactorGrant(ctx, "digest", twoFactorGrantTestRecord(42), service.TwoFactorGrantTTL))
	require.NoError(t, cache.DeleteTwoFactorGrant(ctx, "digest"))
	exists, err := client.Exists(ctx, twoFactorGrantKey("digest")).Result()
	require.NoError(t, err)
	require.Zero(t, exists)
}

// 校验限速：同一用户+IP 60 秒窗口内计数，窗口过后自动归零。
func TestTwoFactorGrantCacheVerifyAttemptWindow(t *testing.T) {
	cache, _, server := newTwoFactorGrantTestCache(t)
	ctx := context.Background()
	for i := 1; i <= 10; i++ {
		count, err := cache.IncrementTwoFactorGrantVerifyAttempt(ctx, 42, "ip-hash", time.Minute)
		require.NoError(t, err)
		require.EqualValues(t, i, count)
	}
	count, err := cache.IncrementTwoFactorGrantVerifyAttempt(ctx, 42, "ip-hash", time.Minute)
	require.NoError(t, err)
	require.EqualValues(t, 11, count)

	server.FastForward(61 * time.Second)
	count, err = cache.IncrementTwoFactorGrantVerifyAttempt(ctx, 42, "ip-hash", time.Minute)
	require.NoError(t, err)
	require.EqualValues(t, 1, count, "窗口过后重新计数")
}

// 损坏的记录不能被当成有效凭证：Load 返回错误，服务层据此 fail-closed。
func TestTwoFactorGrantCacheRejectsCorruptRecord(t *testing.T) {
	cache, client, _ := newTwoFactorGrantTestCache(t)
	ctx := context.Background()
	require.NoError(t, client.Set(ctx, twoFactorGrantKey("bad"), "{not-json", time.Minute).Err())
	_, err := cache.LoadTwoFactorGrant(ctx, "bad")
	require.Error(t, err)
}

func TestTwoFactorGrantCacheRejectsInvalidArguments(t *testing.T) {
	cache, _, _ := newTwoFactorGrantTestCache(t)
	ctx := context.Background()
	require.Error(t, cache.SaveTwoFactorGrant(ctx, "", twoFactorGrantTestRecord(42), time.Minute))
	require.Error(t, cache.SaveTwoFactorGrant(ctx, "digest", nil, time.Minute))
	require.Error(t, cache.SaveTwoFactorGrant(ctx, "digest", twoFactorGrantTestRecord(0), time.Minute))
	require.Error(t, cache.SaveTwoFactorGrant(ctx, "digest", twoFactorGrantTestRecord(42), 0))
	_, err := cache.RevokeTwoFactorGrantsForUser(ctx, 0)
	require.Error(t, err)
	_, err = cache.IncrementTwoFactorGrantVerifyAttempt(ctx, 42, "", time.Minute)
	require.Error(t, err)
}
