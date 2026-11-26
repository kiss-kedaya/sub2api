package repository

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"
)

func redisPoolReportFields(r redisPoolReport) map[string]any {
	enc := zapcore.NewMapObjectEncoder()
	for _, f := range r.Fields {
		f.AddTo(enc)
	}
	return enc.Fields
}

func TestBuildRedisPoolReportDecidesWhetherAndHowLoudToLog(t *testing.T) {
	steady := redisPoolSample{Total: 40, Idle: 30, Hits: 1000, Misses: 40, Stale: 5}

	tests := []struct {
		name       string
		prev, cur  redisPoolSample
		poolSize   int
		wantLog    bool
		wantLevel  zapcore.Level
		wantReason map[string]bool // near_capacity / pool_timeouts，仅 WARN 时检查
	}{
		{name: "idle process logs nothing", prev: redisPoolSample{}, cur: redisPoolSample{}, poolSize: 512},
		{name: "unchanged numbers log nothing", prev: steady, cur: steady, poolSize: 512},
		{name: "first non-empty sample is logged as baseline", prev: redisPoolSample{}, cur: steady, poolSize: 512, wantLog: true, wantLevel: zapcore.InfoLevel},
		{
			name: "changed numbers log at info", prev: steady,
			cur:      redisPoolSample{Total: 41, Idle: 30, Hits: 1500, Misses: 41, Stale: 5},
			poolSize: 512, wantLog: true, wantLevel: zapcore.InfoLevel,
		},
		{
			name: "exactly 80 percent is not near capacity", prev: redisPoolSample{Total: 400, Idle: 400},
			cur:      redisPoolSample{Total: 400, Idle: 400},
			poolSize: 500,
		},
		{
			name: "over 80 percent warns even if nothing changed", prev: redisPoolSample{Total: 401, Idle: 401},
			cur:      redisPoolSample{Total: 401, Idle: 401},
			poolSize: 500, wantLog: true, wantLevel: zapcore.WarnLevel,
			wantReason: map[string]bool{"near_capacity": true, "pool_timeouts": false},
		},
		{
			name: "pool timeouts warn even when the pool is small", prev: redisPoolSample{Total: 4, Idle: 4, Timeouts: 7},
			cur:      redisPoolSample{Total: 4, Idle: 4, Timeouts: 9},
			poolSize: 512, wantLog: true, wantLevel: zapcore.WarnLevel,
			wantReason: map[string]bool{"near_capacity": false, "pool_timeouts": true},
		},
		{
			name: "counter wraparound still counts as growth", prev: redisPoolSample{Total: 4, Idle: 4, Timeouts: math.MaxUint32},
			cur:      redisPoolSample{Total: 4, Idle: 4, Timeouts: 1},
			poolSize: 512, wantLog: true, wantLevel: zapcore.WarnLevel,
			wantReason: map[string]bool{"near_capacity": false, "pool_timeouts": true},
		},
		{name: "unknown pool size never counts as near capacity", prev: steady, cur: steady, poolSize: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			report, ok := buildRedisPoolReport(tc.prev, tc.cur, tc.poolSize)
			require.Equal(t, tc.wantLog, ok)
			if !ok {
				return
			}
			require.Equal(t, tc.wantLevel, report.Level)
			fields := redisPoolReportFields(report)
			if tc.wantLevel == zapcore.WarnLevel {
				require.Equal(t, "redis pool under pressure", report.Message)
				for k, want := range tc.wantReason {
					require.Equal(t, want, fields[k], k)
				}
			} else {
				require.Equal(t, "redis pool stats", report.Message)
			}
		})
	}
}

func TestBuildRedisPoolReportFields(t *testing.T) {
	prev := redisPoolSample{Total: 10, Idle: 9, Hits: 100, Misses: 4, Timeouts: 1, Stale: 2, Waits: 3}
	cur := redisPoolSample{Total: 12, Idle: 5, Pending: 2, PubSub: 7, Hits: 180, Misses: 9, Timeouts: 1, Stale: 6, Waits: 8}
	report, ok := buildRedisPoolReport(prev, cur, 512)
	require.True(t, ok)
	require.Equal(t, map[string]any{
		"pool_size":        int64(512),
		"total_conns":      uint32(12),
		"idle_conns":       uint32(5),
		"pending_requests": uint32(2),
		"pubsub_conns":     uint32(7),
		"hits_delta":       uint32(80),
		"misses_delta":     uint32(5),
		"timeouts_delta":   uint32(0),
		"stale_delta":      uint32(4),
		"waits_delta":      uint32(5),
	}, redisPoolReportFields(report))
}

type fakeRedisPoolSource struct {
	mu       sync.Mutex
	cur      redisPoolSample
	isClosed bool
}

func (f *fakeRedisPoolSource) set(s redisPoolSample) {
	f.mu.Lock()
	f.cur = s
	f.mu.Unlock()
}

func (f *fakeRedisPoolSource) sample() redisPoolSample {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cur
}

func (f *fakeRedisPoolSource) closed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.isClosed
}

type redisReportRecorder struct {
	mu      sync.Mutex
	reports []redisPoolReport
}

func (r *redisReportRecorder) emit(rep redisPoolReport) {
	r.mu.Lock()
	r.reports = append(r.reports, rep)
	r.mu.Unlock()
}

func (r *redisReportRecorder) snapshot() []redisPoolReport {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]redisPoolReport(nil), r.reports...)
}

func (r *redisReportRecorder) count() int { return len(r.snapshot()) }

func TestRedisPoolMonitorLogsOnlyWhenSomethingChangedAndStops(t *testing.T) {
	src := &fakeRedisPoolSource{}
	src.set(redisPoolSample{Total: 10, Idle: 10, Hits: 100})
	rec := &redisReportRecorder{}
	m := runRedisPoolMonitor(src, 512, 5*time.Millisecond, rec.emit)
	t.Cleanup(m.Stop)

	// 第一轮写一条基线；之后数字不变的几十个周期里一行都不写。
	require.Eventually(t, func() bool { return rec.count() == 1 }, 2*time.Second, time.Millisecond)
	time.Sleep(80 * time.Millisecond)
	require.Equal(t, 1, rec.count(), "数字没变不应该再写日志")

	// 数字变了：写一条 INFO。
	src.set(redisPoolSample{Total: 10, Idle: 9, Hits: 150})
	require.Eventually(t, func() bool { return rec.count() == 2 }, 2*time.Second, time.Millisecond)
	require.Equal(t, zapcore.InfoLevel, rec.snapshot()[1].Level)

	// 出现池超时：写一条 WARN。
	src.set(redisPoolSample{Total: 10, Idle: 9, Hits: 150, Timeouts: 3})
	require.Eventually(t, func() bool { return rec.count() == 3 }, 2*time.Second, time.Millisecond)
	require.Equal(t, zapcore.WarnLevel, rec.snapshot()[2].Level)

	// 连接数超过 80%：数字不变也每轮都写 WARN。
	src.set(redisPoolSample{Total: 450, Idle: 0, Pending: 30, Hits: 150, Timeouts: 3})
	require.Eventually(t, func() bool { return rec.count() >= 3+4 }, 2*time.Second, time.Millisecond)
	for _, rep := range rec.snapshot()[3:] {
		require.Equal(t, zapcore.WarnLevel, rep.Level)
	}

	// Stop 之后不再有任何输出，并且 goroutine 已退出。
	m.Stop()
	select {
	case <-m.Done():
	default:
		t.Fatal("Stop 返回时监控 goroutine 应该已经退出")
	}
	before := rec.count()
	time.Sleep(40 * time.Millisecond)
	require.Equal(t, before, rec.count())
	m.Stop() // 可重复调用
}

func TestRedisPoolMonitorStopsOnItsOwnWhenTheClientIsClosed(t *testing.T) {
	srv := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	rec := &redisReportRecorder{}
	m := runRedisPoolMonitor(redisClientPoolSource{client: client}, 512, 5*time.Millisecond, rec.emit)
	t.Cleanup(m.Stop)

	select {
	case <-m.Done():
		t.Fatal("客户端还开着，监控不该退出")
	case <-time.After(60 * time.Millisecond):
	}

	require.NoError(t, client.Close())
	select {
	case <-m.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("客户端 Close 之后监控应该自行退出（否则每个被关掉的客户端都会留下一个永不退出的 goroutine）")
	}
}

// 空闲、开着、池里一条连接都没有的客户端（比如备份色进程）：监控要一直活着，
// 而且判断“是不是已经关了”不能真的往 Redis 发命令或建连接。
func TestRedisPoolMonitorProbeNeverTouchesRedisWhileTheClientIsOpen(t *testing.T) {
	srv := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	rec := &redisReportRecorder{}
	m := runRedisPoolMonitor(redisClientPoolSource{client: client}, 512, 2*time.Millisecond, rec.emit)
	t.Cleanup(m.Stop)

	time.Sleep(80 * time.Millisecond) // 至少几十个周期

	select {
	case <-m.Done():
		t.Fatal("开着的空闲客户端不该让监控退出")
	default:
	}
	require.Equal(t, 0, srv.CommandCount(), "监控不能向 Redis 发命令")
	require.Equal(t, 0, srv.TotalConnectionCount(), "监控不能建连接")
	require.Equal(t, 0, rec.count(), "空闲进程不写日志")

	// 探测期间 PendingRequests 会瞬间 +1（取连接前先登记），所以必须等监控停下之后再看它有没有归零。
	m.Stop()
	require.Equal(t, uint32(0), client.PoolStats().PendingRequests, "探测不能把统计留脏")
}

// ProvideRedis 用到的入口：按配置的间隔启动监控，间隔为 0 表示关闭，监控随客户端 Close 退出。
func TestObserveRedisPoolStartsTheMonitorOnlyWhenConfigured(t *testing.T) {
	srv := miniredis.RunT(t)
	cfg := &config.Config{Redis: config.RedisConfig{
		Host: srv.Host(), Port: mustAtoi(t, srv.Port()),
		DialTimeoutSeconds: 2, ReadTimeoutSeconds: 3, WriteTimeoutSeconds: 3,
		PoolSize: 8, PoolTimeoutMS: 1000, PoolStatsIntervalSeconds: 0,
	}}
	client := redis.NewClient(buildRedisOptions(cfg))
	t.Cleanup(func() { _ = client.Close() })

	require.Nil(t, observeRedisPool(client, cfg), "间隔为 0 表示关闭")

	cfg.Redis.PoolStatsIntervalSeconds = 60
	m := observeRedisPool(client, cfg)
	require.NotNil(t, m)
	t.Cleanup(m.Stop)
	select {
	case <-m.Done():
		t.Fatal("监控应该在运行")
	default:
	}
}

func TestProvideRedisAppliesPoolSettingsAndKeepsClientUsable(t *testing.T) {
	srv := miniredis.RunT(t)
	cfg := &config.Config{Redis: config.RedisConfig{
		Host: srv.Host(), Port: mustAtoi(t, srv.Port()),
		DialTimeoutSeconds: 2, ReadTimeoutSeconds: 3, WriteTimeoutSeconds: 3,
		PoolSize: 16, MinIdleConns: 0, MaxIdleConns: 4, PoolFIFO: true, PoolTimeoutMS: 250,
		ConnMaxIdleTimeSeconds: 300, ConnMaxLifetimeSeconds: 1800, PoolStatsIntervalSeconds: 60,
	}}
	client := ProvideRedis(cfg)
	t.Cleanup(func() { _ = client.Close() })

	require.NoError(t, client.Ping(context.Background()).Err())
	opts := client.Options()
	require.True(t, opts.PoolFIFO)
	require.Equal(t, 16, opts.PoolSize)
	require.Equal(t, 4, opts.MaxIdleConns)
	require.Equal(t, 250*time.Millisecond, opts.PoolTimeout)
	require.Equal(t, 30*time.Minute, opts.ConnMaxLifetime)
}

func mustAtoi(t *testing.T, s string) int {
	t.Helper()
	n, err := strconv.Atoi(s)
	require.NoError(t, err)
	return n
}

func TestStartRedisPoolMonitorIsOffWhenIntervalIsZero(t *testing.T) {
	srv := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	m := startRedisPoolMonitor(client, 512, 0)
	require.Nil(t, m)
	m.Stop() // nil 安全
	select {
	case <-m.Done():
	default:
		t.Fatal("nil 监控的 Done 应该是已关闭的")
	}
	require.Nil(t, startRedisPoolMonitor(nil, 512, time.Minute))
}

func TestSampleRedisPoolStatsCopiesEveryCounter(t *testing.T) {
	require.Equal(t, redisPoolSample{}, sampleRedisPoolStats(nil))

	stats := &redis.PoolStats{
		TotalConns: 1, IdleConns: 2, PendingRequests: 3, Hits: 4, Misses: 5, Timeouts: 6, StaleConns: 7, WaitCount: 8,
	}
	stats.PubSubStats.Active = 9
	require.Equal(t, redisPoolSample{Total: 1, Idle: 2, Pending: 3, PubSub: 9, Hits: 4, Misses: 5, Timeouts: 6, Stale: 7, Waits: 8}, sampleRedisPoolStats(stats))
}

func TestRedisPoolStartupLogFieldsShowEffectiveSettingsWithoutSecrets(t *testing.T) {
	cfg := &config.Config{Redis: config.RedisConfig{
		Host: "redis.internal.example", Port: 6379, Username: "svc-user", Password: "s3cr3t-value",
		DialTimeoutSeconds: 5, ReadTimeoutSeconds: 3, WriteTimeoutSeconds: 3,
		PoolSize: 512, MinIdleConns: 8, MaxIdleConns: 64, PoolFIFO: true, PoolTimeoutMS: 1000,
		ConnMaxIdleTimeSeconds: 0, // 关闭按空闲时间回收：内部哨兵值 -1 要写成 0
		ConnMaxLifetimeSeconds: 1800,
	}}
	opts := buildRedisOptions(cfg)
	opts.MinIdleConns = 0 // 测试里不要预热连接
	client := redis.NewClient(opts)
	t.Cleanup(func() { _ = client.Close() })

	fields := redisPoolLogFields(client.Options())
	enc := zapcore.NewMapObjectEncoder()
	for _, f := range fields {
		f.AddTo(enc)
	}
	got := enc.Fields
	require.Equal(t, int64(512), got["pool_size"])
	require.Equal(t, int64(64), got["max_idle_conns"])
	require.Equal(t, true, got["pool_fifo"])
	require.Equal(t, time.Second, got["pool_timeout"])
	require.Equal(t, time.Duration(0), got["conn_max_idle_time"])
	require.Equal(t, 30*time.Minute, got["conn_max_lifetime"])
	require.Equal(t, 3*time.Minute, got["conn_max_lifetime_jitter"])
	require.Equal(t, int64(3), got["max_retries"], "go-redis 默认对可重试错误（含池超时）重试 3 次")

	for key, val := range got {
		rendered := strings.ToLower(fmt.Sprintf("%s=%v", key, val))
		require.NotContains(t, rendered, "s3cr3t", key)
		require.NotContains(t, rendered, "svc-user", key)
		require.NotContains(t, rendered, "redis.internal", key)
	}
}
