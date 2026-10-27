package repository

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

// Each scope uses an independent Cluster hash tag. The registration is atomic;
// authoritative DB reads happen after it, never before the in-flight snapshot.
var reserveFixedChargeScopeScript = redis.NewScript(`
local expired = redis.call('ZRANGEBYSCORE', KEYS[1], '-inf', ARGV[1])
for _, id in ipairs(expired) do
  redis.call('ZREM', KEYS[1], id)
  redis.call('HDEL', KEYS[2], id)
end
local sum = 0
local values = redis.call('HGETALL', KEYS[2])
for i = 1, #values, 2 do
  if values[i] ~= ARGV[3] then sum = sum + tonumber(values[i+1]) end
end
redis.call('ZADD', KEYS[1], ARGV[2], ARGV[3])
redis.call('HSET', KEYS[2], ARGV[3], ARGV[4])
redis.call('PEXPIRE', KEYS[1], ARGV[5])
redis.call('PEXPIRE', KEYS[2], ARGV[5])
return tostring(sum)
`)

func fixedChargeScopeKeys(scope string) []string {
	return []string{fmt.Sprintf("billing:fixed:{%s}:pending", scope), fmt.Sprintf("billing:fixed:{%s}:amount", scope)}
}

func (c *billingCache) ReserveInflightScope(ctx context.Context, scope, requestID string, amount float64, ttl time.Duration) (float64, error) {
	if scope == "" || requestID == "" || amount <= 0 || math.IsNaN(amount) || math.IsInf(amount, 0) || ttl <= 0 {
		return 0, fmt.Errorf("invalid fixed charge reservation")
	}
	now, ttlMS := time.Now().UnixMilli(), ttl.Milliseconds()
	value, err := reserveFixedChargeScopeScript.Run(ctx, c.rdb, fixedChargeScopeKeys(scope), now, now+ttlMS, requestID, strconv.FormatFloat(amount, 'f', -1, 64), ttlMS).Text()
	if err != nil {
		return 0, err
	}
	return strconv.ParseFloat(value, 64)
}

func (c *billingCache) ReleaseInflightScope(ctx context.Context, scope, requestID string) error {
	return releaseInflightBalanceScript.Run(ctx, c.rdb, fixedChargeScopeKeys(scope), requestID).Err()
}

var _ service.InflightScopeReservationCache = (*billingCache)(nil)
