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
func withModelTransientFallback[T any](ctx context.Context, selectAccount func(context.Context) (T, error)) (T, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Value(modelTransientFallbackContextKey{}) != nil {
		return selectAccount(ctx)
	}
	probe := &modelTransientFallbackProbe{}
	result, err := selectAccount(context.WithValue(ctx, modelTransientFallbackContextKey{}, probe))
	if !errors.Is(err, ErrNoAvailableAccounts) || !probe.blocked.Load() {
		return result, err
	}
	return selectAccount(context.WithValue(ctx, modelTransientFallbackContextKey{}, &modelTransientFallbackProbe{bypass: true}))
}
