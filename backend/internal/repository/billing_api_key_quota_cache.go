package repository

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

// API Key 级 USD 配额的实时用量 + 在途预占：
//   - apikey:quota:{id}               HASH  field=quota_used（结算后的实时用量，api_keys.quota_used 的镜像）
//   - apikey:quota_inflight:{id}      ZSET  member=requestID score=过期时间(ms)
//   - apikey:quota_inflight_amt:{id}  HASH  field=requestID value=预估金额(USD)
//
// 三个 key 共用同一个 hash tag，保证 Redis Cluster 下同一个 Lua 可以一起访问。
// quota_used 字段故意不续期：30 分钟后过期，由下一次准入从数据库重新播种，
// 这样即便某次结算镜像写入丢失，也会在一代 TTL 内自愈（DB 才是权威）。
const (
	apiKeyQuotaCounterKeyPrefix     = "apikey:quota:"
	apiKeyQuotaInflightKeyPrefix    = "apikey:quota_inflight:"
	apiKeyQuotaInflightAmtKeyPrefix = "apikey:quota_inflight_amt:"
	apiKeyQuotaUsedField            = "quota_used"
)

func apiKeyQuotaKeys(keyID int64) (counterKey, zsetKey, amountKey string) {
	tag := fmt.Sprintf("{%d}", keyID)
	return apiKeyQuotaCounterKeyPrefix + tag,
		apiKeyQuotaInflightKeyPrefix + tag,
		apiKeyQuotaInflightAmtKeyPrefix + tag
}

var (
	// KEYS: [1]=counter hash [2]=inflight zset [3]=inflight amount hash
	// ARGV: [1]=now_ms [2]=expire_at_ms [3]=member [4]=estimate [5]=quota [6]=ttl_ms
	// 返回 {ready, allowed, used(string), inflight(string)}
	//   ready=0 → 计数器不存在，调用方需要先从数据库播种再重试；
	//   规则：quota > 0 且 used + inflight + estimate > quota 时拒绝；
	//   放行时登记本次预估金额并顺延两个在途 key 的 TTL。
	reserveAPIKeyQuotaScript = redis.NewScript(`
		local used = redis.call('HGET', KEYS[1], 'quota_used')
		if not used then
			return {0, 0, '0', '0'}
		end
		local now = tonumber(ARGV[1])
		local expired = redis.call('ZRANGEBYSCORE', KEYS[2], '-inf', now)
		if #expired > 0 then
			redis.call('ZREMRANGEBYSCORE', KEYS[2], '-inf', now)
			redis.call('HDEL', KEYS[3], unpack(expired))
		end
		local vals = redis.call('HVALS', KEYS[3])
		local inflight = 0
		for i = 1, #vals do
			inflight = inflight + (tonumber(vals[i]) or 0)
		end
		local base = tonumber(used) or 0
		local estimate = tonumber(ARGV[4])
		local quota = tonumber(ARGV[5])
		if quota > 0 and (base + inflight + estimate) > quota then
			return {1, 0, tostring(base), tostring(inflight)}
		end
		redis.call('ZADD', KEYS[2], tonumber(ARGV[2]), ARGV[3])
		redis.call('HSET', KEYS[3], ARGV[3], ARGV[4])
		local ttl = tonumber(ARGV[6])
		redis.call('PEXPIRE', KEYS[2], ttl)
		redis.call('PEXPIRE', KEYS[3], ttl)
		return {1, 1, tostring(base), tostring(inflight)}
	`)

	// KEYS: [1]=counter hash  ARGV: [1]=quota_used [2]=ttl_ms
	// 只在字段不存在时写入（并发首个请求只有一个播种生效），不覆盖更新的值。
	seedAPIKeyQuotaUsageScript = redis.NewScript(`
		redis.call('HSETNX', KEYS[1], 'quota_used', ARGV[1])
		if redis.call('PTTL', KEYS[1]) < 0 then
			redis.call('PEXPIRE', KEYS[1], tonumber(ARGV[2]))
		end
		return 1
	`)

	// KEYS: [1]=counter hash  ARGV: [1]=cost
	// 计数器不存在说明已过期：本次增量直接跳过，由下一次准入从数据库重新播种，
	// 数据库在同一事务里已经加过这笔费用，不会漏计。
	incrementAPIKeyQuotaUsageScript = redis.NewScript(`
		if redis.call('EXISTS', KEYS[1]) == 0 then
			return 0
		end
		redis.call('HINCRBYFLOAT', KEYS[1], 'quota_used', tonumber(ARGV[1]))
		return 1
	`)

	releaseAPIKeyQuotaScript = redis.NewScript(`
		redis.call('ZREM', KEYS[1], ARGV[1])
		redis.call('HDEL', KEYS[2], ARGV[1])
		return 1
	`)

	// KEYS: [1]=inflight zset [2]=inflight amount hash
	// ARGV: [1]=member [2]=expire_at_ms [3]=ttl_ms [4]=now_ms
	// 仅当成员仍存在且未过期时续期；已过期未清理的成员不得被复活。
	renewAPIKeyQuotaScript = redis.NewScript(`
		local score = redis.call('ZSCORE', KEYS[1], ARGV[1])
		if not score then
			return 0
		end
		if tonumber(score) <= tonumber(ARGV[4]) then
			redis.call('ZREM', KEYS[1], ARGV[1])
			redis.call('HDEL', KEYS[2], ARGV[1])
			return 0
		end
		redis.call('ZADD', KEYS[1], 'XX', tonumber(ARGV[2]), ARGV[1])
		local ttl = tonumber(ARGV[3])
		if redis.call('PTTL', KEYS[1]) < ttl then
			redis.call('PEXPIRE', KEYS[1], ttl)
		end
		if redis.call('PTTL', KEYS[2]) < ttl then
			redis.call('PEXPIRE', KEYS[2], ttl)
		end
		return 1
	`)
)

// SeedAPIKeyQuotaUsage 实现 service.APIKeyQuotaReservationCache。
func (c *billingCache) SeedAPIKeyQuotaUsage(ctx context.Context, keyID int64, quotaUsed float64, ttl time.Duration) error {
	counterKey, _, _ := apiKeyQuotaKeys(keyID)
	ttlMs := ttl.Milliseconds()
	if ttlMs <= 0 {
		ttlMs = 1
	}
	return seedAPIKeyQuotaUsageScript.Run(ctx, c.rdb, []string{counterKey},
		strconv.FormatFloat(quotaUsed, 'f', -1, 64),
		ttlMs,
	).Err()
}

// ReserveAPIKeyQuota 实现 service.APIKeyQuotaReservationCache。
// 返回值 ready=false 表示计数器尚未播种，调用方需要先播种再重试。
func (c *billingCache) ReserveAPIKeyQuota(ctx context.Context, keyID int64, requestID string, estimate, quota float64, ttl time.Duration) (bool, bool, float64, float64, error) {
	counterKey, zsetKey, amountKey := apiKeyQuotaKeys(keyID)
	ttlMs := ttl.Milliseconds()
	if ttlMs <= 0 {
		ttlMs = 1
	}
	now := time.Now().UnixMilli()
	res, err := reserveAPIKeyQuotaScript.Run(ctx, c.rdb, []string{counterKey, zsetKey, amountKey},
		now,
		now+ttlMs,
		requestID,
		strconv.FormatFloat(estimate, 'f', -1, 64),
		strconv.FormatFloat(quota, 'f', -1, 64),
		ttlMs,
	).Slice()
	if err != nil {
		return false, false, 0, 0, err
	}
	if len(res) < 4 {
		return false, false, 0, 0, fmt.Errorf("unexpected api key quota reservation reply: %v", res)
	}
	ready, _ := res[0].(int64)
	allowed, _ := res[1].(int64)
	used := parseAPIKeyQuotaReplyFloat(res[2])
	inflight := parseAPIKeyQuotaReplyFloat(res[3])
	return ready == 1, allowed == 1, used, inflight, nil
}

func parseAPIKeyQuotaReplyFloat(v any) float64 {
	s, ok := v.(string)
	if !ok {
		return 0
	}
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

// ReleaseAPIKeyQuota 实现 service.APIKeyQuotaReservationCache。
func (c *billingCache) ReleaseAPIKeyQuota(ctx context.Context, keyID int64, requestID string) error {
	_, zsetKey, amountKey := apiKeyQuotaKeys(keyID)
	return releaseAPIKeyQuotaScript.Run(ctx, c.rdb, []string{zsetKey, amountKey}, requestID).Err()
}

// IncrementAPIKeyQuotaUsage 实现 service.APIKeyQuotaReservationCache。
func (c *billingCache) IncrementAPIKeyQuotaUsage(ctx context.Context, keyID int64, cost float64) error {
	counterKey, _, _ := apiKeyQuotaKeys(keyID)
	_, err := incrementAPIKeyQuotaUsageScript.Run(ctx, c.rdb, []string{counterKey},
		strconv.FormatFloat(cost, 'f', -1, 64),
	).Result()
	if err != nil && err != redis.Nil {
		return err
	}
	return nil
}

// InvalidateAPIKeyQuotaUsage 实现 service.APIKeyQuotaReservationCache。
// 只删除实时用量镜像；在途预占保留，避免把仍在请求中的占用清空。
func (c *billingCache) InvalidateAPIKeyQuotaUsage(ctx context.Context, keyID int64) error {
	counterKey, _, _ := apiKeyQuotaKeys(keyID)
	return c.rdb.Del(ctx, counterKey).Err()
}

// RenewAPIKeyQuota 实现 service.APIKeyQuotaReservationCache。
func (c *billingCache) RenewAPIKeyQuota(ctx context.Context, keyID int64, requestID string, ttl time.Duration) (bool, error) {
	_, zsetKey, amountKey := apiKeyQuotaKeys(keyID)
	ttlMs := ttl.Milliseconds()
	if ttlMs <= 0 {
		ttlMs = 1
	}
	now := time.Now().UnixMilli()
	n, err := renewAPIKeyQuotaScript.Run(ctx, c.rdb, []string{zsetKey, amountKey}, requestID, now+ttlMs, ttlMs, now).Int64()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

var (
	_ service.APIKeyQuotaReservationCache = (*billingCache)(nil)
)
