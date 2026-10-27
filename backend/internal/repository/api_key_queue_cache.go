// Adapted from ranxi2001/sub2api @ 9620d315, LGPL-3.0-or-later.
package repository

import (
	"context"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
	"time"
)

const (
	apiKeyWaitKeyPrefix            = "concurrency:wait:api_key:"
	apiKeyWaitClosedKeyPrefix      = "concurrency:wait_closed:api_key:"
	apiKeyQueueCleanupGraceSeconds = 5
)

func apiKeyWaitKey(apiKeyID int64) string {
	return fmt.Sprintf("%s%d", apiKeyWaitKeyPrefix, apiKeyID)
}

func apiKeyWaitClosedKey(apiKeyID int64) string {
	return fmt.Sprintf("%s%d", apiKeyWaitClosedKeyPrefix, apiKeyID)
}

// liveTransferFenceKey fences aborted Live handoffs: member=leaseID,
// score=milliseconds until which a late transfer command must be refused.

var acquireAPIKeySlotScript = redis.NewScript(`
	redis.replicate_commands()
	local t = redis.call('TIME')
	local nowMs = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
	local now = math.floor(nowMs / 1000)
	local ttl = tonumber(ARGV[1])
	local member = ARGV[3]
	local limit = tonumber(ARGV[4])
	redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', now - ttl)
	redis.call('ZREMRANGEBYSCORE', KEYS[2], '-inf', now - tonumber(ARGV[2]))
	if redis.call('ZSCORE', KEYS[1], member) ~= false then
		return {1, nowMs}
	end
	if redis.call('ZSCORE', KEYS[4], member) ~= false then
		return {4, nowMs}
	end
	-- A late fast attempt must not bypass the queue state machine.
	if redis.call('ZSCORE', KEYS[3], member) ~= false then
		return {0, nowMs}
	end
	local count = redis.call('ZCARD', KEYS[1]) + redis.call('ZCARD', KEYS[2])
	if limit > 0 and count >= limit then
		return {0, nowMs}
	end
	redis.call('ZADD', KEYS[1], now, member)
	redis.call('EXPIRE', KEYS[1], ttl)
	return {1, nowMs}
`)

func (c *concurrencyCache) AcquireAPIKeySlot(ctx context.Context, apiKeyID int64, maxConcurrency int, requestID string) (bool, error) {
	acquired, _, err := c.AcquireAPIKeySlotWithTime(ctx, apiKeyID, maxConcurrency, requestID)
	return acquired, err
}

func (c *concurrencyCache) AcquireAPIKeySlotWithTime(ctx context.Context, apiKeyID int64, maxConcurrency int, requestID string) (bool, int64, error) {
	code, nowMs, err := runScriptInt64Pair(
		ctx,
		c.rdb,
		acquireAPIKeySlotScript,
		[]string{apiKeySlotKey(apiKeyID), liveAPIKeySlotKey(apiKeyID), apiKeyWaitKey(apiKeyID), apiKeyWaitClosedKey(apiKeyID)},
		c.slotTTLSeconds,
		liveLeaseTTLSeconds,
		requestID,
		maxConcurrency,
	)
	if err != nil {
		return false, 0, err
	}
	return code == int64(service.APIKeyQueueOutcomeAcquired), nowMs, nil
}

// advanceAPIKeyQueueScript is the atomic ENTER/POLL state machine. All time
// checks use Redis TIME; the attempt's deadline is fixed by the caller and
// never extended here.
//
// KEYS: 1=regular, 2=live, 3=waiting, 4=closed
// ARGV: 1=mode(1=ENTER,2=POLL), 2=attemptID, 3=fixedDeadlineMs, 4=keyLimit,
//
//	5=maxWaiting, 6=slotTTLSeconds, 7=liveTTLSeconds, 8=cleanupGraceSeconds
var advanceAPIKeyQueueScript = redis.NewScript(`
	redis.replicate_commands()
	local t = redis.call('TIME')
	local nowMs = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
	local now = math.floor(nowMs / 1000)
	local mode = tonumber(ARGV[1])
	local attemptID = ARGV[2]
	local deadlineMs = tonumber(ARGV[3])
	local limit = tonumber(ARGV[4])
	local maxWaiting = tonumber(ARGV[5])
	local ttl = tonumber(ARGV[6])
	local grace = tonumber(ARGV[8])

	redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', now - ttl)
	redis.call('ZREMRANGEBYSCORE', KEYS[2], '-inf', now - tonumber(ARGV[7]))
	redis.call('ZREMRANGEBYSCORE', KEYS[3], '-inf', nowMs)
	redis.call('ZREMRANGEBYSCORE', KEYS[4], '-inf', nowMs)

	if nowMs >= deadlineMs then
		redis.call('ZREM', KEYS[3], attemptID)
		return 3
	end
	if limit <= 0 then
		return 6
	end
	if redis.call('ZSCORE', KEYS[1], attemptID) ~= false then
		redis.call('ZREM', KEYS[3], attemptID)
		return 1
	end
	if redis.call('ZSCORE', KEYS[4], attemptID) ~= false then
		return 4
	end
	local queued = redis.call('ZSCORE', KEYS[3], attemptID)
	if queued ~= false and tonumber(queued) ~= deadlineMs then
		return 5
	end
	if mode == 2 and queued == false then
		return 5
	end
	local active = redis.call('ZCARD', KEYS[1]) + redis.call('ZCARD', KEYS[2])
	if active < limit then
		redis.call('ZADD', KEYS[1], now, attemptID)
		redis.call('EXPIRE', KEYS[1], ttl)
		redis.call('ZREM', KEYS[3], attemptID)
		redis.call('ZADD', KEYS[4], deadlineMs, attemptID)
		-- Keep every still-valid fence alive: the key TTL must cover the
		-- furthest member deadline, not just this attempt's.
		local maxClosed = redis.call('ZREVRANGE', KEYS[4], 0, 0, 'WITHSCORES')
		local closedUntil = deadlineMs
		if maxClosed[2] ~= nil then closedUntil = tonumber(maxClosed[2]) end
		local closedTTL = math.ceil((closedUntil - nowMs) / 1000) + grace
		if closedTTL < 1 then closedTTL = 1 end
		redis.call('EXPIRE', KEYS[4], closedTTL)
		return 1
	end
	if maxWaiting == 0 then
		return 8
	end
	if queued == false then
		if redis.call('ZCARD', KEYS[3]) >= maxWaiting then
			return 7
		end
		redis.call('ZADD', KEYS[3], deadlineMs, attemptID)
	end
	local maxMember = redis.call('ZREVRANGE', KEYS[3], 0, 0, 'WITHSCORES')
	if maxMember[2] ~= nil then
		local waitTTL = math.ceil((tonumber(maxMember[2]) - nowMs) / 1000) + grace
		if waitTTL < 1 then waitTTL = 1 end
		redis.call('EXPIRE', KEYS[3], waitTTL)
	end
	return 2
`)

// abortAPIKeyQueueScript fences late ENTER/POLL commands and removes this exact
// attempt from waiting and regular members. It never touches other attempts.
// removeRegular=0 is used after a confirmed ACQUIRED: the waiting stage is
// closed, but the regular member belongs to the new reservation.
//
// KEYS: 1=regular, 2=waiting, 3=closed
// ARGV: 1=attemptID, 2=fixedDeadlineMs, 3=cleanupGraceSeconds, 4=removeRegular(0/1)
var abortAPIKeyQueueScript = redis.NewScript(`
	redis.replicate_commands()
	local t = redis.call('TIME')
	local nowMs = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
	local attemptID = ARGV[1]
	local deadlineMs = tonumber(ARGV[2])
	if nowMs < deadlineMs then
		redis.call('ZADD', KEYS[3], deadlineMs, attemptID)
		local maxClosed = redis.call('ZREVRANGE', KEYS[3], 0, 0, 'WITHSCORES')
		local closedUntil = deadlineMs
		if maxClosed[2] ~= nil then closedUntil = tonumber(maxClosed[2]) end
		local closedTTL = math.ceil((closedUntil - nowMs) / 1000) + tonumber(ARGV[3])
		if closedTTL < 1 then closedTTL = 1 end
		redis.call('EXPIRE', KEYS[3], closedTTL)
	end
	redis.call('ZREM', KEYS[2], attemptID)
	if tonumber(ARGV[4]) == 1 then
		redis.call('ZREM', KEYS[1], attemptID)
	end
	return 1
`)

// apiKeyQueueStatsScript reads active and waiting counts after pruning expired
// members with Redis TIME, so a transfer cannot be shown as both waiting and
// active.
//
// KEYS: 1=regular, 2=live, 3=waiting, 4=closed
// ARGV: 1=slotTTLSeconds, 2=liveTTLSeconds
var apiKeyQueueStatsScript = redis.NewScript(`
	redis.replicate_commands()
	local t = redis.call('TIME')
	local nowMs = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
	local now = math.floor(nowMs / 1000)
	redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', now - tonumber(ARGV[1]))
	redis.call('ZREMRANGEBYSCORE', KEYS[2], '-inf', now - tonumber(ARGV[2]))
	redis.call('ZREMRANGEBYSCORE', KEYS[3], '-inf', nowMs)
	redis.call('ZREMRANGEBYSCORE', KEYS[4], '-inf', nowMs)
	return {redis.call('ZCARD', KEYS[1]) + redis.call('ZCARD', KEYS[2]), redis.call('ZCARD', KEYS[3])}
`)

func (c *concurrencyCache) AdvanceAPIKeyQueue(ctx context.Context, apiKeyID int64, requestID string, mode service.APIKeyQueueMode, deadlineMs int64, keyLimit int, maxWaiting int) (service.APIKeyQueueOutcome, error) {
	code, err := advanceAPIKeyQueueScript.Run(
		ctx,
		c.rdb,
		[]string{apiKeySlotKey(apiKeyID), liveAPIKeySlotKey(apiKeyID), apiKeyWaitKey(apiKeyID), apiKeyWaitClosedKey(apiKeyID)},
		int(mode),
		requestID,
		deadlineMs,
		keyLimit,
		maxWaiting,
		c.slotTTLSeconds,
		liveLeaseTTLSeconds,
		apiKeyQueueCleanupGraceSeconds,
	).Int()
	if err != nil {
		return 0, err
	}
	return service.APIKeyQueueOutcome(code), nil
}

func (c *concurrencyCache) AbortAPIKeyQueueAttempt(ctx context.Context, apiKeyID int64, requestID string, deadlineMs int64, removeRegular bool) error {
	remove := 0
	if removeRegular {
		remove = 1
	}
	return abortAPIKeyQueueScript.Run(
		ctx,
		c.rdb,
		[]string{apiKeySlotKey(apiKeyID), apiKeyWaitKey(apiKeyID), apiKeyWaitClosedKey(apiKeyID)},
		requestID,
		deadlineMs,
		apiKeyQueueCleanupGraceSeconds,
		remove,
	).Err()
}

func (c *concurrencyCache) GetAPIKeyQueueStats(ctx context.Context, apiKeyID int64) (int, int, error) {
	active, waiting, err := runScriptInt64Pair(
		ctx,
		c.rdb,
		apiKeyQueueStatsScript,
		[]string{apiKeySlotKey(apiKeyID), liveAPIKeySlotKey(apiKeyID), apiKeyWaitKey(apiKeyID), apiKeyWaitClosedKey(apiKeyID)},
		c.slotTTLSeconds,
		liveLeaseTTLSeconds,
	)
	if err != nil {
		return 0, 0, err
	}
	return int(active), int(waiting), nil
}

// Each script retains an atomic per-key snapshot; pipelining only removes the
// round trip between keys. Retry NOSCRIPT commands alone after a cache flush.
func (c *concurrencyCache) GetAPIKeyQueueStatsBatch(ctx context.Context, apiKeyIDs []int64) (map[int64]service.APIKeyQueueCounts, error) {
	result := make(map[int64]service.APIKeyQueueCounts, len(apiKeyIDs))
	if len(apiKeyIDs) == 0 {
		return result, nil
	}
	pipe := c.rdb.Pipeline()
	cmds := make([]*redis.Cmd, len(apiKeyIDs))
	for i, id := range apiKeyIDs {
		cmds[i] = apiKeyQueueStatsScript.EvalSha(ctx, pipe,
			[]string{apiKeySlotKey(id), liveAPIKeySlotKey(id), apiKeyWaitKey(id), apiKeyWaitClosedKey(id)},
			c.slotTTLSeconds, liveLeaseTTLSeconds)
	}
	if _, err := pipe.Exec(ctx); err != nil && !redis.HasErrorPrefix(err, "NOSCRIPT") {
		return nil, fmt.Errorf("read API key queue statistics: %w", err)
	}
	for i, cmd := range cmds {
		if redis.HasErrorPrefix(cmd.Err(), "NOSCRIPT") {
			id := apiKeyIDs[i]
			cmds[i] = apiKeyQueueStatsScript.Eval(ctx, pipe,
				[]string{apiKeySlotKey(id), liveAPIKeySlotKey(id), apiKeyWaitKey(id), apiKeyWaitClosedKey(id)},
				c.slotTTLSeconds, liveLeaseTTLSeconds)
		} else if err := cmd.Err(); err != nil {
			return nil, fmt.Errorf("read API key %d queue statistics: %w", apiKeyIDs[i], err)
		}
	}
	if pipe.Len() > 0 {
		if _, err := pipe.Exec(ctx); err != nil {
			return nil, fmt.Errorf("reload API key queue statistics script: %w", err)
		}
	}
	for i, cmd := range cmds {
		raw, err := cmd.Result()
		if err != nil {
			return nil, fmt.Errorf("read API key %d queue statistics: %w", apiKeyIDs[i], err)
		}
		active, err := redisScriptInt64At(raw, 0)
		if err != nil {
			return nil, fmt.Errorf("parse API key %d active count: %w", apiKeyIDs[i], err)
		}
		waiting, err := redisScriptInt64At(raw, 1)
		if err != nil {
			return nil, fmt.Errorf("parse API key %d waiting count: %w", apiKeyIDs[i], err)
		}
		result[apiKeyIDs[i]] = service.APIKeyQueueCounts{Active: int(active), Waiting: int(waiting)}
	}
	return result, nil
}

func (c *concurrencyCache) APIKeySlotRefreshInterval() time.Duration {
	return time.Duration(c.slotTTLSeconds) * time.Second / 3
}

func (c *concurrencyCache) APIKeySlotTTL() time.Duration {
	return time.Duration(c.slotTTLSeconds) * time.Second
}

// Refresh only an existing member, atomically with its key TTL. In particular,
// admin deletion must not be undone by a late heartbeat.
var refreshAPIKeySlotScript = redis.NewScript(`
	redis.replicate_commands()
	if redis.call('ZSCORE', KEYS[1], ARGV[2]) == false then
		return 0
	end
	local now = redis.call('TIME')
	redis.call('ZADD', KEYS[1], 'XX', tonumber(now[1]), ARGV[2])
	redis.call('EXPIRE', KEYS[1], tonumber(ARGV[1]))
	return 1
`)

func (c *concurrencyCache) RefreshAPIKeySlot(ctx context.Context, apiKeyID int64, requestID string) (bool, error) {
	n, err := refreshAPIKeySlotScript.Run(ctx, c.rdb, []string{apiKeySlotKey(apiKeyID)}, c.slotTTLSeconds, requestID).Int()
	return n == 1, err
}
