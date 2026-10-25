package repository

import (
	"context"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestUserConcurrencyLimitsRemainAtomic(t *testing.T) {
	const requests = 16
	for _, tt := range []struct {
		name     string
		limit    int
		expected int
	}{
		{name: "zero allows every request", limit: 0, expected: requests},
		{name: "positive limit remains enforced", limit: 2, expected: 2},
		{name: "negative legacy limit remains bounded", limit: -1, expected: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			redisServer := miniredis.RunT(t)
			client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
			t.Cleanup(func() { _ = client.Close() })
			cache := NewConcurrencyCache(client, 15, 900)
			svc := service.NewConcurrencyService(cache)
			ctx := context.Background()
			results := make([]*service.AcquireResult, requests)
			errs := make([]error, requests)
			start := make(chan struct{})
			var wg sync.WaitGroup
			for i := range requests {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					<-start
					results[i], errs[i] = svc.AcquireUserSlot(ctx, 20, tt.limit)
				}(i)
			}
			close(start)
			wg.Wait()

			acquired := 0
			for i, result := range results {
				require.NoError(t, errs[i])
				require.NotNil(t, result)
				if result.Acquired {
					acquired++
					require.NotNil(t, result.ReleaseFunc)
				}
			}
			require.Equal(t, tt.expected, acquired)
			count, err := cache.GetUserConcurrency(ctx, 20)
			require.NoError(t, err)
			require.Equal(t, acquired, count, "unlimited requests must still appear in concurrency counts")

			for _, result := range results {
				if result.Acquired {
					result.ReleaseFunc()
				}
			}
			count, err = cache.GetUserConcurrency(ctx, 20)
			require.NoError(t, err)
			require.Zero(t, count)
		})
	}
}

func TestUserConcurrencyZeroToPositiveCountsExistingRequests(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache := NewConcurrencyCache(client, 15, 900)
	svc := service.NewConcurrencyService(cache)
	ctx := context.Background()
	var releases []func()
	for range 3 {
		result, err := svc.AcquireUserSlot(ctx, 20, 0)
		require.NoError(t, err)
		require.True(t, result.Acquired)
		releases = append(releases, result.ReleaseFunc)
	}

	blocked, err := svc.AcquireUserSlot(ctx, 20, 2)
	require.NoError(t, err)
	require.False(t, blocked.Acquired)
	releases[0]()
	blocked, err = svc.AcquireUserSlot(ctx, 20, 2)
	require.NoError(t, err)
	require.False(t, blocked.Acquired, "existing unlimited requests still fill the new limit")

	releases[1]()
	allowed, err := svc.AcquireUserSlot(ctx, 20, 2)
	require.NoError(t, err)
	require.True(t, allowed.Acquired)
	count, err := cache.GetUserConcurrency(ctx, 20)
	require.NoError(t, err)
	require.Equal(t, 2, count)

	releases[2]()
	allowed.ReleaseFunc()
	count, err = cache.GetUserConcurrency(ctx, 20)
	require.NoError(t, err)
	require.Zero(t, count)
}

func TestUserConcurrencyZeroCombinesRegularAndLiveSlots(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache := NewConcurrencyCache(client, 15, 900)
	live := cache.(service.LiveConcurrencyCache)
	ctx := context.Background()
	for _, requestID := range []string{"regular-first", "regular-second"} {
		acquired, err := cache.AcquireUserSlot(ctx, 20, 0, requestID)
		require.NoError(t, err)
		require.True(t, acquired)
	}
	acquired, err := live.AcquireLiveLease(ctx, 10, 1, 20, 0, 30, "live-first", false)
	require.NoError(t, err)
	require.True(t, acquired)
	acquired, err = live.AcquireLiveLease(ctx, 10, 1, 20, 0, 30, "account-blocked", false)
	require.NoError(t, err)
	require.False(t, acquired, "unlimited user concurrency must preserve upstream account limits")
	acquired, err = live.AcquireLiveLease(ctx, 11, 1, 20, 0, 30, "live-second", false)
	require.NoError(t, err)
	require.True(t, acquired)
	count, err := cache.GetUserConcurrency(ctx, 20)
	require.NoError(t, err)
	require.Equal(t, 4, count)
	apiCounts, err := cache.(service.APIKeyConcurrencyCache).GetAPIKeyConcurrencyBatch(ctx, []int64{30})
	require.NoError(t, err)
	require.Equal(t, 2, apiCounts[30])

	acquired, err = cache.AcquireUserSlot(ctx, 20, 4, "user-blocked")
	require.NoError(t, err)
	require.False(t, acquired)
	acquired, err = cache.AcquireUserSlot(ctx, 20, 0, "user-unlimited")
	require.NoError(t, err)
	require.True(t, acquired)
	acquired, err = cache.AcquireAccountSlot(ctx, 12, 0, "account-zero")
	require.NoError(t, err)
	require.False(t, acquired, "account cache zero-limit behavior must remain unchanged")
}
