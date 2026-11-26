package repository

import (
	"crypto/tls"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"

	"github.com/redis/go-redis/v9"
)

// redisConnLifetimeJitterDivisor: ConnMaxLifetime 的抖动幅度 = 存活时间 / 10（即 ±10%），
// 让同一批建立的连接不在同一时刻集中到期、集中重建。
const redisConnLifetimeJitterDivisor = 10

// InitRedis 初始化 Redis 客户端
//
// 连接池参数（均可在 redis.* 配置里调整，默认值见 config.go）：
//  1. PoolSize: 最大并发连接数，同时是背压阀——到顶后新请求在 PoolTimeout 内取不到连接就快速失败；
//  2. MinIdleConns: 空闲连接的保底数量，池会一直补足它（没有流量也保持），所以保持小值；
//  3. MaxIdleConns: 空闲连接上限，归还时超限的连接直接关闭，突发期撑大的池子很快缩回；
//  4. PoolFIFO + ConnMaxIdleTime + ConnMaxLifetime: go-redis v9 没有后台回收线程，
//     只在取连接时惰性检查，必须 FIFO 轮转才能让栈底的老连接被检查到；
//  5. DialTimeout/ReadTimeout/WriteTimeout: 精确控制各阶段超时。
func InitRedis(cfg *config.Config) *redis.Client {
	client := redis.NewClient(buildRedisOptions(cfg))
	if cfg.Server.EnableServerTiming {
		client.AddHook(serverTimingRedisHook{})
	}
	return client
}

// buildRedisOptions 构建 Redis 连接选项
// 从配置文件读取连接池和超时参数，支持生产环境调优。
// 新增的池参数在配置里为 0 时一律取“关闭/库默认”，所以手工拼出来的旧配置（比如命令行工具里
// 缩小连接池的那几处）也能得到可用的选项。
func buildRedisOptions(cfg *config.Config) *redis.Options {
	opts := &redis.Options{
		Addr:         cfg.Redis.Address(),
		Username:     cfg.Redis.Username,
		Password:     cfg.Redis.Password,
		DB:           cfg.Redis.DB,
		DialTimeout:  time.Duration(cfg.Redis.DialTimeoutSeconds) * time.Second,  // 建连超时
		ReadTimeout:  time.Duration(cfg.Redis.ReadTimeoutSeconds) * time.Second,  // 读取超时
		WriteTimeout: time.Duration(cfg.Redis.WriteTimeoutSeconds) * time.Second, // 写入超时
		PoolSize:     cfg.Redis.PoolSize,                                         // 连接池大小
		MinIdleConns: cfg.Redis.MinIdleConns,                                     // 最小空闲连接
		MaxIdleConns: redisMaxIdleConns(cfg.Redis),                               // 空闲连接上限
		PoolFIFO:     cfg.Redis.PoolFIFO,                                         // 空闲连接轮转
		// 池耗尽时的等待上限；0 表示用库默认（ReadTimeout+1s）。
		PoolTimeout: time.Duration(cfg.Redis.PoolTimeoutMS) * time.Millisecond,
		// go-redis 里 0 会被悄悄换成 30 分钟，要“不按空闲时间回收”必须传负数。
		ConnMaxIdleTime: redisConnMaxIdleTime(cfg.Redis.ConnMaxIdleTimeSeconds),
	}

	if lifetime := time.Duration(cfg.Redis.ConnMaxLifetimeSeconds) * time.Second; lifetime > 0 {
		opts.ConnMaxLifetime = lifetime
		opts.ConnMaxLifetimeJitter = lifetime / redisConnLifetimeJitterDivisor
	}

	if cfg.Redis.EnableTLS {
		opts.TLSConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
			ServerName: cfg.Redis.Host,
		}
	}

	return opts
}

// redisMaxIdleConns 把配置里的空闲连接上限规整成 go-redis 能用的值：
//   - 0 表示不限制，原样返回；
//   - 小于 MinIdleConns 时抬到 MinIdleConns，否则预热出来的连接一归还就被关掉再补建，白白折腾；
//   - 大于 PoolSize 没有意义，收到 PoolSize。
//
// 不放进 config.Validate 是故意的：旧配置（比如生产里 MinIdleConns=256、PoolSize=4096）
// 加上新默认值不能因为交叉校验而启动失败。
func redisMaxIdleConns(r config.RedisConfig) int {
	maxIdle := r.MaxIdleConns
	if maxIdle <= 0 {
		return 0
	}
	if maxIdle < r.MinIdleConns {
		maxIdle = r.MinIdleConns
	}
	if r.PoolSize > 0 && maxIdle > r.PoolSize {
		maxIdle = r.PoolSize
	}
	return maxIdle
}

func redisConnMaxIdleTime(seconds int) time.Duration {
	if seconds <= 0 {
		return -1
	}
	return time.Duration(seconds) * time.Second
}
