package repository

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// 这组测试用真实的 go-redis 客户端（选项由 buildRedisOptions 生成）对着 miniredis，
// 验证连接池“只涨不缩”的机理和修法，而不是只检查选项字段：
//   - go-redis v9 没有后台回收线程，空闲连接只在被取到时才惰性检查 ConnMaxIdleTime；
//   - 默认 LIFO 只会反复取栈顶，栈底的连接永远轮不到；FIFO 才会逐个检查；
//   - MaxIdleConns 在归还时就关掉超限的连接，不依赖后续流量；
//   - PoolTimeout 决定池耗尽时多久放弃。

func redisPoolTestConfig(t *testing.T, srv *miniredis.Miniredis) config.RedisConfig {
	t.Helper()
	port, err := strconv.Atoi(srv.Port())
	require.NoError(t, err)
	return config.RedisConfig{
		Host:                   srv.Host(),
		Port:                   port,
		DialTimeoutSeconds:     2,
		ReadTimeoutSeconds:     3,
		WriteTimeoutSeconds:    3,
		PoolSize:               64,
		PoolFIFO:               true,
		PoolTimeoutMS:          1000,
		ConnMaxIdleTimeSeconds: 300,
	}
}

func newRedisPoolTestClient(t *testing.T, cfg config.RedisConfig) *redis.Client {
	t.Helper()
	client := redis.NewClient(buildRedisOptions(&config.Config{Redis: cfg}))
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// holdConnections 让 n 个请求同时各占一条连接 hold 这么久（服务端 BLPOP 阻塞），
// 返回时它们都已归还到池里。用 Do 传字符串超时，因为 go-redis 的 BLPop 会把小于 1 秒的超时截成 1 秒。
func holdConnections(client *redis.Client, n int, hold time.Duration) {
	var wg sync.WaitGroup
	start := make(chan struct{})
	timeout := strconv.FormatFloat(hold.Seconds(), 'f', 3, 64)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			// 超时返回 redis.Nil，是预期结果。
			_ = client.Do(context.Background(), "BLPOP", "redis-pool-hold", timeout).Err()
		}()
	}
	close(start)
	wg.Wait()
}

func TestRedisPoolFIFORecyclesStaleIdleConnectionsLIFODoesNot(t *testing.T) {
	for _, tc := range []struct {
		name string
		fifo bool
	}{
		{name: "fifo", fifo: true},
		{name: "lifo", fifo: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv := miniredis.RunT(t)
			cfg := redisPoolTestConfig(t, srv)
			cfg.PoolFIFO = tc.fifo
			cfg.ConnMaxIdleTimeSeconds = 1
			client := newRedisPoolTestClient(t, cfg)
			ctx := context.Background()

			// 突发：12 个请求同时在途，池子被撑到 12 条连接，之后全部空闲。
			holdConnections(client, 12, 250*time.Millisecond)
			require.Equal(t, uint32(12), client.PoolStats().TotalConns)

			// 空闲超过 ConnMaxIdleTime 之后，没有流量时一条都不会少：没有后台回收线程。
			time.Sleep(1300 * time.Millisecond)
			require.Equal(t, uint32(12), client.PoolStats().TotalConns)
			require.Equal(t, 12, srv.CurrentConnectionCount())

			// 流量恢复（只要一点点）：FIFO 从最老的连接开始逐个检查，把过期的都关掉；
			// LIFO 只会检查栈顶，取到一条新连接之后一直复用它，栈底那些过期连接永远留在池里。
			for i := 0; i < 30; i++ {
				require.NoError(t, client.Ping(ctx).Err())
			}
			total := client.PoolStats().TotalConns
			if tc.fifo {
				require.LessOrEqual(t, total, uint32(5), "FIFO 应该把 12 条过期连接换成少数几条新连接")
				require.Eventually(t, func() bool { return srv.CurrentConnectionCount() <= 5 }, 2*time.Second, 10*time.Millisecond)
				return
			}
			require.GreaterOrEqual(t, total, uint32(9), "LIFO 下栈底的过期连接不会被回收")
			require.GreaterOrEqual(t, srv.CurrentConnectionCount(), 9)
		})
	}
}

func TestRedisPoolMaxIdleConnsShrinksPoolRightAfterBurst(t *testing.T) {
	burstThenTraffic := func(t *testing.T, maxIdle int) (*redis.Client, *miniredis.Miniredis) {
		srv := miniredis.RunT(t)
		cfg := redisPoolTestConfig(t, srv)
		cfg.MaxIdleConns = maxIdle
		client := newRedisPoolTestClient(t, cfg)

		holdConnections(client, 12, 150*time.Millisecond)
		// go-redis 判断“空闲是否已满”和把连接放回空闲列表不是一个原子操作，12 个请求同时归还时
		// 上限可能被多放过几条；之后每个请求取放一次就会再关掉一条超限的，所以这里跑一点点流量。
		for i := 0; i < 20; i++ {
			require.NoError(t, client.Ping(context.Background()).Err())
		}
		return client, srv
	}

	t.Run("capped", func(t *testing.T) {
		client, srv := burstThenTraffic(t, 4)

		// 不需要等空闲超时：超限的连接在归还时就被关掉了。
		stats := client.PoolStats()
		require.LessOrEqual(t, stats.TotalConns, uint32(4))
		require.Equal(t, stats.TotalConns, stats.IdleConns)
		require.Eventually(t, func() bool { return srv.CurrentConnectionCount() <= 4 }, 2*time.Second, 10*time.Millisecond)
	})

	t.Run("uncapped keeps the high-water mark", func(t *testing.T) {
		client, srv := burstThenTraffic(t, 0)

		// 这就是线上的“只涨不缩”：突发撑出来的连接全部留在池里，后续流量也不会关掉它们。
		require.Equal(t, uint32(12), client.PoolStats().TotalConns)
		require.Equal(t, 12, srv.CurrentConnectionCount())
	})
}

func TestRedisPoolExhaustionFailsFastWithConfiguredPoolTimeout(t *testing.T) {
	srv := miniredis.RunT(t)
	cfg := redisPoolTestConfig(t, srv)
	cfg.PoolSize = 2
	cfg.PoolTimeoutMS = 100 // 不配置时 go-redis 用 ReadTimeout+1s=4s
	client := newRedisPoolTestClient(t, cfg)

	// 两个长阻塞命令占满整个池。
	var blockers sync.WaitGroup
	for i := 0; i < 2; i++ {
		blockers.Add(1)
		go func() {
			defer blockers.Done()
			_ = client.Do(context.Background(), "BLPOP", "redis-pool-hold", "2").Err()
		}()
	}
	require.Eventually(t, func() bool {
		s := client.PoolStats()
		return s.TotalConns == 2 && s.IdleConns == 0
	}, 2*time.Second, 5*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	started := time.Now()
	err := client.Ping(ctx).Err()
	elapsed := time.Since(started)

	// 池耗尽要快速失败，并且失败原因是池超时，而不是排队排到调用方的 context 超时。
	require.ErrorIs(t, err, redis.ErrPoolTimeout)
	require.Less(t, elapsed, 2*time.Second)
	// go-redis 会把池超时当作可重试错误：每次尝试都要等满 PoolTimeout，所以最坏约 (1+MaxRetries)×PoolTimeout。
	require.GreaterOrEqual(t, elapsed, 100*time.Millisecond)
	require.GreaterOrEqual(t, client.PoolStats().Timeouts, uint32(1))
	t.Logf("pool exhausted: failed after %v, pool timeouts=%d", elapsed, client.PoolStats().Timeouts)

	// 关闭客户端会让还在阻塞的命令立刻返回，不用等满 2 秒。
	require.NoError(t, client.Close())
	blockers.Wait()
}

// 审计结论的证据：WithTimeout 返回的副本与父客户端共用同一个连接池（不会多开一套连接），
// 关闭父客户端后副本也不可用，连接全部释放。
func TestRedisWithTimeoutClonesShareOnePoolWithTheirParent(t *testing.T) {
	srv := miniredis.RunT(t)
	client := newRedisPoolTestClient(t, redisPoolTestConfig(t, srv))
	clone := client.WithTimeout(500 * time.Millisecond)
	ctx := context.Background()

	holdConnections(clone, 6, 150*time.Millisecond)
	require.Equal(t, uint32(6), client.PoolStats().TotalConns, "副本建的连接在父客户端的池里")
	require.Equal(t, client.PoolStats().TotalConns, clone.PoolStats().TotalConns)
	require.Equal(t, 6, srv.CurrentConnectionCount())

	require.NoError(t, client.Ping(ctx).Err())
	require.NoError(t, clone.Ping(ctx).Err())
	require.Equal(t, uint32(6), client.PoolStats().TotalConns, "父客户端和副本复用同一批空闲连接")

	require.NoError(t, client.Close())
	require.ErrorIs(t, clone.Ping(ctx).Err(), redis.ErrClosed)
	require.Eventually(t, func() bool { return srv.CurrentConnectionCount() == 0 }, 2*time.Second, 10*time.Millisecond)
}

// 审计结论的证据：Subscribe 占用的是池外的独立连接（不受 PoolSize 约束，单独计入 PubSubStats），
// PubSub.Close 之后连接被释放。
func TestRedisSubscribeUsesDedicatedConnectionReleasedOnClose(t *testing.T) {
	srv := miniredis.RunT(t)
	client := newRedisPoolTestClient(t, redisPoolTestConfig(t, srv))
	ctx := context.Background()

	require.NoError(t, client.Ping(ctx).Err())
	sub := client.Subscribe(ctx, "redis-pool-audit")
	_, err := sub.Receive(ctx)
	require.NoError(t, err)

	stats := client.PoolStats()
	require.Equal(t, uint32(1), stats.PubSubStats.Active)
	require.Equal(t, uint32(1), stats.TotalConns, "订阅连接不在池里")
	require.Equal(t, 2, srv.CurrentConnectionCount())
	require.Equal(t, uint32(1), sampleRedisPoolStats(stats).PubSub)

	require.NoError(t, sub.Close())
	require.Equal(t, uint32(0), client.PoolStats().PubSubStats.Active)
	require.Eventually(t, func() bool { return srv.CurrentConnectionCount() == 1 }, 2*time.Second, 10*time.Millisecond)
}
