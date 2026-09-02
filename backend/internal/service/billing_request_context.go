package service

import (
	"context"
	"sync"
)

type userPlatformQuotaRequestKey struct {
	userID   int64
	platform string
}

type userPlatformQuotaRequestState struct {
	mu     sync.RWMutex
	limits map[userPlatformQuotaRequestKey]bool
}

type userPlatformQuotaRequestContextKey struct{}

// withUserPlatformQuotaRequestContext installs a small request-scoped state
// used to carry the result of the quota preflight into post-usage billing.
// It stores only the fail-safe "has any limit" decision, never usage amounts,
// so it cannot become a second mutable quota source.
func withUserPlatformQuotaRequestContext(ctx context.Context) context.Context {
	if ctx == nil {
		return nil
	}
	if _, ok := ctx.Value(userPlatformQuotaRequestContextKey{}).(*userPlatformQuotaRequestState); ok {
		return ctx
	}
	return context.WithValue(ctx, userPlatformQuotaRequestContextKey{}, &userPlatformQuotaRequestState{
		limits: make(map[userPlatformQuotaRequestKey]bool),
	})
}

func userPlatformQuotaRequestStateFromContext(ctx context.Context) *userPlatformQuotaRequestState {
	if ctx == nil {
		return nil
	}
	state, _ := ctx.Value(userPlatformQuotaRequestContextKey{}).(*userPlatformQuotaRequestState)
	return state
}

func (s *userPlatformQuotaRequestState) rememberLimit(userID int64, platform string, hasLimit bool) {
	if s == nil || userID <= 0 || platform == "" {
		return
	}
	s.mu.Lock()
	if s.limits == nil {
		s.limits = make(map[userPlatformQuotaRequestKey]bool)
	}
	s.limits[userPlatformQuotaRequestKey{userID: userID, platform: platform}] = hasLimit
	s.mu.Unlock()
}

func (s *userPlatformQuotaRequestState) limit(userID int64, platform string) (bool, bool) {
	if s == nil || userID <= 0 || platform == "" {
		return false, false
	}
	s.mu.RLock()
	hasLimit, ok := s.limits[userPlatformQuotaRequestKey{userID: userID, platform: platform}]
	s.mu.RUnlock()
	return hasLimit, ok
}
