package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const (
	redeemRateLimitKeyPrefix = "redeem:ratelimit:v2:"
	redeemLockKeyPrefix      = "redeem:lock:"
	redeemRateLimitWindow    = 10 * time.Minute
)

var incrementRedeemAttemptScript = redis.NewScript(`
local current = redis.call('INCR', KEYS[1])
local ttl = redis.call('PTTL', KEYS[1])

if current == 1 or ttl == -1 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
end

return current
`)

// redeemRateLimitKey generates the Redis key for redeem attempt rate limiting.
func redeemRateLimitKey(userID int64) string {
	return fmt.Sprintf("%s%d", redeemRateLimitKeyPrefix, userID)
}

// redeemLockKey generates the Redis key for redeem code locking.
//
// 用 sha256(明文) 而不是明文本身：卡密是可兑现凭据，Redis 里的 key 会出现在
// slowlog / MONITOR / 备份 / 运维截图里。哈希与 redeem_codes.code_hash 同值，
// 排查时仍能按同一把卡对上（只是拿不回明文）。空卡密退化为字面量，调用方
// 会在查库阶段直接 404，不会放大可用性。
func redeemLockKey(code string) string {
	normalized := strings.TrimSpace(code)
	if normalized == "" {
		return redeemLockKeyPrefix + "empty"
	}
	return redeemLockKeyPrefix + service.HashRedeemCode(normalized)
}

type redeemCache struct {
	rdb *redis.Client
}

func NewRedeemCache(rdb *redis.Client) service.RedeemCache {
	return &redeemCache{rdb: rdb}
}

func (c *redeemCache) GetRedeemAttemptCount(ctx context.Context, userID int64) (int, error) {
	key := redeemRateLimitKey(userID)
	count, err := c.rdb.Get(ctx, key).Int()
	if err == redis.Nil {
		return 0, nil
	}
	return count, err
}

func (c *redeemCache) IncrementRedeemAttemptCount(ctx context.Context, userID int64) error {
	key := redeemRateLimitKey(userID)
	return incrementRedeemAttemptScript.Run(
		ctx,
		c.rdb,
		[]string{key},
		redeemRateLimitWindow.Milliseconds(),
	).Err()
}

func (c *redeemCache) AcquireRedeemLock(ctx context.Context, code string, ttl time.Duration) (bool, error) {
	key := redeemLockKey(code)
	return c.rdb.SetNX(ctx, key, 1, ttl).Result()
}

func (c *redeemCache) ReleaseRedeemLock(ctx context.Context, code string) error {
	key := redeemLockKey(code)
	return c.rdb.Del(ctx, key).Err()
}
