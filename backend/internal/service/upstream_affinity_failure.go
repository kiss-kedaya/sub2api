package service

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

// Failed affinity is a preference, never authorization or global account health.
// Keep it shared across gateway processes so the next request can use another node.
type upstreamFailureCache interface {
	InvalidateFailedSession(context.Context, int64, int64, string, int64) error
	FailedUserAccounts(context.Context, int64, int64) ([]int64, error)
}

type upstreamAttemptContextKey struct{}
type upstreamAttempts struct {
	mu           sync.Mutex
	failed       map[int64]bool // value: eligible for one fallback after alternatives exhaust
	fallbackUsed bool
	historical   map[int64][]int64
	deadlines    map[int64]time.Time
}

func withUpstreamAttempts(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Value(upstreamAttemptContextKey{}).(*upstreamAttempts); ok {
		return ctx
	}
	return context.WithValue(ctx, upstreamAttemptContextKey{}, &upstreamAttempts{failed: make(map[int64]bool), historical: make(map[int64][]int64), deadlines: make(map[int64]time.Time)})
}

// PreferAlternativeUpstream disables eager in-place retries in production loops.
// Selection performs the bounded fallback only after all unused candidates exhaust.
func PreferAlternativeUpstream(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	_, ok := ctx.Value(upstreamAttemptContextKey{}).(*upstreamAttempts)
	return ok
}

func invalidateUpstreamAffinity(ctx context.Context, cache GatewayCache, groupID *int64, keys []string, account *Account, err error) {
	invalidateUpstreamAffinityWithCircuit(ctx, cache, groupID, keys, account, err, false)
}

// invalidateUpstreamAffinityWithCircuit 清理失败账号的会话粘连，并记下「本请求里这个账号失败过」。
// circuitOpen 为 true（账号刚被错误率断路器停掉）时，该账号不再具备「选号层同账号再试一次」的资格：
// 否则一个已经被摘掉的账号会在候选耗尽后被兜底逻辑重新选中（快照还没来得及更新的那几毫秒里）。
func invalidateUpstreamAffinityWithCircuit(ctx context.Context, cache GatewayCache, groupID *int64, keys []string, account *Account, err error, circuitOpen bool) {
	if account == nil || err == nil || ctx == nil {
		return
	}
	// Local policy, client balance hold abort, and a canceled client are not an upstream failure.
	var policyErr *BetaBlockedError
	if errors.As(err, &policyErr) || isBalanceHoldTopUpError(err) || (errors.Is(err, context.Canceled) && ctx.Err() != nil) {
		return
	}
	// Neither is a provider that went silent on this one request.
	var waitErr *UpstreamFailoverError
	if errors.As(err, &waitErr) && waitErr.IsUpstreamWaitLimit() {
		return
	}
	if state, ok := ctx.Value(upstreamAttemptContextKey{}).(*upstreamAttempts); ok {
		var upstreamErr *UpstreamFailoverError
		retry := errors.As(err, &upstreamErr) && upstreamErr.ShouldRetryNextAccount() &&
			upstreamErr.RetryableOnSameAccount && account.GetPoolModeRetryCount() > 0 && !circuitOpen
		state.mu.Lock()
		state.failed[account.ID] = retry
		if retry {
			state.deadlines[account.ID] = upstreamErr.SameAccountRetryDeadline
		}
		state.mu.Unlock()
	}
	if cache == nil {
		return
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	uid, _ := ctx.Value(ctxkey.UserID).(int64)
	for _, key := range keys {
		if shared, ok := cache.(upstreamFailureCache); ok {
			_ = shared.InvalidateFailedSession(cleanup, derefGroupID(groupID), uid, key, account.ID)
		} else if key != "" {
			// Compatibility with non-Redis caches. Redis uses atomic compare/delete.
			if bound, e := cache.GetSessionAccountID(cleanup, derefGroupID(groupID), key); e == nil && bound == account.ID {
				_ = cache.DeleteSessionAccountID(cleanup, derefGroupID(groupID), key)
			}
		}
	}
}

func (s *GatewayService) InvalidateUpstreamAffinity(ctx context.Context, groupID *int64, session string, account *Account, err error) {
	if s != nil {
		invalidateUpstreamAffinityWithCircuit(ctx, s.cache, groupID, []string{session}, account, err, account != nil && s.rateLimitService.AccountCircuitOpen(account.ID))
	}
}

func (s *OpenAIGatewayService) InvalidateUpstreamAffinity(ctx context.Context, groupID *int64, session string, account *Account, err error) {
	if s == nil {
		return
	}
	keys := []string{s.openAISessionCacheKey(session)}
	if legacy := s.openAILegacySessionCacheKey(ctx, session); legacy != "" {
		keys = append(keys, legacy)
	}
	invalidateUpstreamAffinityWithCircuit(ctx, s.cache, groupID, keys, account, err, account != nil && s.rateLimitService.AccountCircuitOpen(account.ID))
}

// selectWithUpstreamPreferences preserves all caller exclusions (profit veto,
// busy slots, failed attempts). A second pass relaxes only historical preference;
// a final pass permits at most one explicitly retryable failure for this request.
func selectWithUpstreamPreferences(ctx context.Context, cache GatewayCache, key *APIKey, excluded map[int64]struct{}, selectOne func(map[int64]struct{}) (*AccountSelectionResult, OpenAIAccountScheduleDecision, *APIKey, error)) (*AccountSelectionResult, OpenAIAccountScheduleDecision, *APIKey, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	strict := make(map[int64]struct{}, len(excluded))
	for id := range excluded {
		strict[id] = struct{}{}
	}
	state, _ := ctx.Value(upstreamAttemptContextKey{}).(*upstreamAttempts)
	if state != nil {
		state.mu.Lock()
		for id := range state.failed {
			strict[id] = struct{}{}
		}
		state.mu.Unlock()
	}
	preferred := make(map[int64]struct{}, len(strict))
	for id := range strict {
		preferred[id] = struct{}{}
	}
	if shared, ok := cache.(upstreamFailureCache); ok && key != nil {
		uid, _ := ctx.Value(ctxkey.UserID).(int64)
		if uid > 0 {
			for _, gid := range key.CandidateGroupIDs() {
				var ids []int64
				if state != nil {
					state.mu.Lock()
					var cached bool
					ids, cached = state.historical[gid]
					if !cached {
						readCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
						ids, _ = shared.FailedUserAccounts(readCtx, gid, uid)
						cancel()
						state.historical[gid] = ids
					}
					state.mu.Unlock()
				} else {
					ids, _ = shared.FailedUserAccounts(ctx, gid, uid)
				}
				for _, id := range ids {
					preferred[id] = struct{}{}
				}
			}
		}
	}
	result, decision, routed, err := selectOne(preferred)
	if err == nil || !shouldContinueAlongKeyRoutes(err) {
		return result, decision, routed, err
	}
	if len(preferred) > len(strict) {
		result, decision, routed, err = selectOne(strict)
		if err == nil || !shouldContinueAlongKeyRoutes(err) {
			return result, decision, routed, err
		}
	}
	if state == nil {
		return result, decision, routed, err
	}
	state.mu.Lock()
	canRetry := false
	if !state.fallbackUsed {
		for id, retry := range state.failed {
			deadline := state.deadlines[id]
			if retry && (deadline.IsZero() || time.Now().Before(deadline)) {
				delete(strict, id)
				canRetry = true
			}
		}
		if canRetry {
			state.fallbackUsed = true
		}
	}
	state.mu.Unlock()
	if canRetry {
		// Reserve the request's single fallback without holding mu across selection.
		// A cooldown-only rejection must leave that reservation for the bypass pass.
		if ctx.Err() == nil {
			result, decision, routed, err = selectOne(strict)
		}
		if err != nil || result == nil || result.Account == nil {
			state.mu.Lock()
			state.fallbackUsed = false
			state.mu.Unlock()
		}
	}
	return result, decision, routed, err
}
