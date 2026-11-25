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
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	gocache "github.com/patrickmn/go-cache"
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
	// Shared aggregates outlive individual callers, but never wait indefinitely.
	usageStatsCacheLoadTimeout = 15 * time.Second
	usageStatsCacheFailureTTL  = 5 * time.Second
	// usageStatsCacheTimeoutTTL is how long a timed-out aggregate is remembered. A scan that did not fit
	// the load budget will not fit it again five seconds later; asking again at every refresh only keeps
	// a slot and the disk busy for another full budget.
	usageStatsCacheTimeoutTTL = 30 * time.Second
)

// usageStatsCache is a best-effort, short-TTL Redis cache in front of the
// dashboard and usage aggregation queries. Every method is safe to call on a
// nil receiver via the helpers below: a nil cache means "query the database".
type usageStatsCache struct {
	rdb      *redis.Client
	ttl      time.Duration
	sf       singleflight.Group
	failures *gocache.Cache // Short process-local backoff; never publish database errors in Redis.
	// gate caps the end-user aggregates that run on the database at once. Nil: no cap.
	gate *usageStatsGate
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
	return &usageStatsCache{
		rdb: rdb, ttl: cfg.UsageStatsCache.TTL,
		failures: gocache.New(usageStatsCacheFailureTTL, time.Minute),
		gate:     newUsageStatsGate(cfg.UsageStatsCache.MaxConcurrentLoads, cfg.UsageStatsCache.LoadQueueTimeout),
	}
}

// usageStatsCacheKey is the canonical identity of one cached aggregate. Fields
// contain only JSON-safe values, so marshalling is deterministic.
type usageStatsCacheKey struct {
	Query       string  `json:"query"`
	UserID      int64   `json:"user_id,omitempty"`
	APIKeyID    int64   `json:"api_key_id,omitempty"`
	APIKeyIDs   []int64 `json:"api_key_ids,omitempty"`
	AccountID   int64   `json:"account_id,omitempty"`
	GroupID     int64   `json:"group_id,omitempty"`
	Model       string  `json:"model,omitempty"`
	ModelSource string  `json:"model_source,omitempty"`
	Timezone    string  `json:"timezone,omitempty"`
	Start       string  `json:"start,omitempty"`
	End         string  `json:"end,omitempty"`
	// Filters carries the full usage filter set for queries that accept one.
	// It is a value (not a pointer) so nil optionals stay nil in the hash.
	Filters *usageFiltersFingerprint `json:"filters,omitempty"`
	Extra   string                   `json:"extra,omitempty"`
}

// endUserScoped reports whether the aggregate is one user's or one API key's, i.e. something an end user
// can trigger by opening a page, as opposed to an admin report over everything or over one account.
// Only these go through the concurrency gate: admin reports have their own limit, and the account scans
// that feed rate-limit decisions must never queue behind a user's usage page.
func (k usageStatsCacheKey) endUserScoped() bool {
	if k.UserID > 0 || k.APIKeyID > 0 || len(k.APIKeyIDs) > 0 {
		return true
	}
	return k.Filters != nil && (k.Filters.UserID > 0 || k.Filters.APIKeyID > 0)
}

// usageFiltersFingerprint mirrors the comparable portion of UsageLogFilters.
type usageFiltersFingerprint struct {
	UserID                int64  `json:"user_id,omitempty"`
	APIKeyID              int64  `json:"api_key_id,omitempty"`
	AccountID             int64  `json:"account_id,omitempty"`
	GroupID               int64  `json:"group_id,omitempty"`
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
	// OmitAccountCost and StatsScope change the shape of the answer (account cost left at zero, only part of the
	// stats), so they are part of the identity: an admin view must never be served a user-shaped entry.
	OmitAccountCost bool   `json:"omit_account_cost,omitempty"`
	StatsScope      string `json:"stats_scope,omitempty"`
}

func usageFiltersFingerprintOf(filters UsageLogFilters) *usageFiltersFingerprint {
	out := &usageFiltersFingerprint{
		UserID:                filters.UserID,
		APIKeyID:              filters.APIKeyID,
		AccountID:             filters.AccountID,
		GroupID:               filters.GroupID,
		Model:                 filters.Model,
		ModelFilterSource:     filters.ModelFilterSource,
		RequestType:           filters.RequestType,
		Stream:                filters.Stream,
		NativeCompactionV2:    filters.NativeCompactionV2,
		BillingType:           filters.BillingType,
		BillingMode:           filters.BillingMode,
		UpstreamModelMismatch: filters.UpstreamModelMismatch,
		OmitAccountCost:       filters.OmitAccountCost,
		StatsScope:            filters.StatsScope,
		// RequestID narrows the list only; no aggregate applies it, so it must not split the key (typing a
		// request id would otherwise recompute every card, and a random one would dodge the cache).
		// ExactTotal/CompactAssociations/AccountNameOnly likewise only affect list endpoints.
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
	raw   []byte
}

// loadUsageStatsCached serves one aggregate from Redis when possible, otherwise
// from load(). It never returns cache errors: a broken cache degrades to the
// database path.
func loadUsageStatsCached[T any](
	cache *usageStatsCache,
	ctx context.Context,
	key usageStatsCacheKey,
	load func(context.Context) (T, error),
) (T, error) {
	return loadUsageStatsCachedWithTimeout(cache, ctx, key, usageStatsCacheLoadTimeout, load)
}

// loadUsageStatsCachedWithTimeout is loadUsageStatsCached with an explicit
// database budget for the shared load. Most aggregates finish well inside the
// default budget; an aggregate whose cold path can legitimately run longer (the
// admin account history scan) passes its own instead of failing early.
func loadUsageStatsCachedWithTimeout[T any](
	cache *usageStatsCache,
	ctx context.Context,
	key usageStatsCacheKey,
	loadTimeout time.Duration,
	load func(context.Context) (T, error),
) (T, error) {
	if err := ctx.Err(); err != nil {
		var zero T
		return zero, err
	}
	if cache == nil {
		return load(ctx)
	}
	redisKey := usageStatsRedisKey(key)
	if redisKey == "" {
		return load(ctx)
	}
	// Only the waiter uses the request deadline. Disconnecting one caller must
	// neither cancel the shared database query nor prevent populating Redis.
	ch := cache.sf.DoChan(redisKey, func() (any, error) {
		base := context.WithoutCancel(ctx)
		if raw, ok := cache.get(base, redisKey); ok {
			var value T
			if json.Unmarshal(raw, &value) == nil {
				return usageStatsCacheResult[T]{raw: raw}, nil
			}
			// A corrupt or older payload is treated as a miss, never as an error.
		}
		if failure, ok := cache.failures.Get(redisKey); ok {
			if err, valid := failure.(error); valid {
				return nil, err
			}
		}
		// Past this point the aggregate really runs on the database, so this is where the cap applies (a hit
		// or a remembered failure above never takes a slot). Waiting for a slot is not part of the database
		// budget: the load context starts once the slot is ours.
		if key.endUserScoped() {
			release, err := cache.gate.acquire(base)
			if err != nil {
				// Not remembered: being turned away says nothing about the next attempt.
				return nil, err
			}
			defer release()
		}
		loadCtx, cancel := context.WithTimeout(base, loadTimeout)
		defer cancel()
		value, loadErr := load(loadCtx)
		if loadErr != nil {
			cache.rememberFailure(redisKey, loadErr)
			return nil, loadErr
		}
		// Publish bytes, not shared pointers: user handlers redact admin-only
		// fields in place, and each waiter must own its result.
		raw, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			return usageStatsCacheResult[T]{value: value}, nil
		}
		// base, not loadCtx: a scan that used up its budget still produced a good answer worth keeping.
		cache.set(base, redisKey, raw)
		return usageStatsCacheResult[T]{raw: raw}, nil
	})
	select {
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	case result := <-ch:
		if result.Err != nil {
			var zero T
			return zero, result.Err
		}
		payload, ok := result.Val.(usageStatsCacheResult[T])
		if !ok {
			var zero T
			return zero, errors.New("usage stats cache: unexpected payload type")
		}
		if payload.raw != nil {
			var value T
			if err := json.Unmarshal(payload.raw, &value); err != nil {
				return value, err
			}
			return value, nil
		}
		return payload.value, nil
	}
}

func (c *usageStatsCache) paused() bool {
	return time.Now().UnixNano() < c.skipUntil.Load()
}

// rememberFailure keeps a database failure for a short while, so a struggling database is not asked the same
// question again by every refresh. Two kinds are treated specially:
//   - a cancellation is not remembered at all: the shared load is detached from every caller, so something else
//     (an operator killing the statement, a shutdown) stopped it, and that says nothing about the next attempt;
//   - a timeout is remembered longer than other errors, because the same scan will time out again.
func (c *usageStatsCache) rememberFailure(key string, err error) {
	switch {
	case errors.Is(err, context.Canceled):
		return
	case usagestats.IsStatsTimeout(err):
		c.failures.Set(key, err, usageStatsCacheTimeoutTTL)
	default:
		c.failures.SetDefault(key, err)
	}
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

func (c *usageStatsCache) set(ctx context.Context, key string, raw []byte) {
	if c.paused() || ctx.Err() != nil {
		return
	}
	opCtx, cancel := context.WithTimeout(ctx, usageStatsCacheOpTimeout)
	defer cancel()
	if err := c.rdb.Set(opCtx, key, raw, c.ttl).Err(); err != nil && ctx.Err() == nil {
		c.pause(err)
	}
}
