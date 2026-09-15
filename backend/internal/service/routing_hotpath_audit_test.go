//go:build unit

package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type routingHotpathLoadCache struct {
	ConcurrencyCache
	load func(context.Context, []AccountWithConcurrency) (map[int64]*AccountLoadInfo, error)
}

func (c *routingHotpathLoadCache) GetAccountsLoadBatch(ctx context.Context, accounts []AccountWithConcurrency) (map[int64]*AccountLoadInfo, error) {
	return c.load(ctx, accounts)
}

func TestRoutingHotpathAudit_UnsharedLoadHonorsCancellation(t *testing.T) {
	for _, fresh := range []bool{false, true} {
		for _, deadline := range []bool{false, true} {
			name := "cache_disabled"
			if fresh {
				name = "fresh"
			}
			if deadline {
				name += "/deadline"
			} else {
				name += "/cancel"
			}
			t.Run(name, func(t *testing.T) {
				started := make(chan context.Context, 1)
				release := make(chan struct{})
				cache := &routingHotpathLoadCache{load: func(ctx context.Context, _ []AccountWithConcurrency) (map[int64]*AccountLoadInfo, error) {
					started <- ctx
					select {
					case <-ctx.Done():
						return nil, ctx.Err()
					case <-release:
						return map[int64]*AccountLoadInfo{}, nil
					}
				}}
				svc := NewConcurrencyService(cache)
				if !fresh {
					svc.SetAccountLoadBatchCacheTTL(0)
				}
				ctx, cancel := context.WithCancel(context.Background())
				wantErr := context.Canceled
				if deadline {
					cancel()
					ctx, cancel = context.WithTimeout(context.Background(), 100*time.Millisecond)
					wantErr = context.DeadlineExceeded
				}
				defer cancel()
				done := make(chan error, 1)
				go func() {
					accounts := []AccountWithConcurrency{{ID: 42, MaxConcurrency: 3}}
					var err error
					if fresh {
						_, err = svc.GetAccountsLoadBatchFresh(ctx, accounts)
					} else {
						_, err = svc.GetAccountsLoadBatch(ctx, accounts)
					}
					done <- err
				}()
				defer func() {
					close(release)
					select {
					case <-done:
					case <-time.After(time.Second):
						t.Error("load did not exit after cleanup")
					}
				}()
				var fetchCtx context.Context
				select {
				case fetchCtx = <-started:
				case <-time.After(time.Second):
					t.Fatal("load did not start")
				}
				_, bounded := fetchCtx.Deadline()
				require.True(t, bounded, "Redis operations must retain an execution limit")
				if !deadline {
					cancel()
				}
				<-ctx.Done()
				select {
				case err := <-done:
					done <- err
					require.ErrorIs(t, err, wantErr)
				case <-time.After(250 * time.Millisecond):
					t.Fatal("unshared load kept waiting after caller cancellation")
				}
			})
		}
	}
}

func TestRoutingHotpathAudit_SharedLoadSurvivesLeaderCancellation(t *testing.T) {
	started := make(chan context.Context, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	var calls atomic.Int32
	cache := &routingHotpathLoadCache{load: func(ctx context.Context, _ []AccountWithConcurrency) (map[int64]*AccountLoadInfo, error) {
		calls.Add(1)
		started <- ctx
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-release:
			return map[int64]*AccountLoadInfo{42: {AccountID: 42, CurrentConcurrency: 2}}, nil
		}
	}}
	svc := NewConcurrencyService(cache)
	svc.SetAccountLoadBatchCacheTTL(5 * time.Second)
	accounts := []AccountWithConcurrency{{ID: 42, MaxConcurrency: 3}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := svc.GetAccountsLoadBatch(ctx, accounts)
		done <- err
	}()
	var fetchCtx context.Context
	select {
	case fetchCtx = <-started:
	case <-time.After(time.Second):
		t.Fatal("shared load did not start")
	}
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("leader caller did not cancel")
	}
	require.NoError(t, fetchCtx.Err(), "shared load must survive its first caller")
	_, bounded := fetchCtx.Deadline()
	require.True(t, bounded)

	// Join the existing flight before releasing it; this proves the result
	// was not discarded and no second backend call was issued.
	joined := svc.accountLoadGroup.DoChan(accountLoadBatchCacheKey(accounts), func() (any, error) {
		return nil, errors.New("shared flight disappeared")
	})
	unblock()
	select {
	case result := <-joined:
		require.NoError(t, result.Err)
	case <-time.After(time.Second):
		t.Fatal("shared load did not complete")
	}
	loaded, err := svc.GetAccountsLoadBatch(context.Background(), accounts)
	require.NoError(t, err)
	require.Equal(t, 2, loaded[42].CurrentConcurrency)
	require.Equal(t, int32(1), calls.Load())
}

func TestRoutingHotpathAudit_LoadFailureIsRetried(t *testing.T) {
	var calls atomic.Int32
	failure := errors.New("temporary load lookup failure")
	svc := NewConcurrencyService(&routingHotpathLoadCache{load: func(context.Context, []AccountWithConcurrency) (map[int64]*AccountLoadInfo, error) {
		if calls.Add(1) == 1 {
			return nil, failure
		}
		return map[int64]*AccountLoadInfo{42: {AccountID: 42, CurrentConcurrency: 1}}, nil
	}})
	accounts := []AccountWithConcurrency{{ID: 42, MaxConcurrency: 3}}
	_, err := svc.GetAccountsLoadBatch(context.Background(), accounts)
	require.ErrorIs(t, err, failure)
	loaded, err := svc.GetAccountsLoadBatch(context.Background(), accounts)
	require.NoError(t, err)
	require.Equal(t, 1, loaded[42].CurrentConcurrency)
	require.Equal(t, int32(2), calls.Load())
}
