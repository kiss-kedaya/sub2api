package repository

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestConcurrencyCacheBalanceSlotsUseGlobalUserBudget(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache := NewConcurrencyCache(client, 15, 900).(*concurrencyCache)
	ctx := context.Background()

	// $0.25 allows two $0.10 OpenAI/Grok reservations, regardless of group.
	require.NoError(t, client.Set(ctx, billingBalanceKey(42), "0.25", 0).Err())
	first, err := cache.AcquireUserBalanceSlot(ctx, 42, 7, "openai", 0.1, 0.25, "req-1")
	require.NoError(t, err)
	require.True(t, first)
	second, err := cache.AcquireUserBalanceSlot(ctx, 42, 8, "grok", 0.1, 0.25, "req-2")
	require.NoError(t, err)
	require.True(t, second)
	third, err := cache.AcquireUserBalanceSlot(ctx, 42, 9, "openai", 0.1, 0.25, "req-3")
	require.NoError(t, err)
	require.False(t, third)

	require.NoError(t, cache.ReleaseUserBalanceSlot(ctx, 42, 7, "openai", "req-1"))
	third, err = cache.AcquireUserBalanceSlot(ctx, 42, 9, "openai", 0.1, 0.25, "req-3")
	require.NoError(t, err)
	require.True(t, third)

	// The group mirror is namespaced, but it cannot multiply the user's budget.
	groupCount, err := client.ZCard(ctx, balanceGroupSlotKey(42, 8, "grok")).Result()
	require.NoError(t, err)
	require.Equal(t, int64(1), groupCount)
}

func TestConcurrencyCacheBalanceSlotFillsMissingBalanceCache(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache := NewConcurrencyCache(client, 15, 900).(*concurrencyCache)
	ctx := context.Background()

	acquired, err := cache.AcquireUserBalanceSlot(ctx, 99, 12, "anthropic", 1, 1.25, "req-missing-cache")
	require.NoError(t, err)
	require.True(t, acquired)
	balance, err := client.Get(ctx, billingBalanceKey(99)).Float64()
	require.NoError(t, err)
	require.Equal(t, 1.25, balance)
}

func TestConcurrencyCacheBalanceSlotsUseWeightedPlatformCost(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache := NewConcurrencyCache(client, 15, 900).(*concurrencyCache)
	ctx := context.Background()
	require.NoError(t, client.Set(ctx, billingBalanceKey(100), "1.05", 0).Err())

	anthropic, err := cache.AcquireUserBalanceSlot(ctx, 100, 1, "anthropic", 1, 1.05, "anthropic-1")
	require.NoError(t, err)
	require.True(t, anthropic)
	openAI, err := cache.AcquireUserBalanceSlot(ctx, 100, 2, "openai", 0.1, 1.05, "openai-1")
	require.NoError(t, err)
	require.False(t, openAI)

	require.NoError(t, cache.ReleaseUserBalanceSlot(ctx, 100, 1, "anthropic", "anthropic-1"))
	openAI, err = cache.AcquireUserBalanceSlot(ctx, 100, 2, "openai", 0.1, 1.05, "openai-1")
	require.NoError(t, err)
	require.True(t, openAI)
}
