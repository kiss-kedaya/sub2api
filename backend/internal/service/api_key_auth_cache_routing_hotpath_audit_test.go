//go:build unit

package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

const routingHotpathAuthKey = "routing-audit-synthetic-key"

func routingHotpathAuthRecord() *APIKey {
	groupID := int64(42)
	return &APIKey{
		ID: 11, UserID: 22, GroupID: &groupID, Status: StatusActive,
		User:  &User{ID: 22, Status: StatusActive},
		Group: &Group{ID: groupID, Platform: PlatformOpenAI, Status: StatusActive},
	}
}

func routingHotpathAuthService(t *testing.T, lookup func(context.Context, string) (*APIKey, error)) *APIKeyService {
	t.Helper()
	svc := NewAPIKeyService(&authRepoStub{getByKeyForAuth: lookup}, nil, nil, nil, nil, nil, &config.Config{
		APIKeyAuth: config.APIKeyAuthCacheConfig{
			Singleflight: true, LookupConcurrency: 1,
			L1Size: 1024, L1TTLSeconds: 60, NegativeTTLSeconds: 30,
		},
	})
	t.Cleanup(func() {
		svc.authCacheL1.Close()
		svc.authNegativeCacheL1.Close()
	})
	return svc
}

type routingHotpathAuthGate struct {
	started chan context.Context
	release chan struct{}
	once    sync.Once
	calls   atomic.Int32
}

func (g *routingHotpathAuthGate) unblock() { g.once.Do(func() { close(g.release) }) }

func (g *routingHotpathAuthGate) lookup(ctx context.Context, _ string) (*APIKey, error) {
	if g.calls.Add(1) != 1 {
		return nil, errors.New("unexpected duplicate auth lookup")
	}
	g.started <- ctx
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-g.release:
		return routingHotpathAuthRecord(), nil
	}
}

// Done is evaluated after the public entry point joins singleflight, so the
// test can release the query without relying on sleeps or goroutine ordering.
type routingHotpathAuthWaitContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *routingHotpathAuthWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

type routingHotpathAuthResult struct {
	key *APIKey
	err error
}

func routingHotpathStartAuth(t *testing.T, svc *APIKeyService, ctx context.Context) <-chan routingHotpathAuthResult {
	t.Helper()
	result := make(chan routingHotpathAuthResult, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		key, err := svc.GetByKey(ctx, routingHotpathAuthKey)
		result <- routingHotpathAuthResult{key, err}
	}()
	t.Cleanup(func() {
		select {
		case <-finished:
		case <-time.After(time.Second):
			t.Error("auth caller did not exit after cleanup")
		}
	})
	return result
}

func routingHotpathAwaitAuth(t *testing.T, result <-chan routingHotpathAuthResult) routingHotpathAuthResult {
	t.Helper()
	select {
	case got := <-result:
		return got
	case <-time.After(250 * time.Millisecond):
		t.Fatal("auth caller remained blocked after cancellation or query release")
		return routingHotpathAuthResult{}
	}
}

// Restored from the archived failing auth-waiter reproduction.
func TestRoutingHotpathAudit_AuthFollowerCancellation(t *testing.T) {
	for _, alreadyCanceled := range []bool{true, false} {
		name := "while_waiting"
		if alreadyCanceled {
			name = "already_canceled"
		}
		t.Run(name, func(t *testing.T) {
			gate := &routingHotpathAuthGate{started: make(chan context.Context, 1), release: make(chan struct{})}
			svc := routingHotpathAuthService(t, gate.lookup)
			leader := routingHotpathStartAuth(t, svc, context.Background())
			defer gate.unblock()
			var lookupCtx context.Context
			select {
			case lookupCtx = <-gate.started:
			case <-time.After(time.Second):
				t.Fatal("auth lookup did not start")
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if alreadyCanceled {
				cancel()
			}
			followerCtx := &routingHotpathAuthWaitContext{Context: ctx, waiting: make(chan struct{})}
			follower := routingHotpathStartAuth(t, svc, followerCtx)
			if !alreadyCanceled {
				select {
				case <-followerCtx.waiting:
				case <-time.After(time.Second):
					t.Fatal("auth follower did not enter cancelable waiting")
				}
				cancel()
			}
			require.ErrorIs(t, routingHotpathAwaitAuth(t, follower).err, context.Canceled)
			require.NoError(t, lookupCtx.Err(), "a canceled follower must not cancel the shared lookup")
			gate.unblock()
			got := routingHotpathAwaitAuth(t, leader)
			require.NoError(t, got.err)
			require.Equal(t, routingHotpathAuthKey, got.key.Key)
			require.Equal(t, int64(42), *got.key.GroupID)
			require.Equal(t, int32(1), gate.calls.Load())
		})
	}
}

func TestRoutingHotpathAudit_AuthLeaderCancellation(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		name := "cancel"
		if deadline {
			name = "deadline"
		}
		t.Run(name, func(t *testing.T) {
			gate := &routingHotpathAuthGate{started: make(chan context.Context, 1), release: make(chan struct{})}
			svc := routingHotpathAuthService(t, gate.lookup)
			type traceKey struct{}
			parent := context.WithValue(context.Background(), traceKey{}, "synthetic-trace")
			ctx, cancel := context.WithCancel(parent)
			wantErr := context.Canceled
			if deadline {
				cancel()
				ctx, cancel = context.WithTimeout(parent, 100*time.Millisecond)
				wantErr = context.DeadlineExceeded
			}
			defer cancel()
			leader := routingHotpathStartAuth(t, svc, ctx)
			defer gate.unblock()
			var lookupCtx context.Context
			select {
			case lookupCtx = <-gate.started:
			case <-time.After(time.Second):
				t.Fatal("auth lookup did not start")
			}
			if !deadline {
				cancel()
			}
			require.ErrorIs(t, routingHotpathAwaitAuth(t, leader).err, wantErr)
			require.NoError(t, lookupCtx.Err(), "the leader caller must not cancel the shared lookup")
			require.Equal(t, "synthetic-trace", lookupCtx.Value(traceKey{}))
			lookupDeadline, bounded := lookupCtx.Deadline()
			require.True(t, bounded, "shared auth lookup must have an independent deadline")
			require.Positive(t, time.Until(lookupDeadline))
			require.LessOrEqual(t, time.Until(lookupDeadline), apiKeyAuthSharedLookupTimeout)

			followerCtx := &routingHotpathAuthWaitContext{Context: context.Background(), waiting: make(chan struct{})}
			follower := routingHotpathStartAuth(t, svc, followerCtx)
			select {
			case <-followerCtx.waiting:
			case <-time.After(time.Second):
				t.Fatal("healthy follower did not join the shared lookup")
			}
			gate.unblock()
			got := routingHotpathAwaitAuth(t, follower)
			require.NoError(t, got.err)
			require.Equal(t, int64(22), got.key.UserID)
			require.Equal(t, int64(42), *got.key.GroupID)
			require.Equal(t, int32(1), gate.calls.Load())
			require.Zero(t, svc.AuthLookupMetrics().InFlight)
		})
	}
}

func TestRoutingHotpathAudit_AuthLookupFailureRetries(t *testing.T) {
	for _, failure := range []error{errors.New("temporary auth database failure"), context.Canceled, context.DeadlineExceeded} {
		t.Run(failure.Error(), func(t *testing.T) {
			var calls atomic.Int32
			svc := routingHotpathAuthService(t, func(context.Context, string) (*APIKey, error) {
				if calls.Add(1) == 1 {
					return nil, failure
				}
				return routingHotpathAuthRecord(), nil
			})
			_, err := svc.GetByKey(context.Background(), routingHotpathAuthKey)
			require.ErrorIs(t, err, failure)
			svc.authNegativeCacheL1.Wait()
			got, err := svc.GetByKey(context.Background(), routingHotpathAuthKey)
			require.NoError(t, err)
			require.Equal(t, int64(11), got.ID)
			svc.authCacheL1.Wait()
			cached, err := svc.GetByKey(context.Background(), routingHotpathAuthKey)
			require.NoError(t, err)
			require.Equal(t, got, cached)
			require.Equal(t, int32(2), calls.Load())
			require.Zero(t, svc.AuthLookupMetrics().InFlight)
		})
	}
}

func TestRoutingHotpathAudit_AuthLookupBudgetExpiresAndRetries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		svc := routingHotpathAuthService(t, func(ctx context.Context, _ string) (*APIKey, error) {
			if calls.Add(1) > 1 {
				return routingHotpathAuthRecord(), nil
			}
			if _, bounded := ctx.Deadline(); !bounded {
				return nil, errors.New("shared auth lookup has no execution deadline")
			}
			<-ctx.Done()
			return nil, ctx.Err()
		})
		started := time.Now()
		_, err := svc.GetByKey(context.Background(), routingHotpathAuthKey)
		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.Equal(t, apiKeyAuthSharedLookupTimeout, time.Since(started))
		require.Zero(t, svc.AuthLookupMetrics().InFlight, "timeout must release the lookup bulkhead")
		svc.authNegativeCacheL1.Wait()
		_, negative := svc.authNegativeCacheL1.Get(svc.authCacheKey(routingHotpathAuthKey))
		require.False(t, negative, "a shared lookup timeout is not an invalid key")
		_, err = svc.GetByKey(context.Background(), routingHotpathAuthKey)
		require.NoError(t, err)
		require.Equal(t, int32(2), calls.Load())
	})
}

func TestRoutingHotpathAudit_AuthErrorSentinels(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		negative bool
	}{
		{"invalid_key", ErrAPIKeyNotFound, true},
		{"lookup_overloaded", ErrAPIKeyAuthOverloaded, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			svc := routingHotpathAuthService(t, func(context.Context, string) (*APIKey, error) {
				calls.Add(1)
				return nil, tc.err
			})
			_, err := svc.GetByKey(context.Background(), routingHotpathAuthKey)
			// Both HTTP middleware variants classify these errors with errors.Is.
			require.ErrorIs(t, err, tc.err)
			svc.authNegativeCacheL1.Wait()
			_, negative := svc.authNegativeCacheL1.Get(svc.authCacheKey(routingHotpathAuthKey))
			require.Equal(t, tc.negative, negative)
			_, err = svc.GetByKey(context.Background(), routingHotpathAuthKey)
			require.ErrorIs(t, err, tc.err)
			wantCalls := int32(2)
			if tc.negative {
				wantCalls = 1
			}
			require.Equal(t, wantCalls, calls.Load())
			require.Zero(t, svc.AuthLookupMetrics().InFlight)
		})
	}
}
