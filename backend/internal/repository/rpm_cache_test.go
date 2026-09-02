package repository

import (
	"context"
	"sync"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestRPMCache_CurrentMinuteSuffixIsSharedAcrossConcurrentCalls(t *testing.T) {
	mini := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	cache := &RPMCacheImpl{rdb: client}
	const callers = 32
	suffixes := make([]string, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			suffix, err := cache.currentMinuteSuffix(context.Background())
			require.NoError(t, err)
			suffixes[index] = suffix
		}(i)
	}
	wg.Wait()

	for _, suffix := range suffixes {
		require.NotEmpty(t, suffix)
		require.Equal(t, suffixes[0], suffix)
	}
}

func TestRPMCache_IncrementUsesCachedMinuteKey(t *testing.T) {
	mini := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	cache := &RPMCacheImpl{rdb: client}
	first, err := cache.IncrementRPM(context.Background(), 42)
	require.NoError(t, err)
	second, err := cache.IncrementRPM(context.Background(), 42)
	require.NoError(t, err)
	require.Equal(t, 1, first)
	require.Equal(t, 2, second)
}
