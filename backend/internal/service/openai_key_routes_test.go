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
	key := &APIKey{GroupID: &primary, RouteGroupIDs: []int64{emptyID, geminiID}}

	var tried []int64
	_, _, _, err := svc.selectAlongKeyRoutes(context.Background(), key, []string{PlatformOpenAI}, "gemini-3.8-flash", func(groupID *int64, _ []string) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
		tried = append(tried, *groupID)
		return nil, OpenAIAccountScheduleDecision{}, ErrNoAvailableAccounts
	})
	require.ErrorIs(t, err, ErrNoAvailableAccounts)
	require.Equal(t, []int64{geminiID}, tried)
}
