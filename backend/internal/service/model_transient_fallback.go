package service

import (
	"context"
	"errors"
	"sync/atomic"
)

type modelTransientFallbackContextKey struct{}

type modelTransientFallbackProbe struct {
	blocked atomic.Bool
	bypass  bool
}

// Only the outermost selector retries, after all normal candidates (including
// smart-route groups) have exhausted. Every other admission check still runs.
func withModelTransientFallback[T any](ctx context.Context, selectAccount func(context.Context) (T, error), additionalNoAvailableErrors ...error) (T, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Value(modelTransientFallbackContextKey{}) != nil {
		return selectAccount(ctx)
	}
	probe := &modelTransientFallbackProbe{}
	result, err := selectAccount(context.WithValue(ctx, modelTransientFallbackContextKey{}, probe))
	retryable := errors.Is(err, ErrNoAvailableAccounts)
	for _, unavailable := range additionalNoAvailableErrors {
		retryable = retryable || errors.Is(err, unavailable)
	}
	if !retryable || !probe.blocked.Load() {
		return result, err
	}
	retried, retryErr := selectAccount(context.WithValue(ctx, modelTransientFallbackContextKey{}, &modelTransientFallbackProbe{bypass: true}))
	// Extra error families are opt-in (OpenAI compact). Preserve their original
	// semantics if the retry still has no capacity, but never hide a new error.
	for _, unavailable := range additionalNoAvailableErrors {
		if errors.Is(err, unavailable) && (errors.Is(retryErr, ErrNoAvailableAccounts) || errors.Is(retryErr, unavailable)) {
			return retried, err
		}
	}
	return retried, retryErr
}
