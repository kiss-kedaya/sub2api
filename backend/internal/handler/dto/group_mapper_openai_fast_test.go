package dto

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestGroupMapperExposesForceOpenAIFastOnlyToAdmins(t *testing.T) {
	group := &service.Group{
		ID: 7, Name: "fast", Platform: service.PlatformOpenAI, Status: service.StatusActive, ForceOpenAIFast: true, FreeOpenAIFast: true,
	}

	userJSON, err := json.Marshal(GroupFromService(group))
	require.NoError(t, err)
	require.NotContains(t, string(userJSON), "force_openai_fast")
	require.NotContains(t, string(userJSON), "free_openai_fast")

	adminJSON, err := json.Marshal(GroupFromServiceAdmin(group))
	require.NoError(t, err)
	require.Contains(t, string(adminJSON), `"force_openai_fast":true`)
	require.Contains(t, string(adminJSON), `"free_openai_fast":true`)
}

func TestGroupMapperExposesOpenAITierMultipliersOnlyToAdmins(t *testing.T) {
	fast, ultrafast := 1.0, 2.5
	group := &service.Group{
		ID: 7, Name: "tier", Platform: service.PlatformOpenAI, Status: service.StatusActive,
		OpenAIFastMultiplier: &fast, OpenAIUltrafastMultiplier: &ultrafast,
	}

	userJSON, err := json.Marshal(GroupFromService(group))
	require.NoError(t, err)
	require.NotContains(t, string(userJSON), "openai_fast_multiplier")
	require.NotContains(t, string(userJSON), "openai_ultrafast_multiplier")

	adminJSON, err := json.Marshal(GroupFromServiceAdmin(group))
	require.NoError(t, err)
	require.Contains(t, string(adminJSON), `"openai_fast_multiplier":1`)
	require.Contains(t, string(adminJSON), `"openai_ultrafast_multiplier":2.5`)

	// Not configured: the admin payload says null (official price), never 0.
	adminJSON, err = json.Marshal(GroupFromServiceAdmin(&service.Group{ID: 8, Platform: service.PlatformOpenAI}))
	require.NoError(t, err)
	require.Contains(t, string(adminJSON), `"openai_fast_multiplier":null`)
	require.Contains(t, string(adminJSON), `"openai_ultrafast_multiplier":null`)
}
