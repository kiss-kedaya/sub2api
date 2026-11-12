package middleware

import (
	"context"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

// apiKeyRiskRedisPrefix stores one JSON marker per auto-disabled key. It is a
// Redis key, not a database column: the marker is derived state with a TTL, so
// adding a migration for it would be more than this feature needs.
const apiKeyRiskRedisPrefix = "gateway_key_risk:"

// redisAPIKeyRiskStore is the Redis-backed service.APIKeyRiskStore. It lives in
// the middleware package because the service layer must not import Redis
// (depguard), while middleware already owns the shared client.
type redisAPIKeyRiskStore struct {
	client *redis.Client
}

// NewRedisAPIKeyRiskStore returns the store the gateway wires at router
// construction. A nil client returns a nil store, which keeps the risk markers
// process-local instead of failing every write.
func NewRedisAPIKeyRiskStore(client *redis.Client) service.APIKeyRiskStore {
	if client == nil {
		return nil
	}
	return &redisAPIKeyRiskStore{client: client}
}

func apiKeyRiskRedisKey(keyID int64) string {
	return apiKeyRiskRedisPrefix + strconv.FormatInt(keyID, 10)
}

func (s *redisAPIKeyRiskStore) Save(ctx context.Context, keyID int64, payload []byte, ttl time.Duration) error {
	return s.client.Set(ctx, apiKeyRiskRedisKey(keyID), payload, ttl).Err()
}

func (s *redisAPIKeyRiskStore) Load(ctx context.Context, keyID int64) ([]byte, error) {
	return s.client.Get(ctx, apiKeyRiskRedisKey(keyID)).Bytes()
}

func (s *redisAPIKeyRiskStore) Delete(ctx context.Context, keyID int64) error {
	return s.client.Del(ctx, apiKeyRiskRedisKey(keyID)).Err()
}
