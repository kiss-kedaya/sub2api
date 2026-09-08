package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAccountsSupportingRequestedModel_EmptyMappingDoesNotStealMappedSibling(t *testing.T) {
	empty := Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{}}
	mapped := Account{
		ID:       2,
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"model_mapping": map[string]any{"gemini-3.8-flash": "gemini-3.8-flash"},
		},
	}

	got := accountsSupportingRequestedModel([]Account{empty, mapped}, "gemini-3.8-flash")
	require.Len(t, got, 1)
	require.Equal(t, int64(2), got[0].ID)
}

func TestAccountsSupportingRequestedModel_EmptyMappingFallbackWhenNobodyMapped(t *testing.T) {
	empty := Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{}}
	got := accountsSupportingRequestedModel([]Account{empty}, "gemini-3.8-flash")
	require.Len(t, got, 1)
	require.Equal(t, int64(1), got[0].ID)
}
