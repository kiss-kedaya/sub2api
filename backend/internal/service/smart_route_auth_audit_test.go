//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

// Keep the real lifecycle retirement implementation; supply only in-memory
// snapshot/account reads so this reproduction needs no database or Redis.
type smartRouteAuditCache struct {
	*groupLifecycleTestCache
	accounts []*Account
}

func (c *smartRouteAuditCache) GetSnapshot(_ context.Context, bucket SchedulerBucket) ([]*Account, bool, error) {
	c.mu.Lock()
	retired := c.retired[bucket.String()]
	c.mu.Unlock()
	if retired {
		return nil, false, nil
	}
	out := make([]*Account, 0)
	for _, account := range c.accounts {
		if account.Platform == bucket.Platform && openAIStickyAccountMatchesGroup(account, &bucket.GroupID) {
			out = append(out, account)
		}
	}
	return out, len(out) > 0, nil
}

func (c *smartRouteAuditCache) GetAccount(_ context.Context, id int64) (*Account, error) {
	for _, account := range c.accounts {
		if account.ID == id {
			cloned := *account
			return &cloned, nil
		}
	}
	return nil, nil
}

type smartRouteAuditStickyCache struct {
	*schedulerTestGatewayCache
	hits int
}

func (c *smartRouteAuditStickyCache) GetSessionAccountID(ctx context.Context, groupID int64, sessionHash string) (int64, error) {
	if groupID != 27 {
		return 0, ErrStickySessionNotFound
	}
	id, err := c.schedulerTestGatewayCache.GetSessionAccountID(ctx, groupID, sessionHash)
	if err == nil && id > 0 {
		c.hits++
	}
	return id, err
}

func TestSmartRouteAuthAudit_BackupAuthorization(t *testing.T) {
	for _, tc := range []struct {
		name       string
		inactive   bool
		retire     bool
		exclusive  bool
		restricted bool
		sticky     bool
		allowlist  bool
		allowed    bool
	}{
		{name: "authorized_backup_control", allowed: true},
		{name: "revoked_exclusive_permission", exclusive: true},
		{name: "revoked_restricted_public_permission", restricted: true},
		{name: "revoked_exclusive_permission_sticky", exclusive: true, sticky: true},
		{name: "revoked_restricted_public_permission_sticky", restricted: true, sticky: true},
		{name: "inactive_policy_before_outbox_retirement", inactive: true},
		{name: "inactive_after_outbox_retirement", inactive: true, retire: true},
		{name: "inactive_after_outbox_retirement_sticky", inactive: true, retire: true, sticky: true},
		{name: "inactive_after_outbox_retirement_sticky_allowlist", inactive: true, retire: true, sticky: true, allowlist: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetOpenAIAdvancedSchedulerSettingCacheForTest()
			t.Cleanup(resetOpenAIAdvancedSchedulerSettingCacheForTest)
			ctx := context.Background()
			primary := &Group{ID: 9, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true}
			backup := &Group{ID: 27, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true, IsExclusive: tc.exclusive}
			last := &Group{ID: 28, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true}
			if tc.inactive {
				backup.Status = "inactive"
			}
			if tc.allowlist {
				backup.ModelAllowlist = GroupModelsListConfig{Enabled: true, Models: []string{"gpt-5.4"}}
			}
			user := &User{ID: 100, Status: StatusActive, Balance: 10, AllowedGroups: []int64{9, 28}, RestrictPublicGroups: tc.restricted}
			if tc.allowed {
				user.AllowedGroups = append(user.AllowedGroups, backup.ID)
			}
			key := &APIKey{ID: 200, UserID: user.ID, Status: StatusActive, User: user, GroupID: &primary.ID, Group: primary, RouteGroupIDs: []int64{9, 27, 28}}
			require.True(t, user.CanBindGroup(primary.ID, primary.IsExclusive))
			if !tc.inactive && !tc.allowed {
				require.False(t, user.CanBindGroup(backup.ID, backup.IsExclusive))
			}
			cache := &smartRouteAuditCache{groupLifecycleTestCache: newGroupLifecycleTestCache()}
			for _, gid := range []int64{27, 28} {
				cache.accounts = append(cache.accounts, &Account{
					ID: 1000 + gid, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
					Status: StatusActive, Schedulable: true, Concurrency: 1, GroupIDs: []int64{gid},
					Credentials: map[string]any{"model_mapping": map[string]any{"gpt-5.4": "gpt-5.4"}},
				})
			}
			cfg := &config.Config{RunMode: config.RunModeStandard}
			auth := NewAPIKeyService(&authRepoStub{getByKeyForAuth: func(context.Context, string) (*APIKey, error) { return key, nil }}, nil, nil, nil, nil, nil, cfg)
			key, authErr := auth.GetByKey(ctx, "smart-route-audit-synthetic")
			require.NoError(t, authErr)
			require.Equal(t, user.AllowedGroups, key.User.AllowedGroups)
			repo := &modelsListAccountRepoStub{byGroup: map[int64][]Account{}}
			for _, account := range cache.accounts {
				repo.byGroup[account.GroupIDs[0]] = append(repo.byGroup[account.GroupIDs[0]], *account)
			}
			if tc.allowlist {
				// Prove sticky admission is independent of the catalog DB fallback.
				repo.err = errors.New("catalog database fallback unavailable")
			}
			snapshot := &SchedulerSnapshotService{cache: cache, cfg: cfg, groupRepo: &groupLifecycleTestGroupRepo{group: backup}}
			for _, group := range []*Group{primary, backup, last} {
				snapshot.storeCachedGroup(group)
			}
			if tc.retire {
				require.NoError(t, snapshot.handleGroupEvent(ctx, &backup.ID, make(map[batchSeenKey]struct{})))
				require.NotEmpty(t, cache.retiredBuckets())
			}
			stickyCache := &smartRouteAuditStickyCache{schedulerTestGatewayCache: &schedulerTestGatewayCache{}}
			sessionHash := ""
			if tc.sticky {
				sessionHash = "audit-sticky"
				stickyCache.sessionBindings = map[string]int64{"openai:" + sessionHash: 1027}
			}
			svc := &OpenAIGatewayService{
				cfg: cfg, schedulerSnapshot: snapshot, cache: stickyCache, accountRepo: repo,
				concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
			}
			// Keep this fixture's global settings reset from racing a cold-cache
			// background refresh launched by snapshot-only scheduling.
			_ = svc.openAIAdvancedSchedulerRuntimeSettings(context.Background())
			selection, decision, routed, err := svc.SelectAccountWithSchedulerForCapabilityAlongKeyRoutes(
				ctx, key, "", sessionHash, "gpt-5.4", nil, OpenAIUpstreamTransportAny,
				OpenAIEndpointCapabilityResponses, false, false, false, PlatformOpenAI,
			)
			require.NoError(t, err)
			require.NotNil(t, selection)
			defer releaseAccountSelection(selection)
			require.NotNil(t, routed)
			if tc.sticky {
				require.Zero(t, stickyCache.hits, "rejected groups must never reach sticky selection")
			}
			t.Logf("selected_group=%d account=%d backup_status=%s backup_allowed=%t retired=%t sticky_hits=%d layer=%s",
				*routed.GroupID, selection.Account.ID, backup.Status, user.CanBindGroup(backup.ID, backup.IsExclusive), tc.retire, stickyCache.hits, decision.Layer)
			want := last.ID
			if tc.allowed {
				want = backup.ID
			}
			require.Equal(t, want, *routed.GroupID, "must skip an inactive or unauthorized backup and use the healthy final route")
		})
	}
}

func TestSmartRouteCoreGatewayAuthorizationAndMapping(t *testing.T) {
	for _, state := range []string{"authorized", "revoked_exclusive", "revoked_public", "inactive", "missing"} {
		t.Run(state, func(t *testing.T) {
			primary := gatewayProfitTestGroup(9, PlatformAnthropic)
			primary.RateMultiplier = 0.1
			backup := gatewayProfitTestGroup(27, PlatformAnthropic)
			backup.RateMultiplier = 2
			last := gatewayProfitTestGroup(28, PlatformAnthropic)
			last.RateMultiplier = 2
			groups := []*Group{primary, backup, last}
			key := smartRouteCoreKey(groups...)
			for _, group := range groups {
				group.ModelAllowlist = GroupModelsListConfig{Enabled: true, Models: []string{"claude-public"}}
			}
			switch state {
			case "revoked_exclusive":
				backup.IsExclusive = true
				key.User.AllowedGroups = []int64{9, 28}
			case "revoked_public":
				key.User.RestrictPublicGroups = true
				key.User.AllowedGroups = []int64{9, 28}
			case "inactive":
				backup.Status = "inactive"
			case "missing":
				groups = []*Group{primary, last}
			}
			backupAccount := gatewayProfitTestAccount(1027, PlatformAnthropic, 1, backup.ID)
			backupAccount.Credentials = map[string]any{"model_mapping": map[string]any{"backup-private": "backup-private"}}
			lastAccount := gatewayProfitTestAccount(1028, PlatformAnthropic, 1, last.ID)
			lastAccount.Credentials = map[string]any{"model_mapping": map[string]any{"claude-public": "claude-public"}}
			snapshot, _ := newSmartRouteCoreSnapshot(groups, &backupAccount, &lastAccount)
			repo := &modelsListAccountRepoStub{}
			channel := newTestChannelService(makeStandardRepo(Channel{
				ID: 1, Status: StatusActive, GroupIDs: []int64{27},
				ModelMapping: map[string]map[string]string{PlatformAnthropic: {"claude-public": "backup-private"}},
			}, map[int64]string{27: PlatformAnthropic}))
			_, channelErr := channel.GetChannelForGroup(context.Background(), backup.ID)
			require.NoError(t, channelErr)
			sticky := &smartRouteAuditStickyCache{schedulerTestGatewayCache: &schedulerTestGatewayCache{
				sessionBindings: map[string]int64{"sticky": backupAccount.ID},
			}}
			svc := &GatewayService{
				accountRepo: repo, schedulerSnapshot: snapshot, cfg: &config.Config{}, cache: sticky,
				channelService: channel, concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
			}
			ctx := ContextWithAPIKeyRoute(context.Background(), key)
			ctx, pricingAt := WithGatewayTokenRequestPricing(ctx)
			result, routed, err := svc.SelectAccountAlongKeyRoutes(ctx, key, "sticky", "claude-public", nil, "", key.UserID, PlatformAnthropic)
			require.NoError(t, err)
			require.NotNil(t, result)
			defer releaseAccountSelection(result)
			wantGroup := last.ID
			if state == "authorized" {
				wantGroup = backup.ID
				require.Positive(t, sticky.hits)
			} else {
				require.Zero(t, sticky.hits)
			}
			require.Equal(t, wantGroup, *routed.GroupID)
			require.Equal(t, int64(1000)+wantGroup, result.Account.ID)
			selectedCtx := ContextWithSelectionProfitGate(ctx, result)
			gate, _ := selectedCtx.Value(openAIProfitControlGateCtxKey{}).(*openAIProfitControlGate)
			require.NotNil(t, gate)
			require.InDelta(t, 2, gate.threshold, 1e-9, "must not reuse primary billing multiplier 0.1")
			require.Equal(t, pricingAt, gate.pricingAt)
			require.Equal(t, primary.ID, gatewayTokenRequestBillingGroupFromContext(ctx).ID)
			require.Zero(t, repo.listByGroupCalls.Load())
		})
	}
}

func TestSmartRouteCoreSimpleModeActiveGroupWithoutSnapshot(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	t.Cleanup(resetOpenAIAdvancedSchedulerSettingCacheForTest)
	for _, platform := range []string{PlatformOpenAI, PlatformGrok} {
		t.Run(platform, func(t *testing.T) {
			group := &Group{ID: 9, Platform: platform, Status: StatusActive, Hydrated: true}
			model := "gpt-5.5"
			if platform == PlatformGrok {
				model = "grok-4.6"
			}
			account := &Account{ID: 1, Platform: platform, Type: AccountTypeAPIKey, Status: StatusActive,
				Schedulable: true, Concurrency: 1, GroupIDs: []int64{group.ID},
				Credentials: map[string]any{"model_mapping": map[string]any{model: model}}}
			repo := &mockAccountRepoForPlatform{accounts: []Account{*account}, accountsByID: map[int64]*Account{account.ID: account}}
			svc := &OpenAIGatewayService{accountRepo: repo, cfg: &config.Config{RunMode: config.RunModeSimple}}
			_ = svc.openAIAdvancedSchedulerRuntimeSettings(context.Background())
			result, _, routed, err := svc.SelectAccountWithSchedulerForCapabilityAlongKeyRoutes(context.Background(), smartRouteCoreKey(group),
				"", "", model, nil, OpenAIUpstreamTransportAny, OpenAIEndpointCapabilityChatCompletions, false, false, false, platform)
			require.NoError(t, err)
			require.NotNil(t, result)
			defer releaseAccountSelection(result)
			require.Equal(t, account.ID, result.Account.ID)
			require.Equal(t, group.ID, *routed.GroupID)
		})
	}
}

func TestSmartRouteCoreCandidateMappingAndProfitContext(t *testing.T) {
	primary := &Group{ID: 9, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true, RateMultiplier: 0.1, ProfitControlEnabled: true}
	backup := &Group{ID: 27, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true, RateMultiplier: 2, ProfitControlEnabled: true}
	for _, group := range []*Group{primary, backup} {
		group.ModelAllowlist = GroupModelsListConfig{Enabled: true, Models: []string{"gpt-5.5"}}
	}
	channel := newTestChannelService(makeStandardRepo(Channel{
		ID: 1, Status: StatusActive, GroupIDs: []int64{9},
		ModelMapping: map[string]map[string]string{PlatformOpenAI: {"gpt-5.5": "primary-private"}},
	}, map[int64]string{9: PlatformOpenAI}))
	_, channelErr := channel.GetChannelForGroup(context.Background(), primary.ID)
	require.NoError(t, channelErr)
	snapshot, _ := newSmartRouteCoreSnapshot([]*Group{primary, backup})
	svc := &OpenAIGatewayService{schedulerSnapshot: snapshot, channelService: channel}
	key := smartRouteCoreKey(primary, backup)
	ctx := ContextWithAPIKeyRoute(context.Background(), key)
	ctx, pricingAt := svc.WithOpenAIRequestPricingContext(ctx, &primary.ID)
	var tried []string
	_, _, routed, err := svc.selectAlongKeyRoutes(ctx, key, nil, "gpt-5.5",
		func(ctx context.Context, gid *int64, _ []string, model string) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
			tried = append(tried, model)
			require.Equal(t, *gid, ctx.Value(ctxkey.Group).(*Group).ID)
			ctx = svc.withOpenAIProfitControlGate(ctx, gid)
			gate := ctx.Value(openAIProfitControlGateCtxKey{}).(*openAIProfitControlGate)
			require.Equal(t, pricingAt, gate.pricingAt)
			if *gid == primary.ID {
				require.InDelta(t, 0.1, gate.threshold, 1e-9)
				return nil, OpenAIAccountScheduleDecision{}, ErrNoAvailableAccounts
			}
			require.InDelta(t, 2, gate.threshold, 1e-9)
			return &AccountSelectionResult{}, OpenAIAccountScheduleDecision{}, nil
		})
	require.NoError(t, err)
	require.Equal(t, []string{"primary-private", "gpt-5.5"}, tried)
	require.Equal(t, backup.ID, *routed.GroupID)
	require.Equal(t, primary.ID, key.Group.ID)
	require.Equal(t, primary.ID, ctx.Value(ctxkey.Group).(*Group).ID)
}
