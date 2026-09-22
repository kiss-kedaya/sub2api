package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenAICatalogModels_UsesMappedGeminiKeys(t *testing.T) {
	gid := int64(32)
	svc := &OpenAIGatewayService{
		accountRepo: &modelsListAccountRepoStub{
			byGroup: map[int64][]Account{
				gid: {{
					ID:       29143,
					Platform: PlatformOpenAI,
					Credentials: map[string]any{
						"model_mapping": map[string]any{
							"gemini-3.8-flash": "gemini-3.8-flash",
						},
					},
				}},
			},
		},
	}

	require.Equal(t, []string{"gemini-3.8-flash"}, svc.catalogModels(context.Background(), &gid, PlatformOpenAI))
	require.Equal(t, groupCatalogModelPresent, groupCatalogHasRequestedModelWith(context.Background(), gid, "gemini-3.8-flash", svc.catalogModels))
}

func TestSelectAlongKeyRoutes_SkipsEmptyOpenAIWhenSiblingMapsGemini(t *testing.T) {
	emptyID := int64(1)
	geminiID := int64(32)
	svc := &OpenAIGatewayService{
		accountRepo: &modelsListAccountRepoStub{
			byGroup: map[int64][]Account{
				emptyID: {{
					ID:       10,
					Platform: PlatformOpenAI,
				}},
				geminiID: {{
					ID:       29143,
					Platform: PlatformOpenAI,
					Credentials: map[string]any{
						"model_mapping": map[string]any{
							"gemini-3.8-flash": "gemini-3.8-flash",
						},
					},
				}},
			},
		},
	}
	primary := emptyID
	groups := []*Group{
		{ID: emptyID, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true},
		{ID: geminiID, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true},
	}
	repo, ok := svc.accountRepo.(*modelsListAccountRepoStub)
	require.True(t, ok)
	var accounts []*Account
	for gid, items := range repo.byGroup {
		for _, item := range items {
			item.GroupIDs = []int64{gid}
			accounts = append(accounts, &item)
		}
	}
	svc.schedulerSnapshot, _ = newSmartRouteCoreSnapshot(groups, accounts...)
	key := &APIKey{User: &User{}, GroupID: &primary, Group: groups[0], RouteGroupIDs: []int64{emptyID, geminiID}}

	var tried []int64
	_, _, _, err := svc.selectAlongKeyRoutes(context.Background(), key, []string{PlatformOpenAI}, "gemini-3.8-flash", func(_ context.Context, groupID *int64, _ []string, _ string) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
		tried = append(tried, *groupID)
		return nil, OpenAIAccountScheduleDecision{}, ErrNoAvailableAccounts
	})
	require.ErrorIs(t, err, ErrNoAvailableAccounts)
	require.Equal(t, []int64{geminiID}, tried)
}

func TestSelectAlongKeyRoutes_TriesGrokGroupWhenCatalogMissesNewGrokModel(t *testing.T) {
	openaiID := int64(43)
	grokID := int64(25)
	groups := []*Group{
		{ID: openaiID, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true},
		{ID: grokID, Platform: PlatformGrok, Status: StatusActive, Hydrated: true},
	}
	openaiAcc := Account{
		ID: 10, Platform: PlatformOpenAI, GroupIDs: []int64{openaiID},
		Credentials: map[string]any{"model_mapping": map[string]any{"gpt-5.4": "gpt-5.4"}},
	}
	grokAcc := Account{
		ID: 29131, Platform: PlatformGrok, GroupIDs: []int64{grokID},
		Credentials: map[string]any{"model_mapping": map[string]any{"grok-4.6": "grok-4.6"}},
	}
	svc := &OpenAIGatewayService{
		accountRepo: &modelsListAccountRepoStub{
			byGroup: map[int64][]Account{
				openaiID: {openaiAcc},
				grokID:   {grokAcc},
			},
		},
	}
	svc.schedulerSnapshot, _ = newSmartRouteCoreSnapshot(groups, &openaiAcc, &grokAcc)
	key := &APIKey{User: &User{}, GroupID: &openaiID, Group: groups[0], RouteGroupIDs: []int64{openaiID, grokID}}

	var tried []int64
	_, _, _, err := svc.selectAlongKeyRoutes(context.Background(), key, []string{PlatformOpenAI}, "grok-4.7", func(_ context.Context, groupID *int64, _ []string, _ string) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
		tried = append(tried, *groupID)
		return nil, OpenAIAccountScheduleDecision{}, ErrNoAvailableAccounts
	})
	require.ErrorIs(t, err, ErrNoAvailableAccounts)
	require.Equal(t, []int64{grokID}, tried)
}

func TestSelectAlongKeyRoutes_DeepSeekBackupClearsPrimaryProfitGate(t *testing.T) {
	openaiID := int64(43)
	deepseekID := int64(36)
	primary := &Group{ID: openaiID, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true, RateMultiplier: 0.18, ProfitControlEnabled: true}
	backup := &Group{ID: deepseekID, Platform: PlatformDeepseek, Status: StatusActive, Hydrated: true, RateMultiplier: 0.10, ProfitControlEnabled: true}
	openaiAcc := Account{
		ID: 10, Platform: PlatformOpenAI, GroupIDs: []int64{openaiID},
		Credentials: map[string]any{"model_mapping": map[string]any{"gpt-5.4": "gpt-5.4"}},
	}
	deepseekAcc := Account{
		ID: 29204, Platform: PlatformDeepseek, GroupIDs: []int64{deepseekID},
		Credentials: map[string]any{"model_mapping": map[string]any{"deepseek-v4.1-flash": "deepseek-v4.1-flash"}},
	}
	svc := &OpenAIGatewayService{
		accountRepo: &modelsListAccountRepoStub{
			byGroup: map[int64][]Account{
				openaiID:   {openaiAcc},
				deepseekID: {deepseekAcc},
			},
		},
	}
	svc.schedulerSnapshot, _ = newSmartRouteCoreSnapshot([]*Group{primary, backup}, &openaiAcc, &deepseekAcc)
	key := &APIKey{User: &User{}, GroupID: &openaiID, Group: primary, RouteGroupIDs: []int64{openaiID, deepseekID}}
	rate := 1.0
	account := &Account{ID: 29204, Platform: PlatformDeepseek, Type: AccountTypeAPIKey, RateMultiplier: &rate}

	ctx, _ := svc.WithOpenAIRequestPricingContext(profitControlTestCtx(primary), &openaiID)
	vetoed, _ := OpenAIProfitControlVeto(ctx, account)
	require.True(t, vetoed, "entry openai gate must veto deepseek account rate 1.0")

	selection, _, routed, err := svc.selectAlongKeyRoutes(ctx, key, []string{PlatformOpenAI}, "deepseek-v4.1-flash", func(routeCtx context.Context, groupID *int64, _ []string, _ string) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
		routeCtx = svc.withOpenAIProfitControlGate(routeCtx, groupID)
		if groupID != nil && *groupID == deepseekID {
			return attachSelectionProfitGate(routeCtx, &AccountSelectionResult{Account: account, Acquired: true}), OpenAIAccountScheduleDecision{}, nil
		}
		return nil, OpenAIAccountScheduleDecision{}, ErrNoAvailableAccounts
	})
	require.NoError(t, err)
	require.NotNil(t, routed)
	require.Equal(t, deepseekID, *routed.GroupID)
	require.True(t, selection.profitGateResolved)
	require.False(t, selection.ProfitGateActive())

	handlerCtx := ContextWithSelectionProfitGate(ctx, selection)
	vetoed, _ = OpenAIProfitControlVeto(handlerCtx, account)
	require.False(t, vetoed, "deepseek backup must not inherit group 43 profit veto")
}
