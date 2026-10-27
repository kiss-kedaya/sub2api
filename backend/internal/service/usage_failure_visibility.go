package service

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

// Usage failure metadata is hydrated only when a page/detail is read. It is
// deliberately separate from billing writes and aggregate spend statistics.
type usageFailureHydrator interface {
	HydrateUsageLogFailures(context.Context, []UsageLog) error
}

var usageFailureLookupLastLog atomic.Int64

func (s *UsageService) hydrateUsageFailures(ctx context.Context, logs []UsageLog) {
	loader, ok := s.usageRepo.(usageFailureHydrator)
	if !ok || len(logs) == 0 {
		return
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := loader.HydrateUsageLogFailures(lookupCtx, logs); err != nil {
		// Ops retention or a transient lookup failure must not hide an otherwise
		// readable bill. Clear any partially hydrated page; no badge claims success.
		for i := range logs {
			logs[i].FailureStatusCode = nil
		}
		now := time.Now().UnixNano()
		last := usageFailureLookupLastLog.Load()
		if now-last >= int64(time.Minute) && usageFailureLookupLastLog.CompareAndSwap(last, now) {
			logger.LegacyPrintf("service.usage", "Warning: usage delivery outcomes unavailable; retaining billing page with unknown outcomes: %v", err)
		}
	}
}
