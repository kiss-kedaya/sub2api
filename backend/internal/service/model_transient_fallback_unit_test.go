//go:build unit

package service

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

func TestModelTransientFallbackLoadAwareAndSticky(t *testing.T) {
	for _, sticky := range []bool{false, true} {
		for _, allCooling := range []bool{false, true} {
			t.Run(map[bool]string{false: "unbound", true: "sticky"}[sticky]+"/"+map[bool]string{false: "healthy alternative", true: "all cooling"}[allCooling], func(t *testing.T) {
				primary, other := modelTransientFallbackAccount(1, 10), modelTransientFallbackAccount(2, 10)
				primary.AccountGroups = []AccountGroup{{AccountID: 1, GroupID: 10}}
				other.AccountGroups = []AccountGroup{{AccountID: 2, GroupID: 10}}
				svc, _, ctx, group := newModelTransientFallbackFixture(primary, other)
				svc.cfg.Gateway.Scheduling.LoadBatchEnabled = true
				loadCache := &mockConcurrencyCache{}
				svc.concurrencyService = NewConcurrencyService(loadCache)
				session := ""
				if sticky {
					session = "sticky"
				}
				svc.cache = &mockGatewayCacheForPlatform{sessionBindings: map[string]int64{session: primary.ID}}
				recordModelTransientFailures(svc, primary)
				if allCooling {
					recordModelTransientFailures(svc, other)
				}
				selection, err := svc.SelectAccountWithLoadAwareness(ctx, &group.ID, session, "model-a", nil, "", 0)
				require.NoError(t, err)
				t.Cleanup(selection.ReleaseFunc)
				want := int64(2)
				if allCooling {
					want = 1
				}
				require.Equal(t, want, selection.Account.ID)
				if !sticky {
					require.Equal(t, 1, loadCache.loadBatchCalls, "no second load query after success")
				}
			})
		}
	}
}

func TestModelTransientFallbackPreservesSameAccountRetry(t *testing.T) {
	for _, loadAware := range []bool{false, true} {
		for _, cooling := range []bool{false, true} {
			t.Run(fmt.Sprintf("load=%t/cooling=%t", loadAware, cooling), func(t *testing.T) {
				account := modelTransientFallbackAccount(1, 10)
				account.AccountGroups = []AccountGroup{{AccountID: account.ID, GroupID: 10}}
				account.Credentials = map[string]any{"pool_mode": true, "pool_mode_retry_count": 1}
				svc, _, ctx, group := newModelTransientFallbackFixture(account)
				if loadAware {
					svc.cfg.Gateway.Scheduling.LoadBatchEnabled = true
					svc.concurrencyService = NewConcurrencyService(&mockConcurrencyCache{})
				}
				if cooling {
					recordModelTransientFailures(svc, account)
				}
				failure := &UpstreamFailoverError{StatusCode: http.StatusBadGateway, RetryableOnSameAccount: true}
				svc.InvalidateUpstreamAffinity(ctx, &group.ID, "", account, failure)
				key := &APIKey{GroupID: &group.ID, Group: group, User: &User{ID: 1}}
				svc.storeGroupModelsCatalog(&group.ID, groupModelsCatalog{
					byPlatform: map[string][]string{PlatformAnthropic: {"model-a"}},
					platforms:  map[string]struct{}{PlatformAnthropic: {}},
				})
				excluded := map[int64]struct{}{account.ID: {}, 99: {}}
				selection, _, err := svc.SelectAccountAlongKeyRoutes(ctx, key, "", "model-a", excluded, "", 0)
				require.NoError(t, err, "cooldown probing must not consume the approved retry")
				require.Equal(t, account.ID, selection.Account.ID)
				if selection.ReleaseFunc != nil {
					selection.ReleaseFunc()
				}
				_, _, err = svc.SelectAccountAlongKeyRoutes(ctx, key, "", "model-a", excluded, "", 0)
				require.ErrorIs(t, err, ErrNoAvailableAccounts, "only one selected same-account retry is allowed")
				require.Len(t, excluded, 2, "caller exclusions must remain intact")
			})
		}
	}
}

func TestModelTransientFallbackStickyProfitVetoDoesNotRetry(t *testing.T) {
	account := modelTransientFallbackAccount(1, 10)
	rate := 1.0
	account.RateMultiplier = &rate
	svc, cache, ctx, group := newModelTransientFallbackFixture(account)
	svc.cfg.Gateway.Scheduling.LoadBatchEnabled = true
	svc.concurrencyService = NewConcurrencyService(&mockConcurrencyCache{})
	svc.cache = &mockGatewayCacheForPlatform{sessionBindings: map[string]int64{"sticky": account.ID}}
	recordModelTransientFailures(svc, account)
	ctx = context.WithValue(ctx, openAIProfitControlGateCtxKey{}, &openAIProfitControlGate{threshold: 0.5})
	_, err := svc.SelectAccountWithLoadAwareness(ctx, &group.ID, "sticky", "model-a", nil, "", 0)
	require.ErrorIs(t, err, ErrNoAvailableAccounts)
	require.Equal(t, 1, cache.snapshotReads, "profit-vetoed sticky checks are not cooldown admission attempts")
}

func TestModelTransientFallbackGeminiSelection(t *testing.T) {
	account := modelTransientFallbackAccount(1, 10)
	account.Platform = PlatformGemini
	svc, _, _, group := newModelTransientFallbackFixture(account)
	group.Platform = PlatformGemini
	group.UpdatedAt = time.Now()
	svc.schedulerSnapshot.seedCachedGroup(group)
	ctx := svc.PrepareSchedulerRequestContext(context.WithValue(context.Background(), ctxkey.Group, group))
	for range 3 {
		svc.rateLimitService.recordModelTransientOutcome(account, 502, []string{"gemini-test"})
	}
	selection, err := svc.SelectAccountWithLoadAwareness(ctx, &group.ID, "", "gemini-test", nil, "", 0)
	require.NoError(t, err)
	require.Equal(t, account.ID, selection.Account.ID)
}
