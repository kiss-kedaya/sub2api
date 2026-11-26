package repository

import (
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestBuildRedisOptions(t *testing.T) {
	cfg := &config.Config{
		Redis: config.RedisConfig{
			Host:                "localhost",
			Port:                6379,
			Username:            "app-user",
			Password:            "secret",
			DB:                  2,
			DialTimeoutSeconds:  5,
			ReadTimeoutSeconds:  3,
			WriteTimeoutSeconds: 4,
			PoolSize:            100,
			MinIdleConns:        10,
		},
	}

	opts := buildRedisOptions(cfg)
	require.Equal(t, "localhost:6379", opts.Addr)
	require.Equal(t, "app-user", opts.Username)
	require.Equal(t, "secret", opts.Password)
	require.Equal(t, 2, opts.DB)
	require.Equal(t, 5*time.Second, opts.DialTimeout)
	require.Equal(t, 3*time.Second, opts.ReadTimeout)
	require.Equal(t, 4*time.Second, opts.WriteTimeout)
	require.Equal(t, 100, opts.PoolSize)
	require.Equal(t, 10, opts.MinIdleConns)
	require.Nil(t, opts.TLSConfig)

	// Test case with TLS enabled
	cfgTLS := &config.Config{
		Redis: config.RedisConfig{
			Host:      "localhost",
			EnableTLS: true,
		},
	}
	optsTLS := buildRedisOptions(cfgTLS)
	require.NotNil(t, optsTLS.TLSConfig)
	require.Equal(t, "localhost", optsTLS.TLSConfig.ServerName)
}

// 生产里连接数只涨不缩、池耗尽时请求长时间排队，修法全在这几个选项上：
// 每个字段都要真的传给 go-redis，且默认值/边界处理不能退化。
func TestBuildRedisOptionsPoolHygiene(t *testing.T) {
	base := config.RedisConfig{
		Host:                   "localhost",
		Port:                   6379,
		DialTimeoutSeconds:     5,
		ReadTimeoutSeconds:     3,
		WriteTimeoutSeconds:    3,
		PoolSize:               512,
		MinIdleConns:           8,
		MaxIdleConns:           64,
		PoolFIFO:               true,
		PoolTimeoutMS:          1000,
		ConnMaxIdleTimeSeconds: 300,
		ConnMaxLifetimeSeconds: 1800,
	}
	build := func(mutate func(*config.RedisConfig)) *redis.Options {
		r := base
		if mutate != nil {
			mutate(&r)
		}
		return buildRedisOptions(&config.Config{Redis: r})
	}

	t.Run("every pool setting reaches go-redis", func(t *testing.T) {
		opts := build(nil)
		require.True(t, opts.PoolFIFO, "FIFO 轮转才能让栈底的老连接被检查到")
		require.Equal(t, 512, opts.PoolSize)
		require.Equal(t, 8, opts.MinIdleConns)
		require.Equal(t, 64, opts.MaxIdleConns)
		require.Equal(t, time.Second, opts.PoolTimeout)
		require.Equal(t, 5*time.Minute, opts.ConnMaxIdleTime)
		require.Equal(t, 30*time.Minute, opts.ConnMaxLifetime)
		require.Equal(t, 3*time.Minute, opts.ConnMaxLifetimeJitter, "存活时间要带 ±10% 抖动，避免连接同一时刻集中重建")
	})

	t.Run("effective options after go-redis applies its defaults", func(t *testing.T) {
		// 不显式传 PoolTimeout 时 go-redis 会取 ReadTimeout+1s=4s；配置过的值必须原样生效。
		// MinIdleConns=0：否则创建客户端就会在后台去连 localhost:6379。
		client := redis.NewClient(build(func(r *config.RedisConfig) { r.MinIdleConns = 0 }))
		defer func() { _ = client.Close() }()
		effective := client.Options()
		require.Equal(t, time.Second, effective.PoolTimeout)
		require.Equal(t, 5*time.Minute, effective.ConnMaxIdleTime)
		require.Equal(t, 30*time.Minute, effective.ConnMaxLifetime)
		require.Equal(t, 3*time.Minute, effective.ConnMaxLifetimeJitter)
	})

	t.Run("lifo can still be selected", func(t *testing.T) {
		require.False(t, build(func(r *config.RedisConfig) { r.PoolFIFO = false }).PoolFIFO)
	})

	t.Run("zero values mean off or library default", func(t *testing.T) {
		// 命令行工具（admin_2fa_*）会拿生产配置的拷贝改小连接池，手工拼出的配置里新字段是 0。
		opts := build(func(r *config.RedisConfig) {
			r.MaxIdleConns = 0
			r.PoolTimeoutMS = 0
			r.ConnMaxIdleTimeSeconds = 0
			r.ConnMaxLifetimeSeconds = 0
		})
		require.Equal(t, 0, opts.MaxIdleConns, "0 表示不限制")
		require.Equal(t, time.Duration(0), opts.PoolTimeout, "0 表示用 go-redis 默认值")
		require.Equal(t, time.Duration(-1), opts.ConnMaxIdleTime, "go-redis 里 0 会被换成 30 分钟，不回收必须传负数")
		require.Equal(t, time.Duration(0), opts.ConnMaxLifetime)
		require.Equal(t, time.Duration(0), opts.ConnMaxLifetimeJitter)
	})

	t.Run("max idle below min idle is raised to min idle", func(t *testing.T) {
		// 生产环境变量是 REDIS_MIN_IDLE_CONNS=256：新的默认 64 不能让预热连接一归还就被关掉再补建。
		opts := build(func(r *config.RedisConfig) {
			r.PoolSize = 4096
			r.MinIdleConns = 256
			r.MaxIdleConns = 64
		})
		require.Equal(t, 256, opts.MaxIdleConns)
	})

	t.Run("max idle above pool size is clamped", func(t *testing.T) {
		opts := build(func(r *config.RedisConfig) {
			r.PoolSize = 32
			r.MinIdleConns = 0
			r.MaxIdleConns = 64
		})
		require.Equal(t, 32, opts.MaxIdleConns)
	})
}
