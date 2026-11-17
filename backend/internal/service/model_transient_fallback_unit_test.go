//go:build unit

package service

import (
	"context"
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
