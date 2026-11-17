package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	gocache "github.com/patrickmn/go-cache"
	"github.com/stretchr/testify/require"
)

type modelTransientFallbackCache struct {
	SchedulerCache
	accounts      []*Account
	accountReads  int
	snapshotReads int
	miss          bool
}

func (c *modelTransientFallbackCache) GetSnapshot(_ context.Context, bucket SchedulerBucket) ([]*Account, bool, error) {
	c.snapshotReads++
	if c.miss {
		return nil, false, nil
	}
	var accounts []*Account
	for _, account := range c.accounts {
		if containsInt64(account.GroupIDs, bucket.GroupID) {
			accounts = append(accounts, account)
		}
	}
	return accounts, true, nil
}

func (c *modelTransientFallbackCache) GetAccount(_ context.Context, id int64) (*Account, error) {
	c.accountReads++
	for _, account := range c.accounts {
		if account.ID == id {
			return account, nil
		}
	}
	return nil, nil
}

func modelTransientFallbackAccount(id, groupID int64) *Account {
	return &Account{ID: id, Platform: PlatformAnthropic, Type: AccountTypeAPIKey,
		Status: StatusActive, Schedulable: true, Priority: int(id), GroupIDs: []int64{groupID}}
}

func newModelTransientFallbackFixture(accounts ...*Account) (*GatewayService, *modelTransientFallbackCache, context.Context, *Group) {
	cache := &modelTransientFallbackCache{accounts: accounts}
	cfg := &config.Config{}
	group := &Group{ID: 10, Platform: PlatformAnthropic, Status: StatusActive, Hydrated: true}
	svc := &GatewayService{cfg: cfg,
		schedulerSnapshot: NewSchedulerSnapshotService(cache, nil, nil, nil, cfg),
		rateLimitService:  NewRateLimitService(nil, nil, cfg, nil, nil),
		modelsListCache:   gocache.New(time.Minute, 0), modelsListCacheTTL: time.Minute}
	svc.schedulerSnapshot.seedCachedGroup(group)
	ctx := svc.PrepareSchedulerRequestContext(context.WithValue(context.Background(), ctxkey.Group, group))
	return svc, cache, ctx, group
}

func recordModelTransientFailures(svc *GatewayService, account *Account) {
	for range 3 {
		svc.rateLimitService.recordModelTransientOutcome(account, 502, []string{"model-a"})
	}
}

func TestModelTransientFallbackRealSelection(t *testing.T) {
	for _, tc := range []struct {
		name     string
		accounts int
		cool     []int
		excluded map[int64]struct{}
		wantID   int64
	}{
		{name: "single account after three faults", accounts: 1, cool: []int{0}, wantID: 1},
		{name: "healthy alternative wins", accounts: 2, cool: []int{0}, wantID: 2},
		{name: "all accounts cooling", accounts: 2, cool: []int{0, 1}, wantID: 1},
		{name: "exclusions survive fallback", accounts: 2, cool: []int{0, 1}, excluded: map[int64]struct{}{1: {}}, wantID: 2},
		{name: "healthy request runs once", accounts: 2, wantID: 1},
	} {
		for _, entry := range []string{"model", "load"} {
			t.Run(tc.name+"/"+entry, func(t *testing.T) {
				accounts := []*Account{modelTransientFallbackAccount(1, 10)}
				if tc.accounts == 2 {
					accounts = append(accounts, modelTransientFallbackAccount(2, 10))
				}
				svc, cache, ctx, group := newModelTransientFallbackFixture(accounts...)
				for _, i := range tc.cool {
					recordModelTransientFailures(svc, accounts[i])
					require.WithinDuration(t, time.Now().Add(modelTransientLongCooldown), svc.rateLimitService.modelTransient.CooldownUntil(accounts[i].ID, "model-a"), time.Second)
				}
				var selected *Account
				if entry == "model" {
					var err error
					selected, err = svc.SelectAccountForModelWithExclusions(ctx, &group.ID, "", "model-a", tc.excluded)
					require.NoError(t, err)
					require.Equal(t, 1, cache.accountReads, "selection hydrates once, not twice")
				} else {
					selection, err := svc.SelectAccountWithLoadAwareness(ctx, &group.ID, "", "model-a", tc.excluded, "", 0)
					require.NoError(t, err)
					selected = selection.Account
					require.Equal(t, 2, cache.accountReads, "normal hydration count is unchanged")
				}
				require.Equal(t, tc.wantID, selected.ID)
				for _, i := range tc.cool {
					require.False(t, svc.rateLimitService.ModelTransientAllowed(accounts[i].ID, "model-a"), "fallback must not erase shared cooldown")
				}
				if len(tc.cool) <= 1 && tc.accounts == 2 {
					require.Equal(t, 1, cache.snapshotReads, "a successful first pass must not retry")
				}
			})
		}
	}
}

func TestModelTransientFallbackKeyRoutesExhaustHealthyGroupsFirst(t *testing.T) {
	for _, allCooling := range []bool{false, true} {
		t.Run(map[bool]string{false: "second group healthy", true: "all groups cooling"}[allCooling], func(t *testing.T) {
			primary, secondary := modelTransientFallbackAccount(1, 10), modelTransientFallbackAccount(2, 20)
			svc, cache, ctx, group := newModelTransientFallbackFixture(primary, secondary)
			otherGroup := *group
			otherGroup.ID = 20
			svc.schedulerSnapshot.seedCachedGroup(&otherGroup)
			recordModelTransientFailures(svc, primary)
			if allCooling {
				recordModelTransientFailures(svc, secondary)
			}
			key := &APIKey{GroupID: &group.ID, Group: group, RouteGroupIDs: []int64{10, 20}, User: &User{ID: 1}}
			catalog := groupModelsCatalog{byPlatform: map[string][]string{PlatformAnthropic: {"model-a"}}, platforms: map[string]struct{}{PlatformAnthropic: {}}}
			svc.storeGroupModelsCatalog(&group.ID, catalog)
			svc.storeGroupModelsCatalog(&otherGroup.ID, catalog)
			selection, routed, err := svc.SelectAccountAlongKeyRoutes(ctx, key, "", "model-a", nil, "", 0)
			require.NoError(t, err)
			wantID, wantGroup := int64(2), int64(20)
			if allCooling {
				wantID, wantGroup = 1, 10
			}
			require.Equal(t, wantID, selection.Account.ID)
			require.Equal(t, wantGroup, *routed.GroupID)
			require.Equal(t, 2, cache.accountReads)
			if !allCooling {
				require.Equal(t, 2, cache.snapshotReads, "two normal route attempts, no fallback pass")
			}
		})
	}
}

func TestModelTransientFallbackOnlyRetriesNoAvailableErrors(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       error
		wantCalls int
	}{
		{name: "success", wantCalls: 1},
		{name: "no accounts", err: ErrNoAvailableAccounts, wantCalls: 2},
		{name: "wrapped no accounts", err: fmt.Errorf("model: %w", ErrNoAvailableAccounts), wantCalls: 2},
		{name: "snapshot missing", err: ErrSchedulerCacheNotReady, wantCalls: 1},
		{name: "security restriction", err: ErrSecurityPenaltyGroupRestricted, wantCalls: 1},
		{name: "permission", err: ErrGroupNotAllowed, wantCalls: 1},
		{name: "canceled", err: context.Canceled, wantCalls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			_, err := withModelTransientFallback(context.Background(), func(ctx context.Context) (int, error) {
				calls++
				probe, ok := ctx.Value(modelTransientFallbackContextKey{}).(*modelTransientFallbackProbe)
				require.True(t, ok)
				probe.blocked.Store(true)
				if calls == 2 {
					require.True(t, probe.bypass)
				}
				return 0, tc.err
			})
			require.Equal(t, tc.wantCalls, calls)
			require.True(t, errors.Is(err, tc.err))
		})
	}
}

func TestModelTransientFallbackCompactErrorsAreOptIn(t *testing.T) {
	for _, tc := range []struct {
		name      string
		optIn     bool
		retryErr  error
		wantErr   error
		wantCalls int
	}{
		{name: "default unchanged", wantErr: ErrNoAvailableCompactAccounts, wantCalls: 1},
		{name: "compact recovered", optIn: true, wantCalls: 2},
		{name: "compact still unavailable", optIn: true, retryErr: ErrNoAvailableAccounts, wantErr: ErrNoAvailableCompactAccounts, wantCalls: 2},
		{name: "new error not swallowed", optIn: true, retryErr: ErrSchedulerCacheNotReady, wantErr: ErrSchedulerCacheNotReady, wantCalls: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			var additional []error
			if tc.optIn {
				additional = []error{ErrNoAvailableCompactAccounts}
			}
			_, err := withModelTransientFallback(context.Background(), func(ctx context.Context) (int, error) {
				calls++
				probe := ctx.Value(modelTransientFallbackContextKey{}).(*modelTransientFallbackProbe)
				probe.blocked.Store(true)
				if calls == 1 {
					return 0, fmt.Errorf("compact: %w", ErrNoAvailableCompactAccounts)
				}
				require.True(t, probe.bypass)
				return 0, tc.retryErr
			}, additional...)
			require.ErrorIs(t, err, tc.wantErr)
			require.Equal(t, tc.wantCalls, calls)
		})
	}
}

func TestModelTransientFallbackNoAccountsWithoutCooldownRunsOnce(t *testing.T) {
	svc, cache, ctx, group := newModelTransientFallbackFixture()
	_, err := svc.SelectAccountWithLoadAwareness(ctx, &group.ID, "", "model-a", nil, "", 0)
	require.ErrorIs(t, err, ErrNoAvailableAccounts)
	require.Equal(t, 1, cache.snapshotReads, "an empty non-cooling pool must not be retried")
}

func TestModelTransientFallbackSnapshotMissDoesNotRetry(t *testing.T) {
	account := modelTransientFallbackAccount(1, 10)
	svc, cache, ctx, group := newModelTransientFallbackFixture(account)
	cache.miss = true
	recordModelTransientFailures(svc, account)
	_, err := svc.SelectAccountWithLoadAwareness(ctx, &group.ID, "", "model-a", nil, "", 0)
	require.ErrorIs(t, err, ErrSchedulerCacheNotReady)
	require.Equal(t, 1, cache.snapshotReads)
	require.Zero(t, cache.accountReads)
}

func TestModelTransientFallbackDoesNotReviveHardBlockedAccounts(t *testing.T) {
	for _, tc := range []struct {
		name     string
		modify   func(*Account)
		excluded map[int64]struct{}
	}{
		{name: "disabled", modify: func(a *Account) { a.Schedulable = false }},
		{name: "temporary account cooldown", modify: func(a *Account) { until := time.Now().Add(time.Minute); a.TempUnschedulableUntil = &until }},
		{name: "unsupported model", modify: func(a *Account) {
			a.Credentials = map[string]any{"model_mapping": map[string]any{"other-model": "other-model"}}
		}},
		{name: "model rate limit", modify: func(a *Account) {
			a.Extra = map[string]any{"model_rate_limits": map[string]any{"model-a": map[string]any{"rate_limit_reset_at": time.Now().Add(time.Minute).UTC().Format(time.RFC3339)}}}
		}},
		{name: "excluded", excluded: map[int64]struct{}{1: {}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := modelTransientFallbackAccount(1, 10)
			if tc.modify != nil {
				tc.modify(account)
			}
			svc, cache, ctx, group := newModelTransientFallbackFixture(account)
			recordModelTransientFailures(svc, account)
			passes := 0
			_, err := withModelTransientFallback(ctx, func(ctx context.Context) (*AccountSelectionResult, error) {
				passes++
				return svc.SelectAccountWithLoadAwareness(ctx, &group.ID, "", "model-a", tc.excluded, "", 0)
			})
			require.ErrorIs(t, err, ErrNoAvailableAccounts)
			require.Zero(t, cache.accountReads)
			require.Equal(t, 1, passes, "hard rejections must not trigger a cooldown retry")
		})
	}
}

func TestModelTransientFallbackDiagnosticsDoNotAdmit(t *testing.T) {
	account := modelTransientFallbackAccount(1, 10)
	rate := 1.0
	account.RateMultiplier = &rate
	svc, cache, ctx, group := newModelTransientFallbackFixture(account)
	recordModelTransientFailures(svc, account)
	ctx = context.WithValue(ctx, openAIProfitControlGateCtxKey{}, &openAIProfitControlGate{threshold: 0.5})
	probe := &modelTransientFallbackProbe{}
	diagnosticCtx := context.WithValue(ctx, modelTransientFallbackContextKey{}, probe)
	require.False(t, svc.isGatewayAccountProfitEligible(ctx, account))
	cooldown := svc.rateLimitService.modelTransient
	previous := *cooldown.entries[modelTransientKey(account.ID, "model-a")]
	cooldown.now = func() time.Time { return previous.lastSeen.Add(time.Second) }
	svc.diagnoseSelectionFailure(diagnosticCtx, account, "model-a", PlatformAnthropic, nil, false)
	require.False(t, probe.blocked.Load(), "diagnostics must not record an admission attempt")
	require.Equal(t, previous, *cooldown.entries[modelTransientKey(account.ID, "model-a")], "diagnostics must not refresh or mutate cooldown state")
	_, err := svc.SelectAccountWithLoadAwareness(ctx, &group.ID, "", "model-a", nil, "", 0)
	require.ErrorIs(t, err, ErrNoAvailableAccounts)
	require.Equal(t, 1, cache.snapshotReads, "a profit-vetoed account must not cause a second pool scan")
}
