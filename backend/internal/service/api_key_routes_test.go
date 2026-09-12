package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCandidateGroupIDs(t *testing.T) {
	id := int64(7)
	key := &APIKey{GroupID: &id, RouteGroupIDs: []int64{7, 8, 8, 0, 9}}
	require.Equal(t, []int64{7, 8, 9}, key.CandidateGroupIDs())

	plain := &APIKey{GroupID: &id}
	require.Equal(t, []int64{7}, plain.CandidateGroupIDs())
}

func TestUsesRequestTargetPlatform(t *testing.T) {
	id := int64(7)
	require.False(t, (*APIKey)(nil).UsesRequestTargetPlatform())
	require.False(t, (&APIKey{GroupID: &id, Group: &Group{ID: 7, Platform: PlatformAnthropic}}).UsesRequestTargetPlatform())
	require.True(t, (&APIKey{
		GroupID:       &id,
		RouteGroupIDs: []int64{7, 8},
		Group:         &Group{ID: 7, Platform: PlatformAnthropic},
	}).UsesRequestTargetPlatform())
	require.True(t, (&APIKey{Group: &Group{Platform: PlatformComposite}}).UsesRequestTargetPlatform())
}

func TestGroupUsableForRequest(t *testing.T) {
	require.True(t, groupUsableForRequest(nil, PlatformOpenAI, "gpt-5"))
	require.True(t, groupUsableForRequest(&Group{Platform: PlatformAnthropic}, PlatformAnthropic, "claude-sonnet-4-6"))
	require.False(t, groupUsableForRequest(&Group{Platform: PlatformAnthropic}, PlatformOpenAI, "gpt-5"))
	require.True(t, groupUsableForRequest(&Group{Platform: PlatformOpenAI}, PlatformOpenAI, "gpt-5"))
	require.True(t, groupUsableForRequest(&Group{Platform: PlatformComposite}, PlatformOpenAI, "gpt-5"))

	restricted := &Group{
		Platform:         PlatformOpenAI,
		ModelAllowlist: GroupModelsListConfig{Enabled: true, Models: []string{"gpt-5"}},
	}
	require.True(t, groupUsableForRequest(restricted, PlatformOpenAI, "gpt-5"))
	require.False(t, groupUsableForRequest(restricted, PlatformOpenAI, "gpt-5.4"))

	geminiLabeled := &Group{Platform: PlatformGemini}
	require.False(t, groupUsableForRequest(geminiLabeled, PlatformOpenAI, "gemini-3.8-flash"))
	require.True(t, groupUsableForRequest(geminiLabeled, PlatformOpenAI, "gemini-3.8-flash", map[string]struct{}{PlatformOpenAI: {}}))
	require.True(t, groupUsableForRequest(geminiLabeled, PlatformGemini, "gemini-3.8-flash"))
}

func TestGroupAllowsRequestedModel(t *testing.T) {
	require.True(t, groupAllowsRequestedModel(nil, "gpt-5"))
	require.True(t, groupAllowsRequestedModel(&Group{}, "gpt-5"))

	restricted := &Group{ModelAllowlist: GroupModelsListConfig{Enabled: true, Models: []string{"gpt-5", "codex-mini"}}}
	require.True(t, groupAllowsRequestedModel(restricted, "gpt-5"))
	require.True(t, groupAllowsRequestedModel(restricted, "GPT-5"))
	require.False(t, groupAllowsRequestedModel(restricted, "claude-opus-4"))
	require.False(t, groupAllowsRequestedModel(restricted, ""))

	grokList := &Group{ModelAllowlist: GroupModelsListConfig{Enabled: true, Models: []string{"grok-4.6", "grok-*"}}}
	require.True(t, groupAllowsRequestedModel(grokList, "grok-4.6"))
	require.True(t, groupAllowsRequestedModel(grokList, "grok-4.5"))
	require.False(t, groupAllowsRequestedModel(grokList, "gemini-3.8-flash"))
	require.False(t, groupAllowsRequestedModel(grokList, "__mirasim_wire_probe_model__"))
}

func TestIsOpenAICompatibleUpstreamPlatform(t *testing.T) {
	require.True(t, isOpenAICompatibleUpstreamPlatform("openai"))
	require.True(t, isOpenAICompatibleUpstreamPlatform("grok"))
	require.True(t, isOpenAICompatibleUpstreamPlatform("kimi"))
	require.True(t, isOpenAICompatibleUpstreamPlatform("zhipu"))
	require.True(t, isOpenAICompatibleUpstreamPlatform("deepseek"))
	require.True(t, isOpenAICompatibleUpstreamPlatform("minimax"))
	require.False(t, isOpenAICompatibleUpstreamPlatform("anthropic"))
	require.False(t, isOpenAICompatibleUpstreamPlatform("gemini"))
}

func TestNormalizeAPIKeyGroupIDs(t *testing.T) {
	first := int64(1)
	ids, primary, err := normalizeAPIKeyGroupIDs(&first, []int64{1, 2, 3})
	require.NoError(t, err)
	require.Equal(t, []int64{1, 2, 3}, ids)
	require.Equal(t, int64(1), *primary)

	_, _, err = normalizeAPIKeyGroupIDs(&first, []int64{2, 1})
	require.Error(t, err)

	_, _, err = normalizeAPIKeyGroupIDs(nil, []int64{1, 1})
	require.Error(t, err)

	many := make([]int64, 30)
	for i := range many {
		many[i] = int64(i + 1)
	}
	ids, primary, err = normalizeAPIKeyGroupIDs(nil, many)
	require.NoError(t, err)
	require.Equal(t, many, ids)
	require.Equal(t, int64(1), *primary)
}

func TestShouldContinueAlongKeyRoutes(t *testing.T) {
	require.True(t, shouldContinueAlongKeyRoutes(ErrNoAvailableAccounts))
	require.True(t, shouldContinueAlongKeyRoutes(ErrSchedulerCacheNotReady))
	require.True(t, shouldContinueAlongKeyRoutes(ErrClaudeCodeOnly))
	require.True(t, shouldContinueAlongKeyRoutes(ErrNoAvailableCompactAccounts))
	require.True(t, shouldContinueAlongKeyRoutes(ErrGroupNotFound))
	require.False(t, shouldContinueAlongKeyRoutes(nil))
	require.False(t, shouldContinueAlongKeyRoutes(ErrAPIKeyNotFound))
}

func TestHydrateAPIKeyGroupRequiresFullGroup(t *testing.T) {
	id := int64(1)
	key := &APIKey{GroupID: &id, Group: &Group{ID: 1, RateMultiplier: 1}}

	out, err := hydrateAPIKeyGroup(context.Background(), key, 1, nil)
	require.NoError(t, err)
	require.Equal(t, key, out)

	_, err = hydrateAPIKeyGroup(context.Background(), key, 2, nil)
	require.ErrorIs(t, err, ErrSchedulerCacheNotReady)

	out, err = hydrateAPIKeyGroup(context.Background(), key, 2, func(_ context.Context, gid int64) (*Group, error) {
		return &Group{ID: gid, RateMultiplier: 2}, nil
	})
	require.NoError(t, err)
	require.Equal(t, int64(2), *out.GroupID)
	require.Equal(t, 2.0, out.Group.RateMultiplier)
	require.Equal(t, 1.0, key.Group.RateMultiplier)
}

func TestGroupPlatformFitsRequest(t *testing.T) {
	require.True(t, groupPlatformFitsRequest("anthropic", "anthropic"))
	require.False(t, groupPlatformFitsRequest("openai", "anthropic"))
	require.False(t, groupPlatformFitsRequest("anthropic", "openai"))
	require.True(t, groupPlatformFitsRequest("antigravity", "anthropic"))
	require.True(t, groupPlatformFitsRequest("", "openai"))
}

func TestModelsAdmitRequestedModel(t *testing.T) {
	require.True(t, modelsAdmitRequestedModel([]string{"gemini-3.8-flash"}, "gemini-3.8-flash"))
	require.False(t, modelsAdmitRequestedModel([]string{"grok-4.6", "grok-4.5"}, "gemini-3.8-flash"))
	require.True(t, modelsAdmitRequestedModel([]string{"gpt-*"}, "gpt-5.4"))
	require.False(t, modelsAdmitRequestedModel(nil, "gemini-3.8-flash"))
	require.False(t, modelsAdmitRequestedModel([]string{"grok-4.6"}, ""))
}

func TestGroupCatalogHasRequestedModel_SkipsGrokForGeminiFlash(t *testing.T) {
	grokID := int64(1)
	geminiID := int64(2)
	svc := &GatewayService{
		accountRepo: &modelsListAccountRepoStub{
			byGroup: map[int64][]Account{
				grokID: {{
					ID:       10,
					Platform: PlatformGrok,
				}},
				geminiID: {{
					ID:       20,
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

	require.Equal(t, groupCatalogModelAbsent, svc.groupCatalogHasRequestedModel(context.Background(), grokID, "gemini-3.8-flash"))
	require.Equal(t, groupCatalogModelPresent, svc.groupCatalogHasRequestedModel(context.Background(), geminiID, "gemini-3.8-flash"))
	require.Equal(t, groupCatalogModelAbsent, svc.groupCatalogHasRequestedModel(context.Background(), grokID, "__mirasim_wire_probe_model__"))
	require.Equal(t, groupCatalogModelAbsent, svc.groupCatalogHasRequestedModel(context.Background(), grokID, ""))
}

func TestGroupCatalogUsableForRequest_OpenAICompatibleGrokDoesNotClaimGemini(t *testing.T) {
	grokID := int64(1)
	svc := &GatewayService{
		accountRepo: &modelsListAccountRepoStub{
			byGroup: map[int64][]Account{
				grokID: {{ID: 10, Platform: PlatformGrok}},
			},
		},
		groupRepo: &groupLookupHotpathRepoStub{
			group: &Group{
				ID:               grokID,
				Platform:         PlatformGrok,
				ModelAllowlist: GroupModelsListConfig{Enabled: true, Models: []string{"grok-4.6"}},
			},
		},
	}

	require.False(t, svc.groupCatalogUsableForRequest(context.Background(), grokID, PlatformOpenAI, "gemini-3.8-flash"))
	require.True(t, svc.groupCatalogUsableForRequest(context.Background(), grokID, PlatformOpenAI, "grok-4.6"))
	require.False(t, svc.groupCatalogUsableForRequest(context.Background(), grokID, PlatformOpenAI, ""))
	require.False(t, svc.groupCatalogUsableForRequest(context.Background(), grokID, PlatformOpenAI, "__mirasim_wire_probe_model__"))
}

func TestUpstreamPlatformForModel_DoesNotGuessFromModelName(t *testing.T) {
	openaiID := int64(1)
	svc := &GatewayService{
		accountRepo: &modelsListAccountRepoStub{
			byGroup: map[int64][]Account{
				openaiID: {{
					ID:       10,
					Platform: PlatformOpenAI,
					Credentials: map[string]any{
						"model_mapping": map[string]any{"gpt-5.4": "gpt-5.4"},
					},
				}},
			},
		},
	}
	key := &APIKey{GroupID: &openaiID, Group: &Group{ID: openaiID, Platform: PlatformOpenAI}}

	platform, ok := svc.UpstreamPlatformForModel(context.Background(), key, "grok-imagine-video-1.5")
	require.False(t, ok)
	require.Empty(t, platform)

	platform, ok = svc.UpstreamPlatformForModel(context.Background(), key, "gpt-5.4")
	require.True(t, ok)
	require.Equal(t, PlatformOpenAI, platform)
}

func TestUpstreamPlatformForModel_GeminiGroupOpenAIAccount(t *testing.T) {
	geminiID := int64(2)
	svc := &GatewayService{
		accountRepo: &modelsListAccountRepoStub{
			byGroup: map[int64][]Account{
				geminiID: {{
					ID:       20,
					Platform: PlatformOpenAI,
					Credentials: map[string]any{
						"model_mapping": map[string]any{"gemini-3.8-flash": "gemini-3.8-flash"},
					},
				}},
			},
		},
	}
	key := &APIKey{GroupID: &geminiID, Group: &Group{ID: geminiID, Platform: PlatformGemini}}

	platform, ok := svc.UpstreamPlatformForModel(context.Background(), key, "gemini-3.8-flash")
	require.True(t, ok)
	require.Equal(t, PlatformOpenAI, platform)
}

func TestSkipKeyRouteForCatalog(t *testing.T) {
	require.True(t, skipKeyRouteForCatalog(groupCatalogModelAbsent, false))
	require.True(t, skipKeyRouteForCatalog(groupCatalogModelAbsent, true))
	require.False(t, skipKeyRouteForCatalog(groupCatalogModelUnknown, false))
	require.True(t, skipKeyRouteForCatalog(groupCatalogModelUnknown, true))
	require.False(t, skipKeyRouteForCatalog(groupCatalogModelPresent, false))
	require.False(t, skipKeyRouteForCatalog(groupCatalogModelPresent, true))
}

func TestShouldTryKeyRouteGroup_EmptyOpenAIDoesNotStealMappedGemini(t *testing.T) {
	emptyID := int64(1)
	geminiID := int64(2)
	svc := &GatewayService{
		accountRepo: &modelsListAccountRepoStub{
			byGroup: map[int64][]Account{
				emptyID: {{
					ID:       10,
					Platform: PlatformOpenAI,
				}},
				geminiID: {{
					ID:       20,
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

	require.Equal(t, groupCatalogModelUnknown, svc.groupCatalogHasRequestedModel(context.Background(), emptyID, "gemini-3.8-flash"))
	require.Equal(t, groupCatalogModelPresent, svc.groupCatalogHasRequestedModel(context.Background(), geminiID, "gemini-3.8-flash"))
	require.True(t, keyRouteSiblingHasCatalogedModel(context.Background(), []int64{emptyID, geminiID}, "gemini-3.8-flash", svc.GetAvailableModels))
	require.False(t, svc.shouldTryKeyRouteGroup(context.Background(), emptyID, PlatformOpenAI, "gemini-3.8-flash", true))
	require.True(t, svc.shouldTryKeyRouteGroup(context.Background(), geminiID, PlatformOpenAI, "gemini-3.8-flash", true))
	require.True(t, svc.shouldTryKeyRouteGroup(context.Background(), emptyID, PlatformOpenAI, "gemini-3.8-flash", false))
}

func TestUpstreamPlatformForModel_OpenAIGroupGeminiMapping(t *testing.T) {
	openaiID := int64(32)
	svc := &GatewayService{
		accountRepo: &modelsListAccountRepoStub{
			byGroup: map[int64][]Account{
				openaiID: {{
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
	key := &APIKey{GroupID: &openaiID, Group: &Group{ID: openaiID, Platform: PlatformOpenAI}}

	platform, ok := svc.UpstreamPlatformForModel(context.Background(), key, "gemini-3.8-flash")
	require.True(t, ok)
	require.Equal(t, PlatformOpenAI, platform)
}
