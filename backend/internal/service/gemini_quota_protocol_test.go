package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGeminiCustomProtocolSkipsAIStudioQuota(t *testing.T) {
	t.Parallel()

	native := &Account{
		Platform:    PlatformGemini,
		Type:        AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "gemini-key"},
	}
	require.Equal(t, GeminiTierAIStudioFree, geminiQuotaTierKeyForAccount(native))

	custom := &Account{
		Platform: PlatformGemini,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":      "gemini-key",
			"base_url":     "https://mdkj.lol",
			"api_protocol": APIProtocolResponses,
		},
	}
	require.Equal(t, "", geminiQuotaTierKeyForAccount(custom))

	quota, ok := NewGeminiQuotaService(nil, nil).QuotaForAccount(context.Background(), custom)
	require.False(t, ok)
	require.Equal(t, GeminiQuota{}, quota)
}
