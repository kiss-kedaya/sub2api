package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

// 用户/分组级 RPM 计数器 Redis 实现。
//
// 设计说明：
//   - key 形式：rpm:ug:{uid}:{gid}:{minute}、rpm:u:{uid}:{minute}
//   - 时间来源：Lua 内置 redis.call('TIME')（Redis 服务端时间），避免多实例时钟漂移，单 RTT 完成。
//   - 原子操作：包级 redis.NewScript（EVALSHA）单 RTT 原子执行 TIME + INCR + EXPIRE。
//   - TTL：120s，覆盖当前分钟窗口 + 少量冗余。
//   - 返回值语义：超限判断由调用方（billing_cache_service.checkRPM）与 RPMLimit 比较完成。
const (
	userGroupRPMKeyPrefix = "rpm:ug:"
	userRPMKeyPrefix      = "rpm:u:"

	userRPMKeyTTL = 120 * time.Second
)

var (
	// userRPMIncrScript 在单次 RTT 内获取 Redis 服务端 TIME，拼接当前分钟窗口 key 并原子 INCR+EXPIRE。
	// KEYS[1] = key 前缀（含尾冒号，如 "rpm:ug:{uid}:{gid}:"，置于 KEYS 以便测试命名空间 hook 透明加前缀）
	// ARGV[1] = TTL（秒）
	userRPMIncrScript = redis.NewScript(`
		-- Redis 3.2-4.x compat: opt into effects replication so redis.call('TIME')
		-- replicates correctly. No-op on Redis 5.0+ (effects replication is default).
		redis.replicate_commands()
		local now = tonumber(redis.call('TIME')[1])
		local minute = math.floor(now / 60)
		local key = KEYS[1] .. string.format('%d', minute)
		local count = redis.call('INCR', key)
		redis.call('EXPIRE', key, tonumber(ARGV[1]))
		return count
	`)

	// userRPMGetScript 在单次 RTT 内获取 Redis 服务端 TIME 并读取当前分钟窗口计数（只读）。
	// KEYS[1] = key 前缀（含尾冒号）
	userRPMGetScript = redis.NewScript(`
		local now = tonumber(redis.call('TIME')[1])
		local minute = math.floor(now / 60)
		local key = KEYS[1] .. string.format('%d', minute)
		return redis.call('GET', key)
	`)
)

type userRPMCacheImpl struct {
	rdb *redis.Client
}

// NewUserRPMCache 创建用户/分组级 RPM 计数器。
func NewUserRPMCache(rdb *redis.Client) service.UserRPMCache {
	return &userRPMCacheImpl{rdb: rdb}
}

// atomicIncr 单 RTT Lua 原子 TIME + INCR + EXPIRE。
func (c *userRPMCacheImpl) atomicIncr(ctx context.Context, keyPrefix string) (int, error) {
	val, err := userRPMIncrScript.Run(ctx, c.rdb, []string{keyPrefix}, int(userRPMKeyTTL.Seconds())).Int()
	if err != nil {
		return 0, fmt.Errorf("user rpm increment: %w", err)
	}
	return val, nil
}

// IncrementUserGroupRPM 递增 (user, group) 分钟计数。
func (c *userRPMCacheImpl) IncrementUserGroupRPM(ctx context.Context, userID, groupID int64) (int, error) {
	prefix := fmt.Sprintf("%s%d:%d:", userGroupRPMKeyPrefix, userID, groupID)
	return c.atomicIncr(ctx, prefix)
}

// IncrementUserRPM 递增用户分钟计数。
func (c *userRPMCacheImpl) IncrementUserRPM(ctx context.Context, userID int64) (int, error) {
	prefix := fmt.Sprintf("%s%d:", userRPMKeyPrefix, userID)
	return c.atomicIncr(ctx, prefix)
}

// GetUserGroupRPM 获取 (user, group) 当前分钟已用 RPM（只读）。
func (c *userRPMCacheImpl) GetUserGroupRPM(ctx context.Context, userID, groupID int64) (int, error) {
	prefix := fmt.Sprintf("%s%d:%d:", userGroupRPMKeyPrefix, userID, groupID)
	val, err := userRPMGetScript.Run(ctx, c.rdb, []string{prefix}).Int()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("user group rpm get: %w", err)
	}
	return val, nil
}

// GetUserRPM 获取用户当前分钟已用 RPM（只读）。
func (c *userRPMCacheImpl) GetUserRPM(ctx context.Context, userID int64) (int, error) {
	prefix := fmt.Sprintf("%s%d:", userRPMKeyPrefix, userID)
	val, err := userRPMGetScript.Run(ctx, c.rdb, []string{prefix}).Int()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("user rpm get: %w", err)
	}
	return val, nil
}
