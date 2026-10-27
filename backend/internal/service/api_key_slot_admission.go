// Adapted from ranxi2001/sub2api @ 9620d315, LGPL-3.0-or-later.
package service

import (
	"context"
	"fmt"
	"time"
)

type APIKeySlotAdmissionCache interface {
	APIKeyConcurrencyCache
	AcquireAPIKeySlot(context.Context, int64, int, string) (bool, error)
}

// AcquireAPIKeySlot never waits for capacity. The caller owns ReleaseFunc,
// including after cancellation: WS turns retain their slots while draining.
// HTTP callers bind release to their request context at the helper boundary.
func (s *ConcurrencyService) AcquireAPIKeySlot(ctx context.Context, apiKeyID int64, maxConcurrency int) (*AcquireResult, error) {
	if maxConcurrency == 0 {
		return &AcquireResult{Acquired: true, ReleaseFunc: s.TrackAPIKeySlot(context.WithoutCancel(ctx), apiKeyID)}, nil
	}
	if s == nil || s.cache == nil || apiKeyID <= 0 || maxConcurrency < 0 {
		return nil, fmt.Errorf("API key concurrency admission unavailable")
	}
	cache, ok := s.cache.(APIKeySlotAdmissionCache)
	if !ok {
		return nil, fmt.Errorf("API key concurrency admission unavailable")
	}
	requestID := generateRequestID()
	leaseCache, ok := s.cache.(APIKeySlotLeaseCache)
	owner, ownerOK := apiKeyAdmissionOwnerFromContext(ctx)
	if !ok || leaseCache.APIKeySlotTTL() <= 2*time.Second || leaseCache.APIKeySlotRefreshInterval() <= 0 || !ownerOK {
		return nil, fmt.Errorf("API key concurrency requires a renewable lease and cancellation owner")
	}
	started := time.Now()
	acquireCtx, cancel := context.WithTimeout(ctx, apiKeySlotTrackTimeout)
	acquired, err := cache.AcquireAPIKeySlot(acquireCtx, apiKeyID, maxConcurrency, requestID)
	cancel()
	if err != nil {
		// The write may have succeeded even if its response was lost.
		releaseAPIKeySlot(cache, apiKeyID, requestID)
		return nil, fmt.Errorf("acquire API key %d concurrency slot: %w", apiKeyID, err)
	}
	if !acquired {
		return &AcquireResult{Acquired: false}, nil
	}
	return &AcquireResult{Acquired: true, ReleaseFunc: keepEnforcedAPIKeySlot(owner, leaseCache, apiKeyID, requestID, started)}, nil
}
