package repository

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// API Key 三选一身份校验的 Redis 存储契约：
//   - reveal grant 5 分钟有效、一次性消费（GETDEL），按 user+key+factor 绑定；
//   - Passkey 校验票据 5 分钟有效、一次性消费；
//   - Redis 里只有 token 的 SHA-256 摘要与记录，没有 token 明文。
func newAPIKeyVerificationCacheForTest(t *testing.T) (*miniredis.Miniredis, service.APIKeyVerificationStore) {
	t.Helper()
	server := miniredis.RunT(t)
	cache := NewTotpCache(redis.NewClient(&redis.Options{Addr: server.Addr()}))
	store, ok := cache.(service.APIKeyVerificationStore)
	require.True(t, ok, "TotpCache must implement APIKeyVerificationStore")
	return server, store
}

func TestAPIKeyRevealGrantIsOneShotWithFiveMinuteTTL(t *testing.T) {
	server, store := newAPIKeyVerificationCacheForTest(t)
	ctx := context.Background()
	digest := "sha256-digest-fixture"
	record := &service.APIKeyRevealGrantRecord{
		UserID:    7,
		APIKeyID:  42,
		Factor:    service.APIKeyFactorPassword,
		ExpiresAt: time.Now().Add(service.APIKeyRevealGrantTTL).Unix(),
	}
	require.NoError(t, store.SaveAPIKeyRevealGrant(ctx, digest, record, service.APIKeyRevealGrantTTL))
	require.Equal(t, int(service.APIKeyRevealGrantTTL/time.Second), int(server.TTL(apiKeyRevealGrantKey(digest))/time.Second))

	loaded, err := store.ConsumeAPIKeyRevealGrant(ctx, digest)
	require.NoError(t, err)
	require.NotNil(t, loaded)
	require.Equal(t, record.UserID, loaded.UserID)
	require.Equal(t, record.APIKeyID, loaded.APIKeyID)
	require.Equal(t, record.Factor, loaded.Factor)

	// 第二次消费必须拿不到：grant 是一次性的。
	again, err := store.ConsumeAPIKeyRevealGrant(ctx, digest)
	require.NoError(t, err)
	require.Nil(t, again)

	// 过期窗口：5 分钟后再签发的 grant 已随 TTL 自然消失。
	require.NoError(t, store.SaveAPIKeyRevealGrant(ctx, digest, record, service.APIKeyRevealGrantTTL))
	server.FastForward(service.APIKeyRevealGrantTTL + time.Second)
	expired, err := store.ConsumeAPIKeyRevealGrant(ctx, digest)
	require.NoError(t, err)
	require.Nil(t, expired)
}

func TestAPIKeyPasskeyTicketIsOneShotWithFiveMinuteTTL(t *testing.T) {
	server, store := newAPIKeyVerificationCacheForTest(t)
	ctx := context.Background()
	digest := "passkey-ticket-digest"
	record := &service.APIKeyPasskeyVerificationRecord{
		UserID:    7,
		Factor:    service.APIKeyFactorPasskey,
		ExpiresAt: time.Now().Add(service.APIKeyPasskeyTicketTTL).Unix(),
	}
	require.NoError(t, store.SaveAPIKeyPasskeyTicket(ctx, digest, record, service.APIKeyPasskeyTicketTTL))
	require.Equal(t, int(service.APIKeyPasskeyTicketTTL/time.Second), int(server.TTL(apiKeyPasskeyTicketKey(digest))/time.Second))

	loaded, err := store.ConsumeAPIKeyPasskeyTicket(ctx, digest)
	require.NoError(t, err)
	require.NotNil(t, loaded)
	require.Equal(t, int64(7), loaded.UserID)
	require.Equal(t, service.APIKeyFactorPasskey, loaded.Factor)

	again, err := store.ConsumeAPIKeyPasskeyTicket(ctx, digest)
	require.NoError(t, err)
	require.Nil(t, again)

	require.NoError(t, store.SaveAPIKeyPasskeyTicket(ctx, digest, record, service.APIKeyPasskeyTicketTTL))
	server.FastForward(service.APIKeyPasskeyTicketTTL + time.Second)
	expired, err := store.ConsumeAPIKeyPasskeyTicket(ctx, digest)
	require.NoError(t, err)
	require.Nil(t, expired)
}
