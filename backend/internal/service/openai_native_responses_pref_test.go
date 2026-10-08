package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/stretchr/testify/require"
)

func TestPrioritizeNativeResponsesAccountsKeepsConvertersBehind(t *testing.T) {
	chat := &Account{ID: 1, Platform: PlatformDeepseek, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_protocol": APIProtocolChatCompletions}}
	native := &Account{ID: 2, Platform: PlatformDeepseek, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_protocol": APIProtocolResponses}}
	forceChat := &Account{ID: 3, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: map[string]any{
		openai_compat.ExtraKeyResponsesMode: string(openai_compat.ResponsesSupportModeForceChatCompletions),
	}}
	oauth := &Account{ID: 4, Platform: PlatformOpenAI, Type: AccountTypeOAuth}

	require.False(t, openAIAccountServesInboundResponsesNatively(chat))
	require.True(t, openAIAccountServesInboundResponsesNatively(native))
	require.False(t, openAIAccountServesInboundResponsesNatively(forceChat))
	require.True(t, openAIAccountServesInboundResponsesNatively(oauth))
	require.False(t, openAIAccountServesInboundResponsesNatively(nil))

	ordered := prioritizeNativeResponsesAccounts([]*Account{chat, native, forceChat, oauth})
	require.Equal(t, []int64{2, 4, 1, 3}, []int64{ordered[0].ID, ordered[1].ID, ordered[2].ID, ordered[3].ID})

	same := prioritizeNativeResponsesAccounts([]*Account{chat, forceChat})
	require.Equal(t, []int64{1, 3}, []int64{same[0].ID, same[1].ID})

	loaded := prioritizeNativeResponsesLoaded([]accountWithLoad{
		{account: chat},
		{account: native},
	})
	require.Equal(t, int64(2), loaded[0].account.ID)
	require.Equal(t, int64(1), loaded[1].account.ID)
}

func TestStickyYieldsWhenInboundResponsesWouldConvert(t *testing.T) {
	chat := &Account{ID: 29159, Platform: PlatformDeepseek, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_protocol": APIProtocolChatCompletions}}
	native := &Account{ID: 29161, Platform: PlatformDeepseek, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_protocol": APIProtocolResponses}}
	ctx := WithOpenAIPreferNativeResponses(context.Background())
	require.True(t, openAIStickyYieldsToNativeResponses(ctx, chat))
	require.False(t, openAIStickyYieldsToNativeResponses(ctx, native))
	require.False(t, openAIStickyYieldsToNativeResponses(context.Background(), chat))
}
