//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func deepSeekToolsRoutingAccount(id int64, groupID int64, platform string, models ...string) *Account {
	mapping := make(map[string]any, len(models))
	for _, model := range models {
		mapping[model] = model
	}
	return &Account{ID: id, Platform: platform, GroupIDs: []int64{groupID},
		Credentials: map[string]any{"model_mapping": mapping}}
}

func TestPrepareDeepSeekToolsRoutingUsesCatalogAndBoundAuthorizedGroups(t *testing.T) {
	groups := []*Group{
		{ID: 1, Name: "deepseek-models-on-openai", Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true},
		{ID: 2, Platform: PlatformDeepseek, Status: StatusActive, Hydrated: true, ModelAllowlist: GroupModelAllowlist{
			Enabled: true, Models: []string{"deepseek-v4.1-flash", "deepseek-v4.1-pro"},
		}},
		{ID: 3, Platform: PlatformDeepseek, Status: StatusActive, Hydrated: true},
		{ID: 4, Platform: PlatformDeepseek, Status: StatusActive, Hydrated: true, IsExclusive: true},
		{ID: 5, Platform: PlatformDeepseek, Status: "disabled", Hydrated: true},
		{ID: 6, Platform: PlatformComposite, Status: StatusActive, Hydrated: true},
		{ID: 7, Platform: PlatformDeepseek, Status: StatusActive, Hydrated: true}, // Not bound to this key.
	}
	snapshot, _ := newSmartRouteCoreSnapshot(groups,
		deepSeekToolsRoutingAccount(11, 1, PlatformOpenAI, "deepseek-v4-flash"),
		deepSeekToolsRoutingAccount(12, 2, PlatformDeepseek, "deepseek-v4.1-pro", "deepseek-v4.1-flash", "excluded-flash"),
		deepSeekToolsRoutingAccount(13, 3, PlatformDeepseek, "deepseek-v4.1-flash", "deepseek-v5-pro", "deepseek-v5-flash"),
		deepSeekToolsRoutingAccount(14, 4, PlatformDeepseek, "revoked-flash"),
		deepSeekToolsRoutingAccount(15, 5, PlatformDeepseek, "disabled-flash"),
		deepSeekToolsRoutingAccount(16, 6, PlatformDeepseek, "composite-flash"),
		deepSeekToolsRoutingAccount(17, 7, PlatformDeepseek, "unbound-flash"),
	)
	repo := &modelsListAccountRepoStub{}
	svc := &OpenAIGatewayService{schedulerSnapshot: snapshot, accountRepo: repo}
	key := smartRouteCoreKey(groups[:6]...)
	key.User.AllowedGroups = nil // Public groups remain allowed; exclusive group 4 is revoked.
	originalIDs := append([]int64(nil), key.RouteGroupIDs...)
	prepared, models, err := svc.PrepareDeepSeekToolsRouting(t.Context(), key, "")
	require.NoError(t, err)
	require.Equal(t, []int64{2, 3}, prepared.CandidateGroupIDs())
	require.Equal(t, int64(2), *prepared.GroupID)
	require.Equal(t, []string{"deepseek-v4.1-flash", "deepseek-v4.1-pro", "deepseek-v5-flash", "deepseek-v5-pro"}, models)
	require.NotContains(t, models, "deepseek-v4-flash", "do not invent a default or use another platform's mapping")
	require.Zero(t, repo.listByGroupCalls.Load(), "preparation must read snapshots, not scan account rows")
	require.NotSame(t, key, prepared)
	require.Equal(t, originalIDs, key.RouteGroupIDs)
	require.Equal(t, int64(1), *key.GroupID)
	prepared.RouteGroupIDs[0] = 99
	require.Equal(t, originalIDs, key.RouteGroupIDs, "prepared route IDs must not alias the authenticated key")
}

func TestPrepareDeepSeekToolsRoutingExplicitModelStillNarrowsPlatform(t *testing.T) {
	groups := []*Group{
		{ID: 1, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true},
		{ID: 2, Platform: PlatformDeepseek, Status: StatusActive, Hydrated: true, ModelAllowlist: GroupModelAllowlist{
			Enabled: true, Models: []string{"another-model"},
		}},
	}
	snapshot, cache := newSmartRouteCoreSnapshot(groups)
	repo := &modelsListAccountRepoStub{}
	svc := &OpenAIGatewayService{schedulerSnapshot: snapshot, accountRepo: repo}
	key := smartRouteCoreKey(groups...)
	prepared, models, err := svc.PrepareDeepSeekToolsRouting(t.Context(), key, "  caller-model  ")
	require.NoError(t, err)
	require.Equal(t, []int64{2}, prepared.CandidateGroupIDs())
	require.Equal(t, []string{"caller-model"}, models, "selection, not preparation, enforces explicit-model allowlists")
	require.Zero(t, cache.reads.Load(), "explicit model does not need a model catalog")
	require.Zero(t, repo.listByGroupCalls.Load())
}

func TestPrepareDeepSeekToolsRoutingNoEligibleCandidates(t *testing.T) {
	groups := []*Group{
		{ID: 1, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true},
		{ID: 2, Platform: PlatformDeepseek, Status: StatusActive, Hydrated: true, IsExclusive: true},
	}
	snapshot, _ := newSmartRouteCoreSnapshot(groups)
	repo := &modelsListAccountRepoStub{}
	svc := &OpenAIGatewayService{schedulerSnapshot: snapshot, accountRepo: repo}
	key := smartRouteCoreKey(groups...)
	key.User.AllowedGroups = nil
	prepared, models, err := svc.PrepareDeepSeekToolsRouting(t.Context(), key, "deepseek-v4.1-flash")
	require.ErrorIs(t, err, ErrNoAvailableAccounts)
	require.Nil(t, prepared)
	require.Empty(t, models)
	require.Zero(t, repo.listByGroupCalls.Load())
}

func TestPrepareDeepSeekToolsRoutingColdCatalogNeverFallsBackToDatabase(t *testing.T) {
	group := &Group{ID: 2, Platform: PlatformDeepseek, Status: StatusActive, Hydrated: true}
	snapshot, _ := newSmartRouteCoreSnapshot([]*Group{group})
	repo := &modelsListAccountRepoStub{}
	svc := &OpenAIGatewayService{schedulerSnapshot: snapshot, accountRepo: repo}
	prepared, models, err := svc.PrepareDeepSeekToolsRouting(t.Context(), smartRouteCoreKey(group), "")
	require.ErrorIs(t, err, ErrNoAvailableAccounts)
	require.Nil(t, prepared)
	require.Empty(t, models)
	require.Zero(t, repo.listByGroupCalls.Load())

	// A service without a published snapshot must not switch to repo listing.
	svc.schedulerSnapshot = nil
	_, _, err = svc.PrepareDeepSeekToolsRouting(context.Background(), smartRouteCoreKey(group), "")
	require.ErrorIs(t, err, ErrNoAvailableAccounts)
	require.Zero(t, repo.listByGroupCalls.Load())
}
