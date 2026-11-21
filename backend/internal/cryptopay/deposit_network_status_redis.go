package cryptopay

import (
	"context"
	"encoding/json"
	"time"

	"github.com/redis/go-redis/v9"
)

const depositNetworkStatusKey = "crypto:deposit-network-status:v1:"

type redisNetworkStatusStore struct {
	rdb *redis.Client
}

// Writes are dispatched while the scan holds the lock, but Redis may finish
// after another owner publishes. Never let that delayed write replace a newer
// observation. Each canonical network has its own expiring key.
var saveNetworkStatusSnapshot = redis.NewScript(`
local old = redis.call('GET', KEYS[1])
if old then
  local ok, sample = pcall(cjson.decode, old)
  if ok and type(sample) == 'table' and type(sample.at) == 'number' and sample.at >= tonumber(ARGV[1]) then
    return 0
  end
end
return redis.call('PSETEX', KEYS[1], ARGV[2], ARGV[3])
`)

func (s redisNetworkStatusStore) save(ctx context.Context, chain string, sample networkStatusSnapshot, ttl time.Duration) error {
	encoded, err := json.Marshal(sample)
	if err != nil {
		return err
	}
	return saveNetworkStatusSnapshot.Run(ctx, s.rdb, []string{depositNetworkStatusKey + chain}, sample.ObservedMS, ttl.Milliseconds(), encoded).Err()
}

func (s redisNetworkStatusStore) load(ctx context.Context, chains []string) (map[string]networkStatusSnapshot, error) {
	keys := make([]string, len(chains))
	for i, chain := range chains {
		keys[i] = depositNetworkStatusKey + chain
	}
	values, err := s.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}
	out := make(map[string]networkStatusSnapshot, len(values))
	for i, value := range values {
		raw, ok := value.(string)
		if !ok {
			continue
		}
		var sample networkStatusSnapshot
		if json.Unmarshal([]byte(raw), &sample) == nil {
			out[chains[i]] = sample
		}
	}
	return out, nil
}
