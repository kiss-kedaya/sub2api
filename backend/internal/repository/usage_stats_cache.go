package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
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
	// a slot and the disk busy for another full budget. (One API key's multi-day aggregate has its own budget and
	// memory, below.)
	usageStatsCacheTimeoutTTL = 30 * time.Second

	// usageStatsKeyWindowTTL is how long the answer to one API key's multi-day aggregate (the /v1/usage reports,
	// the per-key usage panels) is reused. Clients poll those every few seconds to a minute, so the cache's own
	// 60 s TTL lapsed just before the next poll and a heavy key's month was re-aggregated from usage_logs on
	// almost every call. It is a constant, not a setting: the cache's TTL still governs everything else.
	// It is not shorter than the grid the moving /v1/usage window is cut on (handler.usageDefaultRangeBucket, also
	// 15 minutes): an answer computed anywhere inside one grid cell is therefore still cached when the cell ends,
	// so a key is aggregated once per cell however often it is polled. Keep the two in step.
	usageStatsKeyWindowTTL = 15 * time.Minute
	// usageStatsKeyWindowOver is the window length above which that longer TTL applies. A single natural day
	// (23 to 25 hours around a DST change) is a "today" view and keeps the cache's own TTL.
	usageStatsKeyWindowOver = 25 * time.Hour

	// usageStatsKeyWindowLoadTimeout is the database budget of one API key's multi-day aggregate, in place of
	// usageStatsCacheLoadTimeout. A heavy key's 15 days of usage_logs need 16 to 28 seconds while the disk is
	// saturated. Cut at 15 seconds such a scan produced nothing to publish, was started again after the timeout
	// memory lapsed, and burnt another 15 seconds and ~2 GB of reads every time: on production 49 of the 76
	// statements cancelled in 40 minutes were these. It stays below the database role's statement_timeout (30 s)
	// on purpose: then it is our own budget that ends the scan, which rememberFailure recognises by the load
	// context's error, and not the server's cancel, which says nothing about whose budget ran out.
	usageStatsKeyWindowLoadTimeout = 28 * time.Second
	// usageStatsKeyWindowTimeoutTTL is how long a timed-out multi-day key aggregate is remembered (in this
	// process). A scan that did not fit 28 seconds will not fit them again half a minute later, and retrying at
	// the pace of usageStatsCacheTimeoutTTL only keeps one more slot and the disk busy for 28 seconds each time.
	usageStatsKeyWindowTimeoutTTL = 5 * time.Minute

	// usageStatsKeyClosedDaysTTL is how long the closed days of one API key's by-day trend are kept (see
	// keyDailyTrendSplit). A day that is over only changes through a late row or a hand rewrite of usage_logs, so
	// it is read from the table once and then reused until the key changes: the question names [start, splitAt),
	// and splitAt moves at every local midnight, so the next day asks a new question and nothing has to be
	// invalidated. 26 hours is the longest natural day (25 at a DST change) plus an hour of margin: an entry is
	// still there when the midnight that retires it comes, and it does not linger long after. It is above zero on
	// purpose: that keeps the closed days in the class of one key's multi-day aggregate (keyWindowAggregate), with
	// its 28 second budget and 5 minute memory of a timeout.
	usageStatsKeyClosedDaysTTL = 26 * time.Hour
)

// keyAggregateTTL is the cache lifetime for an aggregate of one API key over [start, end): usageStatsKeyWindowTTL
// for a multi-day window, 0 (the cache's own TTL) for anything else. Callers put it in usageStatsCacheKey.TTL.
func keyAggregateTTL(apiKeyID int64, start, end time.Time) time.Duration {
	if apiKeyID > 0 && end.Sub(start) > usageStatsKeyWindowOver {
		return usageStatsKeyWindowTTL
	}
	return 0
}

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
	// TTL is how long the answer stays cached when it must outlive the cache's own TTL (see keyAggregateTTL);
	// 0 keeps the cache's TTL. It says how long to reuse an answer, not what was asked, so it is not hashed.
	// It also marks the class of one API key's multi-day aggregate (keyWindowAggregate), which gets its own scan
	// budget and timeout memory: set it only through keyAggregateTTL, or usageStatsKeyClosedDaysTTL for the closed
	// days of a split by-day trend or of a user's dashboard (userDashboardClosedTTL).
	TTL time.Duration `json:"-"`
}

// gateOwner names who a cache miss is charged to in the concurrency gate, or "" when the aggregate is not an
// end user's: an admin report over everything or over one account. Only end-user aggregates go through the
// gate: admin reports are few and sit behind the admin panel, and the account scans that feed rate-limit
// decisions must never queue behind a user's usage page.
//
// The owner is the user when the key names one, otherwise the API key: a key-scoped aggregate such as
// /v1/usage never carries its owner down to the repository, so a client with several keys gets a share
// per key (the shared slots still bound them all). A list of keys is charged to its lowest id, which is the
// same for every visit to the same page of one user's keys.
func (k usageStatsCacheKey) gateOwner() string {
	switch {
	case k.UserID > 0:
		return "u" + strconv.FormatInt(k.UserID, 10)
	case k.Filters != nil && k.Filters.UserID > 0:
		return "u" + strconv.FormatInt(k.Filters.UserID, 10)
	case k.APIKeyID > 0:
		return "k" + strconv.FormatInt(k.APIKeyID, 10)
	case k.Filters != nil && k.Filters.APIKeyID > 0:
		return "k" + strconv.FormatInt(k.Filters.APIKeyID, 10)
	case len(k.APIKeyIDs) > 0:
		return "k" + strconv.FormatInt(slices.Min(k.APIKeyIDs), 10)
	}
	return ""
}

// endUserScoped reports whether the aggregate is one user's or one API key's, i.e. something an end user
// can trigger by opening a page.
func (k usageStatsCacheKey) endUserScoped() bool {
	return k.gateOwner() != ""
}

// keyWindowAggregate reports whether the aggregate is one API key's multi-day answer: the class that
// keyAggregateTTL (or, for the closed days of a split by-day trend or of a user's dashboard,
// usageStatsKeyClosedDaysTTL) gives a TTL, and the only one with the long scan budget and the long memory of a timeout.
func (k usageStatsCacheKey) keyWindowAggregate() bool { return k.TTL > 0 }

// loadTimeout is the database budget of the aggregate.
func (k usageStatsCacheKey) loadTimeout() time.Duration {
	if k.keyWindowAggregate() {
		return usageStatsKeyWindowLoadTimeout
	}
	return usageStatsCacheLoadTimeout
}

// timeoutMemory is how long the aggregate is remembered as too slow after it ran out of time.
func (k usageStatsCacheKey) timeoutMemory() time.Duration {
	if k.keyWindowAggregate() {
		return usageStatsKeyWindowTimeoutTTL
	}
	return usageStatsCacheTimeoutTTL
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
// database path. The database budget of the shared load is the usual 15 seconds,
// or 28 for one API key's multi-day aggregate (usageStatsCacheKey.loadTimeout).
func loadUsageStatsCached[T any](
	cache *usageStatsCache,
	ctx context.Context,
	key usageStatsCacheKey,
	load func(context.Context) (T, error),
) (T, error) {
	return loadUsageStatsCachedWithTimeout(cache, ctx, key, key.loadTimeout(), load)
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
		if owner := key.gateOwner(); owner != "" {
			release, err := cache.gate.acquire(base, owner)
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
			cache.rememberFailure(redisKey, loadErr, loadCtx.Err(), key.timeoutMemory())
			return nil, loadErr
		}
		// Publish bytes, not shared pointers: user handlers redact admin-only
		// fields in place, and each waiter must own its result.
		raw, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			return usageStatsCacheResult[T]{value: value}, nil
		}
		// base, not loadCtx: a scan that used up its budget still produced a good answer worth keeping.
		cache.set(base, redisKey, raw, key.TTL)
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
//   - a scan that ran out of time is remembered for timeoutTTL, longer than other errors, because the same scan
//     will run out again. That is our own load budget (budgetErr, the load context's error: lib/pq reports it
//     as the server's "canceling statement due to user request", so the driver's text cannot tell it apart), a
//     deadline from further down, or the server's own statement_timeout;
//   - a statement somebody stopped is not remembered at all: the shared load is detached from every caller, so
//     an operator's pg_cancel_backend, a migration's reader canceller ("due to user request") or a shutdown
//     stopped it, and that says nothing about the next attempt: once the database is back the same question
//     must be asked again instead of replaying the error for 30 seconds.
func (c *usageStatsCache) rememberFailure(key string, err, budgetErr error, timeoutTTL time.Duration) {
	switch {
	case errors.Is(budgetErr, context.DeadlineExceeded),
		errors.Is(err, context.DeadlineExceeded),
		usagestats.IsStatementTimeout(err):
		c.failures.Set(key, err, timeoutTTL)
	case errors.Is(err, context.Canceled), usagestats.IsStatementCanceledByRequest(err):
		return
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

// set stores one answer for max(the cache's TTL, ttl): a longer TTL asked for by the caller wins, a shorter
// one never cuts into what the operator configured.
func (c *usageStatsCache) set(ctx context.Context, key string, raw []byte, ttl time.Duration) {
	if c.paused() || ctx.Err() != nil {
		return
	}
	opCtx, cancel := context.WithTimeout(ctx, usageStatsCacheOpTimeout)
	defer cancel()
	if err := c.rdb.Set(opCtx, key, raw, max(c.ttl, ttl)).Err(); err != nil && ctx.Err() == nil {
		c.pause(err)
	}
}
