package repository

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestSessionLimitCacheSetWindowCostBatch(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	cache := &sessionLimitCache{rdb: client, defaultIdleTimeout: time.Minute}
	costs := map[int64]float64{
		11: 1.25,
		22: 0,
		33: 98.75,
	}
	require.NoError(t, cache.SetWindowCostBatch(context.Background(), costs))

	got, err := cache.GetWindowCostBatch(context.Background(), []int64{11, 22, 33, 44})
	require.NoError(t, err)
	require.Equal(t, costs, got)
	for accountID := range costs {
		require.Equal(t, windowCostCacheTTL, server.TTL(windowCostKey(accountID)))
	}
}

func TestSessionLimitCacheSetWindowCostBatchEmpty(t *testing.T) {
	cache := &sessionLimitCache{}
	require.NoError(t, cache.SetWindowCostBatch(context.Background(), nil))
}
