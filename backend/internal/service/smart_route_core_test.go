package service

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	gocache "github.com/patrickmn/go-cache"
	"github.com/stretchr/testify/require"
)

type smartRouteCoreSnapshotCache struct {
	openAISnapshotCacheStub
	reads atomic.Int64
}

func (c *smartRouteCoreSnapshotCache) GetSnapshot(_ context.Context, bucket SchedulerBucket) ([]*Account, bool, error) {
	c.reads.Add(1)
	var accounts []*Account
	for _, account := range c.snapshotAccounts {
		if account.Platform == bucket.Platform && openAIStickyAccountMatchesGroup(account, &bucket.GroupID) {
			accounts = append(accounts, account)
		}
	}
	return accounts, len(accounts) > 0, nil
}

func newSmartRouteCoreSnapshot(groups []*Group, accounts ...*Account) (*SchedulerSnapshotService, *smartRouteCoreSnapshotCache) {
	cache := &smartRouteCoreSnapshotCache{openAISnapshotCacheStub: openAISnapshotCacheStub{
		snapshotAccounts: accounts, accountsByID: make(map[int64]*Account),
	}}
	for _, account := range accounts {
		cache.accountsByID[account.ID] = account
	}
	snapshot := &SchedulerSnapshotService{cache: cache, cfg: &config.Config{}}
	for _, group := range groups {
		snapshot.storeCachedGroup(group)
	}
	return snapshot, cache
}

func smartRouteCoreKey(groups ...*Group) *APIKey {
	key := &APIKey{User: &User{ID: 100, Status: StatusActive}, UserID: 100}
	for _, group := range groups {
		key.RouteGroupIDs = append(key.RouteGroupIDs, group.ID)
		key.User.AllowedGroups = append(key.User.AllowedGroups, group.ID)
	}
	if len(groups) > 0 {
		key.GroupID, key.Group = &groups[0].ID, groups[0]
	}
	return key
}

func TestSmartRouteCoreColdSnapshotDoesNotQueryDatabase(t *testing.T) {
	for _, count := range []int{1, 10} {
		t.Run(fmt.Sprintf("groups_%d", count), func(t *testing.T) {
			var groups []*Group
			for i := 1; i <= count; i++ {
				groups = append(groups, &Group{ID: int64(i), Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true})
			}
			snapshot, cache := newSmartRouteCoreSnapshot(groups)
			repo := &modelsListAccountRepoStub{}
			svc := &OpenAIGatewayService{accountRepo: repo, schedulerSnapshot: snapshot}
			attempts := 0
			_, _, _, err := svc.selectAlongKeyRoutes(withSchedulerSnapshotOnly(context.Background()), smartRouteCoreKey(groups...), nil, "gpt-5.5",
				func(context.Context, *int64, []string, string) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
					attempts++
					return nil, OpenAIAccountScheduleDecision{}, ErrNoAvailableAccounts
				})
			require.ErrorIs(t, err, ErrNoAvailableAccounts)
			require.Equal(t, count, attempts, "authorized cold catalogs remain unknown, not absent")
			require.Equal(t, int64(count*len(groupCatalogPlatforms())), cache.reads.Load(), "one catalog pass per candidate")
			require.Zero(t, repo.listByGroupCalls.Load())
			t.Logf("groups=%d attempts=%d repository_account_scans=%d", count, attempts, repo.listByGroupCalls.Load())
		})
	}
}

func TestSmartRouteCoreWarmSnapshotDoesNotQueryOtherProviders(t *testing.T) {
	group := &Group{ID: 9, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true}
	account := &Account{ID: 1, Platform: PlatformOpenAI, GroupIDs: []int64{9}, Credentials: map[string]any{"model_mapping": map[string]any{"gpt-5.5": "gpt-5.5"}}}
	snapshot, _ := newSmartRouteCoreSnapshot([]*Group{group}, account)
	repo := &modelsListAccountRepoStub{}
	svc := &OpenAIGatewayService{accountRepo: repo, schedulerSnapshot: snapshot}
	for i := 0; i < 20; i++ {
		_, _, routed, err := svc.selectAlongKeyRoutes(context.Background(), smartRouteCoreKey(group), nil, "gpt-5.5",
			func(ctx context.Context, gid *int64, _ []string, model string) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
				require.Equal(t, *gid, ctx.Value(ctxkey.Group).(*Group).ID)
				require.Equal(t, "gpt-5.5", model)
				return &AccountSelectionResult{Account: account}, OpenAIAccountScheduleDecision{}, nil
			})
		require.NoError(t, err)
		require.Equal(t, group.ID, *routed.GroupID)
	}
	require.Zero(t, repo.listByGroupCalls.Load())
	t.Logf("requests=20 repository_account_scans=%d", repo.listByGroupCalls.Load())
}

func TestSmartRouteCoreAuthorizationBeforeSelection(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mutate  func(*APIKey, *Group)
		allowed bool
	}{
		{"public", func(*APIKey, *Group) {}, true},
		{"exclusive_allowed", func(_ *APIKey, g *Group) { g.IsExclusive = true }, true},
		{"revoked_exclusive", func(k *APIKey, g *Group) { g.IsExclusive = true; k.User.AllowedGroups = nil }, false},
		{"revoked_public", func(k *APIKey, _ *Group) { k.User.RestrictPublicGroups = true; k.User.AllowedGroups = nil }, false},
		{"inactive", func(_ *APIKey, g *Group) { g.Status = "inactive" }, false},
		{"missing_user", func(k *APIKey, _ *Group) { k.User = nil }, false},
		{"incomplete_policy", func(_ *APIKey, g *Group) { g.Hydrated = false }, false},
		{"subscription_uses_subscription_resolver", func(k *APIKey, g *Group) {
			g.SubscriptionType = SubscriptionTypeSubscription
			g.IsExclusive = true
			k.User.AllowedGroups = nil
		}, true},
		{"composite_parent", func(_ *APIKey, g *Group) { g.Platform = PlatformComposite }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			group := &Group{ID: 9, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true}
			key := smartRouteCoreKey(group)
			tc.mutate(key, group)
			svc := &OpenAIGatewayService{}
			attempts := 0
			_, _, _, err := svc.selectAlongKeyRoutes(context.Background(), key, nil, "gpt-5.5",
				func(context.Context, *int64, []string, string) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
					attempts++
					return nil, OpenAIAccountScheduleDecision{}, ErrNoAvailableAccounts
				})
			require.ErrorIs(t, err, ErrNoAvailableAccounts)
			if tc.allowed {
				require.Equal(t, 1, attempts)
			} else {
				require.Zero(t, attempts)
			}
		})
	}
}

func TestSmartRouteCoreMissingPolicyFailsClosed(t *testing.T) {
	group := &Group{ID: 9, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true}
	for _, missing := range []bool{false, true} {
		t.Run(fmt.Sprintf("missing_%t", missing), func(t *testing.T) {
			snapshot, _ := newSmartRouteCoreSnapshot(nil)
			if !missing {
				inactive := *group
				inactive.Status = "inactive"
				snapshot.storeCachedGroup(&inactive)
			}
			svc := &OpenAIGatewayService{schedulerSnapshot: snapshot}
			_, _, _, err := svc.selectAlongKeyRoutes(context.Background(), smartRouteCoreKey(group), nil, "gpt-5.5",
				func(context.Context, *int64, []string, string) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
					t.Fatal("stale key policy must not authorize selection")
					return nil, OpenAIAccountScheduleDecision{}, nil
				})
			require.Error(t, err)
		})
	}
}

func TestSmartRouteCoreCatalogSnapshotOnlyWithoutCache(t *testing.T) {
	repo := &modelsListAccountRepoStub{}
	snapshot, _ := newSmartRouteCoreSnapshot(nil)
	snapshot.accountRepo = repo
	svc := &GatewayService{accountRepo: repo, schedulerSnapshot: snapshot}
	groupID := int64(9)
	require.Nil(t, svc.GetAvailableModels(withSchedulerSnapshotOnly(context.Background()), &groupID, PlatformOpenAI))
	require.Zero(t, repo.listByGroupCalls.Load())
}

func TestSmartRouteCoreCatalogSingleflight(t *testing.T) {
	repo := &modelsListAccountRepoStub{byGroup: map[int64][]Account{9: {{Platform: PlatformOpenAI, Credentials: map[string]any{"model_mapping": map[string]any{"gpt-5.5": "gpt-5.5"}}}}}}
	svc := &GatewayService{accountRepo: repo, modelsListCache: gocache.New(time.Minute, time.Minute), modelsListCacheTTL: time.Minute}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			gid := int64(9)
			cat := svc.ensureGroupModelsCatalog(context.Background(), &gid)
			require.Equal(t, groupCatalogModelPresent, catalogHasRequestedModel(cat, "gpt-5.5"))
		}()
	}
	wg.Wait()
	require.Equal(t, int64(1), repo.listByGroupCalls.Load())
}

func TestContextWithAPIKeyRoute(t *testing.T) {
	primary := &Group{ID: 9, Platform: PlatformComposite, Status: StatusActive, Hydrated: true}
	key := smartRouteCoreKey(primary)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	updated := ContextWithAPIKeyRoute(ctx, key)
	require.Same(t, primary, updated.Value(ctxkey.Group))
	require.Nil(t, ctx.Value(ctxkey.Group))
	require.Same(t, ctx, ContextWithAPIKeyRoute(ctx, nil))
	require.NotNil(t, ContextWithAPIKeyRoute(nil, nil))
	require.Same(t, ctx, ContextWithAPIKeyRoute(ctx, &APIKey{Group: primary}))
	cancel()
	require.ErrorIs(t, updated.Err(), context.Canceled)
}

func TestSmartRouteCoreNilAndUngrouped(t *testing.T) {
	svc := &OpenAIGatewayService{}
	_, _, _, err := svc.selectAlongKeyRoutes(nil, nil, nil, "model", nil)
	require.ErrorIs(t, err, ErrNoAvailableAccounts)
	_, _, err = (&GatewayService{}).SelectAccountAlongKeyRoutes(nil, nil, "", "model", nil, "", 0)
	require.ErrorIs(t, err, ErrNoAvailableAccounts)
	key := smartRouteCoreKey()
	_, _, routed, err := svc.selectAlongKeyRoutes(nil, key, []string{PlatformOpenAI}, "model",
		func(ctx context.Context, gid *int64, platforms []string, model string) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
			require.NotNil(t, ctx)
			require.Nil(t, gid)
			require.Equal(t, []string{PlatformOpenAI}, platforms)
			require.Equal(t, "model", model)
			return &AccountSelectionResult{}, OpenAIAccountScheduleDecision{}, nil
		})
	require.NoError(t, err)
	require.Same(t, key, routed)
}

func TestSmartRouteCoreCompositeRetainsBillingParent(t *testing.T) {
	parent := &Group{ID: 9, Platform: PlatformComposite, Status: StatusActive, Hydrated: true, RateMultiplier: 0.4}
	member := profitControlTestGroup(27, 0.25, 0)
	member.RateMultiplier = 99
	snapshot, _ := newSmartRouteCoreSnapshot([]*Group{parent, member})
	svc := &OpenAIGatewayService{schedulerSnapshot: snapshot}
	key := smartRouteCoreKey(parent)
	_, _, routed, err := svc.selectAlongKeyRoutes(context.Background(), key, []string{PlatformOpenAI}, "gpt-5.5",
		func(ctx context.Context, gid *int64, _ []string, _ string) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
			require.Equal(t, parent.ID, *gid)
			gate := svc.resolveOpenAIProfitControlGate(ctx, &member.ID)
			require.NotNil(t, gate)
			require.InDelta(t, 0.3, gate.threshold, 1e-9)
			return &AccountSelectionResult{}, OpenAIAccountScheduleDecision{}, nil
		})
	require.NoError(t, err)
	require.Equal(t, parent.ID, *routed.GroupID)
}

func TestSmartRouteCoreSnapshotCatalogWithoutRepository(t *testing.T) {
	group := &Group{ID: 9, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true}
	account := &Account{ID: 1, Platform: PlatformOpenAI, GroupIDs: []int64{9}, Credentials: map[string]any{"model_mapping": map[string]any{"gpt-5.5": "gpt-5.5"}}}
	snapshot, _ := newSmartRouteCoreSnapshot([]*Group{group}, account)
	svc := &GatewayService{schedulerSnapshot: snapshot}
	require.Equal(t, []string{"gpt-5.5"}, svc.GetAvailableModels(context.Background(), &group.ID, PlatformOpenAI))
}

func TestSmartRouteCoreMessagesAliasesAreCandidateScoped(t *testing.T) {
	for _, messages := range []bool{false, true} {
		t.Run(fmt.Sprintf("messages_%t", messages), func(t *testing.T) {
			primary := &Group{ID: 9, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true, AllowMessagesDispatch: true,
				MessagesDispatchModelConfig: OpenAIMessagesDispatchModelConfig{ExactModelMappings: map[string]string{"claude-company": "primary-private"}}}
			backup := &Group{ID: 27, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true, AllowMessagesDispatch: true,
				MessagesDispatchModelConfig: OpenAIMessagesDispatchModelConfig{ExactModelMappings: map[string]string{"claude-company": "backup-private"}}}
			accounts := []*Account{
				{ID: 1, Platform: PlatformOpenAI, GroupIDs: []int64{9}, Credentials: map[string]any{"model_mapping": map[string]any{"primary-private": "primary-private"}}},
				{ID: 2, Platform: PlatformOpenAI, GroupIDs: []int64{27}, Credentials: map[string]any{"model_mapping": map[string]any{"backup-private": "backup-private"}}},
			}
			snapshot, _ := newSmartRouteCoreSnapshot([]*Group{primary, backup}, accounts...)
			svc := &OpenAIGatewayService{schedulerSnapshot: snapshot}
			ctx := context.Background()
			if messages {
				ctx = WithOpenAIMessagesKeyRoute(ctx)
			}
			var tried []string
			_, _, routed, err := svc.selectAlongKeyRoutes(ctx, smartRouteCoreKey(primary, backup), nil, "claude-company",
				func(_ context.Context, gid *int64, _ []string, model string) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
					tried = append(tried, model)
					if *gid == primary.ID {
						return nil, OpenAIAccountScheduleDecision{}, ErrNoAvailableAccounts
					}
					return &AccountSelectionResult{}, OpenAIAccountScheduleDecision{}, nil
				})
			if messages {
				require.NoError(t, err)
				require.Equal(t, []string{"primary-private", "backup-private"}, tried)
				require.Equal(t, backup.ID, *routed.GroupID)
			} else {
				require.ErrorIs(t, err, ErrNoAvailableAccounts)
				require.Empty(t, tried, "ordinary Chat must not use Messages aliases")
			}
		})
	}
}

func TestSmartRouteCoreMessagesNormalizationAndCompositeExemptions(t *testing.T) {
	ctx := WithOpenAIMessagesKeyRoute(context.Background())
	group := &Group{Platform: PlatformOpenAI}
	require.Equal(t, NormalizeOpenAICompatRequestedModel("gpt-5.4-xhigh"), openAIMessagesKeyRouteModel(ctx, group, "gpt-5.4-xhigh"))
	group.Platform = PlatformComposite
	for _, platform := range []string{PlatformGrok, PlatformKimi, PlatformZhipu, PlatformDeepseek, PlatformMiniMax} {
		require.Equal(t, "claude-sonnet-4-5", openAIMessagesKeyRouteModel(WithResolvedTargetPlatform(ctx, platform), group, "claude-sonnet-4-5"))
	}
	require.Equal(t, group.ResolveMessagesDispatchModel("claude-sonnet-4-5"), openAIMessagesKeyRouteModel(WithResolvedTargetPlatform(ctx, PlatformOpenAI), group, "claude-sonnet-4-5"))
}

func TestSmartRouteCoreMessagesAliasCannotBypassOriginalAllowlist(t *testing.T) {
	group := &Group{ID: 9, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true, AllowMessagesDispatch: true,
		ModelAllowlist:              GroupModelsListConfig{Enabled: true, Models: []string{"gpt-private"}},
		MessagesDispatchModelConfig: OpenAIMessagesDispatchModelConfig{ExactModelMappings: map[string]string{"blocked-alias": "gpt-private"}}}
	svc := &OpenAIGatewayService{}
	_, _, _, err := svc.selectAlongKeyRoutes(WithOpenAIMessagesKeyRoute(context.Background()), smartRouteCoreKey(group), nil, "blocked-alias",
		func(context.Context, *int64, []string, string) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
			t.Fatal("dispatch alias must not override the original model allowlist")
			return nil, OpenAIAccountScheduleDecision{}, nil
		})
	require.ErrorIs(t, err, ErrNoAvailableAccounts)
}

func TestSmartRouteCoreMessagesCandidateDispatchPermission(t *testing.T) {
	for _, tc := range []struct {
		name, platform, resolved       string
		messages, allow, wantCandidate bool
	}{
		{"messages_openai_disabled", PlatformOpenAI, "", true, false, false},
		{"messages_smart_openai_disabled", PlatformOpenAI, PlatformOpenAI, true, false, false},
		{"messages_openai_enabled", PlatformOpenAI, PlatformOpenAI, true, true, true},
		{"chat_openai_disabled", PlatformOpenAI, PlatformOpenAI, false, false, true},
		{"messages_grok_exempt", PlatformGrok, PlatformOpenAI, true, false, true},
		{"messages_kimi_exempt", PlatformKimi, "", true, false, true},
		{"messages_zhipu_exempt", PlatformZhipu, "", true, false, true},
		{"messages_deepseek_exempt", PlatformDeepseek, "", true, false, true},
		{"messages_minimax_exempt", PlatformMiniMax, "", true, false, true},
		{"messages_openai_resolved_grok_exempt", PlatformOpenAI, PlatformGrok, true, false, true},
		{"messages_openai_resolved_kimi_exempt", PlatformOpenAI, PlatformKimi, true, false, true},
		{"messages_composite_openai_disabled", PlatformComposite, PlatformOpenAI, true, false, false},
		{"messages_composite_grok_exempt", PlatformComposite, PlatformGrok, true, false, true},
		{"messages_composite_deepseek_exempt", PlatformComposite, PlatformDeepseek, true, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			primary := &Group{ID: 9, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true, AllowMessagesDispatch: true}
			candidate := &Group{ID: 27, Platform: tc.platform, Status: StatusActive, Hydrated: true, AllowMessagesDispatch: tc.allow}
			last := &Group{ID: 28, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true, AllowMessagesDispatch: true}
			groups := []*Group{primary, candidate, last}
			account := &Account{ID: 1027, Platform: tc.platform, GroupIDs: []int64{candidate.ID}, Credentials: map[string]any{"model_mapping": map[string]any{"company-model": "company-model"}}}
			snapshot, _ := newSmartRouteCoreSnapshot(groups, account)
			svc := &OpenAIGatewayService{schedulerSnapshot: snapshot}
			ctx := context.Background()
			if tc.messages {
				ctx = WithOpenAIMessagesKeyRoute(ctx)
			}
			if tc.resolved != "" {
				ctx = WithResolvedTargetPlatform(ctx, tc.resolved)
			}
			var tried []int64
			_, _, routed, err := svc.selectAlongKeyRoutes(ctx, smartRouteCoreKey(groups...), nil, "company-model",
				func(_ context.Context, gid *int64, _ []string, _ string) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
					tried = append(tried, *gid)
					if *gid == primary.ID {
						return nil, OpenAIAccountScheduleDecision{}, ErrNoAvailableAccounts
					}
					return &AccountSelectionResult{}, OpenAIAccountScheduleDecision{}, nil
				})
			require.NoError(t, err)
			if tc.wantCandidate {
				require.Equal(t, candidate.ID, *routed.GroupID)
			} else {
				require.Equal(t, last.ID, *routed.GroupID)
				require.Equal(t, []int64{primary.ID, last.ID}, tried, "disabled candidate must not hide unknown sibling catalogs or reach sticky selection")
			}
		})
	}
}
