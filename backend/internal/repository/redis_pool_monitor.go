package repository

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// redisPoolNearCapacityPercent: 池里的连接数超过 PoolSize 的这个百分比就升级成 WARN。
const redisPoolNearCapacityPercent = 80

// redisPoolSample 是 go-redis 连接池统计的一份拷贝。
// Total/Idle/Pending/PubSub 是瞬时值；其余是累计计数（uint32，会回绕），
// 求差值一律用 uint32 减法，回绕时结果依然正确。
type redisPoolSample struct {
	Total   uint32 // 池里现有连接数（含空闲和在用）
	Idle    uint32 // 其中空闲的
	Pending uint32 // 此刻正在排队等连接的请求数
	PubSub  uint32 // Subscribe 占用的独立连接数（不在池里，不受 PoolSize 约束）

	Hits     uint32 // 命中空闲连接
	Misses   uint32 // 没有空闲连接、新建了连接
	Timeouts uint32 // 等连接超时（redis: connection pool timeout），重试的每一次都算
	Stale    uint32 // 被池关闭并移出的连接（超限归还、过期、出错）
	Waits    uint32 // 需要排队等连接的次数
}

func sampleRedisPoolStats(s *redis.PoolStats) redisPoolSample {
	if s == nil {
		return redisPoolSample{}
	}
	return redisPoolSample{
		Total:    s.TotalConns,
		Idle:     s.IdleConns,
		Pending:  s.PendingRequests,
		PubSub:   s.PubSubStats.Active,
		Hits:     s.Hits,
		Misses:   s.Misses,
		Timeouts: s.Timeouts,
		Stale:    s.StaleConns,
		Waits:    s.WaitCount,
	}
}

// redisPoolReport 是一行待写的日志。
type redisPoolReport struct {
	Level   zapcore.Level
	Message string
	Fields  []zap.Field
}

// buildRedisPoolReport 决定这一轮要不要写日志、写成什么级别：
//   - 连接数超过 PoolSize 的 80%，或这一轮出现了池超时：WARN（池快满/已经有请求被拒是线上最该看到的信号），
//     只要条件成立每轮都写；
//   - 否则只在统计相比上一轮有变化时写 INFO（空闲的备份进程一行都不会写）。
func buildRedisPoolReport(prev, cur redisPoolSample, poolSize int) (redisPoolReport, bool) {
	nearCapacity := poolSize > 0 && uint64(cur.Total)*100 > uint64(poolSize)*redisPoolNearCapacityPercent
	timeoutsDelta := cur.Timeouts - prev.Timeouts
	if !nearCapacity && timeoutsDelta == 0 && cur == prev {
		return redisPoolReport{}, false
	}

	report := redisPoolReport{
		Level:   zapcore.InfoLevel,
		Message: "redis pool stats",
		Fields: []zap.Field{
			zap.Int("pool_size", poolSize),
			zap.Uint32("total_conns", cur.Total),
			zap.Uint32("idle_conns", cur.Idle),
			zap.Uint32("pending_requests", cur.Pending),
			zap.Uint32("pubsub_conns", cur.PubSub),
			// 下面几项是相比上一轮的增量。
			zap.Uint32("hits_delta", cur.Hits-prev.Hits),
			zap.Uint32("misses_delta", cur.Misses-prev.Misses),
			zap.Uint32("timeouts_delta", timeoutsDelta),
			zap.Uint32("stale_delta", cur.Stale-prev.Stale),
			zap.Uint32("waits_delta", cur.Waits-prev.Waits),
		},
	}
	if nearCapacity || timeoutsDelta > 0 {
		report.Level = zapcore.WarnLevel
		report.Message = "redis pool under pressure"
		report.Fields = append(report.Fields,
			zap.Bool("near_capacity", nearCapacity),
			zap.Bool("pool_timeouts", timeoutsDelta > 0),
		)
	}
	return report, true
}

func emitRedisPoolReport(r redisPoolReport) {
	if ce := logger.L().Check(r.Level, r.Message); ce != nil {
		ce.Write(r.Fields...)
	}
}

// observeRedisPool 打印最终生效的连接池参数（取自 go-redis 补完默认值之后的选项，不含密码、地址），
// 并按 redis.pool_stats_interval_seconds 启动连接池统计日志。
// 统计日志随客户端 Close 自动停止，所以不需要在 wire 的清理列表里再加一项。
func observeRedisPool(client *redis.Client, cfg *config.Config) *redisPoolMonitor {
	opts := client.Options()
	logger.L().Info("redis pool configured", redisPoolLogFields(opts)...)
	return startRedisPoolMonitor(client, opts.PoolSize, time.Duration(cfg.Redis.PoolStatsIntervalSeconds)*time.Second)
}

// redisPoolLogFields 是启动日志里的连接池参数。负数的“关闭”哨兵值统一写成 0。
func redisPoolLogFields(opts *redis.Options) []zap.Field {
	nonNegative := func(d time.Duration) time.Duration {
		if d < 0 {
			return 0
		}
		return d
	}
	return []zap.Field{
		zap.Int("pool_size", opts.PoolSize),
		zap.Int("min_idle_conns", opts.MinIdleConns),
		zap.Int("max_idle_conns", opts.MaxIdleConns),
		zap.Bool("pool_fifo", opts.PoolFIFO),
		zap.Duration("pool_timeout", opts.PoolTimeout),
		zap.Duration("conn_max_idle_time", nonNegative(opts.ConnMaxIdleTime)),
		zap.Duration("conn_max_lifetime", nonNegative(opts.ConnMaxLifetime)),
		zap.Duration("conn_max_lifetime_jitter", nonNegative(opts.ConnMaxLifetimeJitter)),
		zap.Duration("dial_timeout", opts.DialTimeout),
		zap.Duration("read_timeout", opts.ReadTimeout),
		zap.Duration("write_timeout", opts.WriteTimeout),
		zap.Int("max_retries", opts.MaxRetries),
		zap.Bool("tls", opts.TLSConfig != nil),
	}
}

// redisPoolSource 是监控器需要的那一小块客户端能力，方便测试替身。
type redisPoolSource interface {
	sample() redisPoolSample
	// closed 报告客户端是否已经 Close。
	closed() bool
}

type redisClientPoolSource struct{ client *redis.Client }

func (s redisClientPoolSource) sample() redisPoolSample {
	return sampleRedisPoolStats(s.client.PoolStats())
}

// closed 用一个已取消的 context 发 PING：go-redis 在取连接之前先检查“客户端是否已关闭”，
// 关闭后立刻返回 redis.ErrClosed；没关闭则卡在 context 已取消那一步，不占连接、不发命令、不动统计。
func (s redisClientPoolSource) closed() bool {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return errors.Is(s.client.Ping(ctx).Err(), redis.ErrClosed)
}

// redisPoolMonitor 周期性读取连接池统计并按 buildRedisPoolReport 的规则写日志。
// 它跟随客户端的生命周期：客户端被 Close 之后下一轮就自行退出，也可以显式 Stop。
type redisPoolMonitor struct {
	stopOnce sync.Once
	stopCh   chan struct{}
	doneCh   chan struct{}
}

// startRedisPoolMonitor 启动监控；interval<=0 或 client 为空表示关闭，返回 nil（nil 的 Stop/Done 都安全）。
func startRedisPoolMonitor(client *redis.Client, poolSize int, interval time.Duration) *redisPoolMonitor {
	if client == nil || interval <= 0 {
		return nil
	}
	return runRedisPoolMonitor(redisClientPoolSource{client: client}, poolSize, interval, emitRedisPoolReport)
}

func runRedisPoolMonitor(src redisPoolSource, poolSize int, interval time.Duration, emit func(redisPoolReport)) *redisPoolMonitor {
	m := &redisPoolMonitor{stopCh: make(chan struct{}), doneCh: make(chan struct{})}
	go func() {
		defer close(m.doneCh)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		var prev redisPoolSample
		for {
			select {
			case <-m.stopCh:
				return
			case <-ticker.C:
			}
			cur := src.sample()
			// 客户端 Close 之后池里一定是 0 条连接；只有看到 0 才去确认是不是真的关了，
			// 正常运行时不会多发任何命令。
			if cur.Total == 0 && src.closed() {
				return
			}
			if report, ok := buildRedisPoolReport(prev, cur, poolSize); ok {
				emit(report)
			}
			prev = cur
		}
	}()
	return m
}

// Stop 停止监控并等它退出；可重复调用。
func (m *redisPoolMonitor) Stop() {
	if m == nil {
		return
	}
	m.stopOnce.Do(func() { close(m.stopCh) })
	<-m.doneCh
}

// Done 在监控退出后关闭。
func (m *redisPoolMonitor) Done() <-chan struct{} {
	if m == nil {
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	return m.doneCh
}
