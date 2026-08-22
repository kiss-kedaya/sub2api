package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// Scenario: mixed groups prefer capability metadata synced for the routed account.
func TestBuildCodexModelsManifestForGroupUsesSyncedAccountMetadata(t *testing.T) {
	t.Parallel()

	const groupID int64 = 735
	account := Account{
		ID:       25,
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"base_url":      "https://opencode.ai/zen/v1",
			"model_mapping": map[string]any{"x-preview-f-free": "x-preview-f-free"},
		},
		Extra: map[string]any{
			UpstreamModelMetadataExtraKey: map[string]any{
				"source": "models.dev",
				"models": map[string]any{
					"x-preview-f-free": map[string]any{
						"id":                         "x-preview-f-free",
						"display_name":               "Ox Alpha Free (Unlimited)",
						"description":                "Stealth reasoning model",
						"reasoning":                  true,
						"supported_reasoning_levels": []any{"low", "high", "max"},
						"input_modalities":           []any{"text", "image"},
						"context_window":             float64(1_000_000),
						"max_output_tokens":          float64(131_072),
					},
				},
			},
		},
	}
	svc := &GatewayService{accountRepo: codexModelsVisibilityAccountRepo{byGroup: map[int64][]Account{
		groupID: {account},
	}}}

	body, err := svc.BuildCodexModelsManifestForGroup(
		context.Background(),
		&Group{ID: groupID, Platform: PlatformComposite},
		"",
		[]string{"x-preview-f-free"},
	)
	require.NoError(t, err)

	models := decodeCodexManifestModels(t, body)
	require.Len(t, models, 1)
	require.Equal(t, "Ox Alpha Free (Unlimited)", models[0]["display_name"])
	require.Equal(t, "low", models[0]["default_reasoning_level"])
	require.Equal(t, []string{"low", "high", "max"}, effortsFromManifestModel(t, models[0]))
	require.Equal(t, []any{"text", "image"}, models[0]["input_modalities"])
	require.EqualValues(t, 1_000_000, models[0]["context_window"])
}

// Scenario: an explicitly non-reasoning model remains directly selectable in Codex.
func TestBuildCodexModelsManifestForGroupUsesNoneForExplicitNonReasoningMetadata(t *testing.T) {
	t.Parallel()

	const groupID int64 = 737
	reasoning := false
	account := Account{
		ID: 28, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{
			"base_url":      "https://provider.example/v1",
			"model_mapping": map[string]any{"company-coding-model": "company-coding-model"},
		},
	}
	account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Models: map[string]UpstreamModelMetadata{
		"company-coding-model": {
			ID: "company-coding-model", Reasoning: &reasoning,
			InputModalities: []string{"text"}, ContextWindow: 64_000,
		},
	}})
	svc := &GatewayService{accountRepo: codexModelsVisibilityAccountRepo{byGroup: map[int64][]Account{
		groupID: {account},
	}}}

	body, err := svc.BuildCodexModelsManifestForGroup(
		context.Background(), &Group{ID: groupID, Platform: PlatformComposite}, "", []string{"company-coding-model"},
	)
	require.NoError(t, err)
	models := decodeCodexManifestModels(t, body)
	require.Len(t, models, 1)
	require.Equal(t, "none", models[0]["default_reasoning_level"])
	require.Equal(t, []string{"none"}, effortsFromManifestModel(t, models[0]))
}

// Scenario: multiple schedulable accounts advertise only their shared capabilities.
func TestBuildCodexModelsManifestForGroupIntersectsSyncedAccountMetadata(t *testing.T) {
	t.Parallel()

	const groupID int64 = 736
	reasoning := true
	newAccount := func(id int64, levels, modalities []string, contextWindow int64) Account {
		account := Account{
			ID: id, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
			Credentials: map[string]any{
				"base_url":      "https://provider.example/v1",
				"model_mapping": map[string]any{"shared-model": "shared-model"},
			},
		}
		account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Models: map[string]UpstreamModelMetadata{
			"shared-model": {
				ID: "shared-model", Reasoning: &reasoning,
				SupportedReasoningLevels: levels,
				InputModalities:          modalities,
				ContextWindow:            contextWindow,
			},
		}})
		return account
	}
	svc := &GatewayService{accountRepo: codexModelsVisibilityAccountRepo{byGroup: map[int64][]Account{
		groupID: {
			newAccount(26, []string{"low", "high"}, []string{"text", "image"}, 256_000),
			newAccount(27, []string{"high", "max"}, []string{"text"}, 128_000),
		},
	}}}

	body, err := svc.BuildCodexModelsManifestForGroup(
		context.Background(), &Group{ID: groupID, Platform: PlatformComposite}, "", []string{"shared-model"},
	)
	require.NoError(t, err)
	models := decodeCodexManifestModels(t, body)
	require.Len(t, models, 1)
	require.Equal(t, []string{"high"}, effortsFromManifestModel(t, models[0]))
	require.Equal(t, "high", models[0]["default_reasoning_level"])
	require.Equal(t, []any{"text"}, models[0]["input_modalities"])
	require.EqualValues(t, 128_000, models[0]["context_window"])
}

func TestBuildCodexModelsManifestForGroupDoesNotAdvertiseNoneWhenAccountReasoningConflicts(t *testing.T) {
	t.Parallel()

	const groupID int64 = 738
	reasoning := true
	noReasoning := false
	newAccount := func(id int64, metadata UpstreamModelMetadata) Account {
		account := Account{
			ID: id, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
			Credentials: map[string]any{
				"base_url":      "https://provider.example/v1",
				"model_mapping": map[string]any{"shared-model": "shared-model"},
			},
		}
		account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Models: map[string]UpstreamModelMetadata{
			"shared-model": metadata,
		}})
		return account
	}
	svc := &GatewayService{accountRepo: codexModelsVisibilityAccountRepo{byGroup: map[int64][]Account{
		groupID: {
			newAccount(29, UpstreamModelMetadata{
				ID: "shared-model", Reasoning: &reasoning,
				SupportedReasoningLevels: []string{"low", "high"},
				InputModalities:          []string{"text"}, ContextWindow: 128_000,
			}),
			newAccount(30, UpstreamModelMetadata{
				ID: "shared-model", Reasoning: &noReasoning,
				InputModalities: []string{"text"}, ContextWindow: 128_000,
			}),
		},
	}}}

	body, err := svc.BuildCodexModelsManifestForGroup(
		context.Background(), &Group{ID: groupID, Platform: PlatformComposite}, "", []string{"shared-model"},
	)
	require.NoError(t, err)
	models := decodeCodexManifestModels(t, body)
	require.Len(t, models, 1)
	_, hasDefault := models[0]["default_reasoning_level"]
	require.False(t, hasDefault)
	require.Empty(t, models[0]["supported_reasoning_levels"])
}
