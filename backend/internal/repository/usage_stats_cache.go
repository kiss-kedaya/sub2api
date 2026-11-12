package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"
)

const (
	// usageStatsCachePrefix namespaces short-lived dashboard/usage aggregates.
	// The hash covers every parameter that changes the result, so two different
	// queries can never share an entry.
	usageStatsCachePrefix = "stats:usage:"
	// usageStatsCacheOpTimeout bounds one Redis round trip. A slow or broken
	// cache must not add latency to the request that simply wants its database
	// answer, so cache IO is capped well below a typical query budget.
	usageStatsCacheOpTimeout = 150 * time.Millisecond
	// usageStatsCacheRetryAfter pauses cache IO for a moment after a failure, so
	// a Redis outage costs one failed round trip per window instead of one per
	// request.
	usageStatsCacheRetryAfter = time.Second
)

// usageStatsCache is a best-effort, short-TTL Redis cache in front of the
// dashboard and usage aggregation queries. Every method is safe to call on a
// nil receiver via the helpers below: a nil cache means "query the database".
type usageStatsCache struct {
	rdb *redis.Client
	ttl time.Duration
	sf  singleflight.Group
	// skipUntil is the Unix-nano deadline observed after a Redis failure. It is
	// written from request goroutines, so it uses atomics.
	skipUntil atomic.Int64
}

// newUsageStatsCache returns nil when caching is disabled, so callers keep the
// plain database path with no extra branch.
func newUsageStatsCache(rdb *redis.Client, cfg *config.Config) *usageStatsCache {
	if rdb == nil || cfg == nil || !cfg.UsageStatsCache.Enabled || cfg.UsageStatsCache.TTL <= 0 {
		return nil
	}
	return &usageStatsCache{rdb: rdb, ttl: cfg.UsageStatsCache.TTL}
}

// usageStatsCacheKey is the canonical identity of one cached aggregate. Fields
// are strings/ints (never pointers) so JSON marshalling is deterministic.
type usageStatsCacheKey struct {
	Query       string `json:"query"`
	UserID      int64  `json:"user_id,omitempty"`
	APIKeyID    int64  `json:"api_key_id,omitempty"`
	AccountID   int64  `json:"account_id,omitempty"`
	GroupID     int64  `json:"group_id,omitempty"`
	Model       string `json:"model,omitempty"`
	ModelSource string `json:"model_source,omitempty"`
	Timezone    string `json:"timezone,omitempty"`
	Start       string `json:"start,omitempty"`
	End         string `json:"end,omitempty"`
	// Filters carries the full usage filter set for queries that accept one.
	// It is a value (not a pointer) so nil optionals stay nil in the hash.
	Filters *usageFiltersFingerprint `json:"filters,omitempty"`
	Extra   string                   `json:"extra,omitempty"`
}

// usageFiltersFingerprint mirrors the comparable portion of UsageLogFilters.
type usageFiltersFingerprint struct {
	UserID                int64  `json:"user_id,omitempty"`
	APIKeyID              int64  `json:"api_key_id,omitempty"`
	AccountID             int64  `json:"account_id,omitempty"`
	GroupID               int64  `json:"group_id,omitempty"`
	RequestID             string `json:"request_id,omitempty"`
	Model                 string `json:"model,omitempty"`
	ModelFilterSource     string `json:"model_source,omitempty"`
	RequestType           *int16 `json:"request_type,omitempty"`
	Stream                *bool  `json:"stream,omitempty"`
	NativeCompactionV2    *bool  `json:"native_compaction_v2,omitempty"`
	BillingType           *int8  `json:"billing_type,omitempty"`
	BillingMode           string `json:"billing_mode,omitempty"`
	UpstreamModelMismatch *bool  `json:"upstream_model_mismatch,omitempty"`
	StartTime             string `json:"start,omitempty"`
	EndTime               string `json:"end,omitempty"`
}

func usageFiltersFingerprintOf(filters UsageLogFilters) *usageFiltersFingerprint {
	out := &usageFiltersFingerprint{
		UserID:                filters.UserID,
		APIKeyID:              filters.APIKeyID,
		AccountID:             filters.AccountID,
		GroupID:               filters.GroupID,
		RequestID:             filters.RequestID,
		Model:                 filters.Model,
		ModelFilterSource:     filters.ModelFilterSource,
		RequestType:           filters.RequestType,
		Stream:                filters.Stream,
		NativeCompactionV2:    filters.NativeCompactionV2,
		BillingType:           filters.BillingType,
		BillingMode:           filters.BillingMode,
		UpstreamModelMismatch: filters.UpstreamModelMismatch,
		// ExactTotal/CompactAssociations/AccountNameOnly only affect list
		// endpoints, never an aggregate, so they are intentionally omitted.
	}
	if filters.StartTime != nil {
		out.StartTime = filters.StartTime.UTC().Format(time.RFC3339Nano)
	}
	if filters.EndTime != nil {
		out.EndTime = filters.EndTime.UTC().Format(time.RFC3339Nano)
	}
	return out
}

func usageStatsRedisKey(key usageStatsCacheKey) string {
	raw, err := json.Marshal(key)
	if err != nil {
		// The key struct only holds JSON-safe primitives; a marshal failure is a
		// programming error, and an uncacheable query is still a correct answer.
		return ""
	}
	sum := sha256.Sum256(raw)
	return usageStatsCachePrefix + hex.EncodeToString(sum[:])
}

// usageStatsCacheResult keeps the singleflight payload typed by caller.
type usageStatsCacheResult[T any] struct {
	value T
}

// loadUsageStatsCached serves one aggregate from Redis when possible, otherwise
// from load(). It never returns cache errors: a broken cache degrades to the
// database path.
func loadUsageStatsCached[T any](
	cache *usageStatsCache,
	ctx context.Context,
	key usageStatsCacheKey,
	load func() (T, error),
) (T, error) {
	if cache == nil {
		return load()
	}
	redisKey := usageStatsRedisKey(key)
	if redisKey == "" {
		return load()
	}
	// Collapse concurrent misses for the same key inside this process; the Redis
	// entry then serves every other replica.
	payload, err, _ := cache.sf.Do(redisKey, func() (any, error) {
		if raw, ok := cache.get(ctx, redisKey); ok {
			var value T
			if json.Unmarshal(raw, &value) == nil {
				return usageStatsCacheResult[T]{value: value}, nil
			}
			// A corrupt or older payload is treated as a miss, never as an error.
		}
		value, loadErr := load()
		if loadErr != nil {
			return nil, loadErr
		}
		cache.set(ctx, redisKey, value)
		return usageStatsCacheResult[T]{value: value}, nil
	})
	if err != nil {
		var zero T
		return zero, err
	}
	result, ok := payload.(usageStatsCacheResult[T])
	if !ok {
		var zero T
		return zero, errors.New("usage stats cache: unexpected payload type")
	}
	return result.value, nil
}

func (c *usageStatsCache) paused() bool {
	return time.Now().UnixNano() < c.skipUntil.Load()
}

func (c *usageStatsCache) pause(err error) {
	c.skipUntil.Store(time.Now().Add(usageStatsCacheRetryAfter).UnixNano())
	logger.L().Warn("usage stats cache unavailable, falling back to database", zap.Error(err))
}

func (c *usageStatsCache) get(ctx context.Context, key string) ([]byte, bool) {
	if c.paused() || ctx.Err() != nil {
		return nil, false
	}
	opCtx, cancel := context.WithTimeout(ctx, usageStatsCacheOpTimeout)
	defer cancel()
	raw, err := c.rdb.Get(opCtx, key).Bytes()
	if err != nil {
		if !errors.Is(err, redis.Nil) && ctx.Err() == nil {
			c.pause(err)
		}
		return nil, false
	}
	return raw, true
}

func (c *usageStatsCache) set(ctx context.Context, key string, value any) {
	if c.paused() || ctx.Err() != nil {
		return
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return
	}
	opCtx, cancel := context.WithTimeout(ctx, usageStatsCacheOpTimeout)
	defer cancel()
	if err := c.rdb.Set(opCtx, key, raw, c.ttl).Err(); err != nil && ctx.Err() == nil {
		c.pause(err)
	}
}
