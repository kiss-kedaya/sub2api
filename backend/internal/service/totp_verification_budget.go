package service

import (
	"context"
	"sync/atomic"
	"time"
)

type accountTOTPAttemptKey struct{}

type accountTOTPAttempt struct {
	userID int64
	used   atomic.Bool
}

// ReserveAccountTOTPAttempt attaches an in-process reservation only after the
// account limiter admits an attempt. Pass the returned context to exactly one
// verification for that account; other callers continue using the legacy budget.
func ReserveAccountTOTPAttempt(ctx context.Context, userID int64, check func(context.Context, int64) (bool, time.Duration, error)) (context.Context, bool, time.Duration, error) {
	if check == nil || userID <= 0 {
		return ctx, false, 0, ErrTotpUnavailable
	}
	allowed, retry, err := check(ctx, userID)
	if err != nil || !allowed {
		return ctx, false, retry, err
	}
	return context.WithValue(ctx, accountTOTPAttemptKey{}, &accountTOTPAttempt{userID: userID}), true, retry, nil
}
