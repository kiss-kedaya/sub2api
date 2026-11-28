package service

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Neutral platforms and model aliases retain their existing body-shape dedupe.
// The native DeepSeek exemption must not change that compatibility behavior.
func TestRewriteInboundDeepSeekCallIDs_NeutralScopeStillDedupes(t *testing.T) {
	callID := "call_00_NGDdtohYPFUNxkT8wU2X4174"
	body := []byte(`{"model":"some-alias","input":[` +
		`{"type":"function_call","id":"` + callID + `","call_id":"` + callID + `","name":"shell","arguments":"{\"cmd\":\"pwd\"}"},` +
		`{"type":"function_call","id":"` + callID + `","call_id":"` + callID + `","name":"shell","arguments":""},` +
		`{"type":"function_call_output","call_id":"` + callID + `","output":"/tmp"}]}`)

	out := RewriteInboundDeepSeekCallIDs(PlatformOpenAI, "some-alias", body)
	items := gjson.GetBytes(out, "input").Array()
	require.Len(t, items, 2, "duplicate function_call must be dropped")
	require.Equal(t, "{\"cmd\":\"pwd\"}", items[0].Get("arguments").String())
	require.NotEqual(t, callID, items[0].Get("id").String(), "id must not equal call_id")
}

// Bodies without tool calls must not be touched, so the wider trigger does not
// rewrite unrelated traffic.
func TestRewriteInboundDeepSeekCallIDs_NoToolCallsUntouched(t *testing.T) {
	body := []byte(`{"model":"some-alias","input":[{"type":"message","role":"user","content":"hi"}]}`)
	out := RewriteInboundDeepSeekCallIDs(PlatformOpenAI, "some-alias", body)
	require.Equal(t, string(body), string(out))
}

// Explicit DeepSeek routing preserves duplicate IDs and invalid tool arguments
// so native upstream validation reports the original error to the client.
func TestRewriteInboundDeepSeekCallIDs_ExplicitDeepSeekPreservesNativeHistory(t *testing.T) {
	t.Parallel()
	for _, scope := range []struct {
		name     string
		platform string
		model    string
	}{
		{"platform-with-alias", PlatformDeepseek, "some-alias"},
		{"model-on-openai-platform", PlatformOpenAI, "deepseek-flash"},
		{"model-without-platform", "", "deepseek-v4-pro"},
		{"platform-and-model", PlatformDeepseek, "deepseek-v4.1-flash"},
	} {
		t.Run(scope.name, func(t *testing.T) {
			for _, tc := range []struct {
				name string
				body string
			}{
				{"responses-duplicate-pairs", `{"model":"some-alias","input":[{"type":"function_call","id":"call_same","call_id":"call_same","name":"lookup","arguments":"{}"},{"type":"function_call","id":"call_same","call_id":"call_same","name":"lookup","arguments":"{\"q\":7}"},{"type":"function_call_output","call_id":"call_same","output":"first"},{"type":"function_call_output","call_id":"call_same","output":"second"}],"opaque":9007199254740993}`},
				{"chat-duplicate-pairs", `{"model":"some-alias","messages":[{"role":"assistant","tool_calls":[{"type":"function","id":"call_same","function":{"name":"lookup","arguments":"{}"}},{"type":"function","id":"call_same","function":{"name":"lookup","arguments":"{\"q\":7}"}}]},{"role":"tool","tool_call_id":"call_same","content":"first"},{"role":"tool","tool_call_id":"call_same","content":"second"}]}`},
				{"invalid-tool-arguments", `{"model":"some-alias","input":[{"type":"function_call","id":"call_same","call_id":"call_same","name":"lookup","arguments":"{"},{"type":"function_call","id":"call_same","call_id":"call_same","name":"lookup","arguments":"{\"q\":7}"}]}`},
				{"invalid-json", `{"input":[{"call_id":"call_same"}`},
			} {
				t.Run(tc.name, func(t *testing.T) {
					body := []byte(tc.body)
					out := RewriteInboundDeepSeekCallIDs(scope.platform, scope.model, body)
					require.Equal(t, tc.body, string(out), "native history must remain byte-for-byte intact, including invalid data")
					require.Equal(t, tc.body, string(body), "the caller's input buffer must not be mutated")
				})
			}
		})
	}
}

func TestBodyHasToolCallIDs(t *testing.T) {
	require.True(t, bodyHasToolCallIDs([]byte(`{"input":[{"call_id":"x"}]}`)))
	require.True(t, bodyHasToolCallIDs([]byte(`{"messages":[{"tool_calls":[]}]}`)))
	require.False(t, bodyHasToolCallIDs([]byte(`{"messages":[{"role":"user"}]}`)))
	require.False(t, bodyHasToolCallIDs(nil))
}

// Regression for KVV k3_tool_result_matches_recent_turn: Kimi (and other
// non-DeepSeek platforms/models) legitimately reuses a tool_call_id ("dup")
// across distinct turns. DeepSeek call-id dedupe must never drop the second
// turn's assistant tool_calls + tool message.
func TestRewriteInboundDeepSeekCallIDs_PreservesKimiKVVRepeatedToolCallID(t *testing.T) {
	t.Parallel()
	kvvPayload := `{"model":"kimi-k3.1-pro","messages":[` +
		`{"role":"user","content":"Run both tools and report the latest result."},` +
		`{"role":"assistant","tool_calls":[{"id":"dup","type":"function","function":{"name":"legacy_lookup_with_a_much_longer_name","arguments":"{\"key\":\"old\"}"}}]},` +
		`{"role":"tool","tool_call_id":"dup","content":"old_result"},` +
		`{"role":"assistant","tool_calls":[{"id":"dup","type":"function","function":{"name":"f_new","arguments":"{\"key\":\"new\"}"}}]},` +
		`{"role":"tool","tool_call_id":"dup","content":"new_result"},` +
		`{"role":"user","content":"Which tool produced the latest result?"}]}`

	scopes := []struct {
		name     string
		platform string
		model    string
		payload  string
	}{
		{"kimi-platform-and-model", PlatformKimi, "kimi-k3.1-pro", kvvPayload},
		{"openai-platform-kimi-model", PlatformOpenAI, "kimi-k3.1-pro", kvvPayload},
		{"composite-platform-kimi-model", PlatformComposite, "kimi-k3.1-pro", kvvPayload},
		{"empty-platform-kimi-model", "", "kimi-k3.1-pro", kvvPayload},
		{"kimi-platform-opaque-alias", PlatformKimi, "some-alias", kvvPayload},
		{"kimi-platform-opaque-body-model", PlatformKimi, "some-alias", `{"model":"some-alias","messages":[{"role":"assistant","tool_calls":[{"id":"dup","type":"function","function":{"name":"legacy_lookup_with_a_much_longer_name","arguments":"{}"}}]},{"role":"tool","tool_call_id":"dup","content":"old"},{"role":"assistant","tool_calls":[{"id":"dup","type":"function","function":{"name":"f_new","arguments":"{}"}}]},{"role":"tool","tool_call_id":"dup","content":"new"}]}`},
		{"zhipu-platform", PlatformZhipu, "glm-5", `{"model":"glm-5","messages":[{"role":"assistant","tool_calls":[{"id":"dup","type":"function","function":{"name":"legacy_lookup_with_a_much_longer_name","arguments":"{}"}}]},{"role":"tool","tool_call_id":"dup","content":"old"},{"role":"assistant","tool_calls":[{"id":"dup","type":"function","function":{"name":"f_new","arguments":"{}"}}]},{"role":"tool","tool_call_id":"dup","content":"new"}]}`},
		{"minimax-platform", PlatformMiniMax, "minimax-m2", `{"model":"minimax-m2","messages":[{"role":"assistant","tool_calls":[{"id":"dup","type":"function","function":{"name":"legacy_lookup_with_a_much_longer_name","arguments":"{}"}}]},{"role":"tool","tool_call_id":"dup","content":"old"},{"role":"assistant","tool_calls":[{"id":"dup","type":"function","function":{"name":"f_new","arguments":"{}"}}]},{"role":"tool","tool_call_id":"dup","content":"new"}]}`},
		{"grok-platform", PlatformGrok, "grok-4", `{"model":"grok-4","messages":[{"role":"assistant","tool_calls":[{"id":"dup","type":"function","function":{"name":"legacy_lookup_with_a_much_longer_name","arguments":"{}"}}]},{"role":"tool","tool_call_id":"dup","content":"old"},{"role":"assistant","tool_calls":[{"id":"dup","type":"function","function":{"name":"f_new","arguments":"{}"}}]},{"role":"tool","tool_call_id":"dup","content":"new"}]}`},
		{"openai-platform-gpt-5", PlatformOpenAI, "gpt-5", `{"model":"gpt-5","messages":[{"role":"assistant","tool_calls":[{"id":"dup","type":"function","function":{"name":"legacy_lookup_with_a_much_longer_name","arguments":"{}"}}]},{"role":"tool","tool_call_id":"dup","content":"old"},{"role":"assistant","tool_calls":[{"id":"dup","type":"function","function":{"name":"f_new","arguments":"{}"}}]},{"role":"tool","tool_call_id":"dup","content":"new"}]}`},
		{"openai-platform-claude", PlatformOpenAI, "claude-sonnet-4", `{"model":"claude-sonnet-4","messages":[{"role":"assistant","tool_calls":[{"id":"dup","type":"function","function":{"name":"legacy_lookup_with_a_much_longer_name","arguments":"{}"}}]},{"role":"tool","tool_call_id":"dup","content":"old"},{"role":"assistant","tool_calls":[{"id":"dup","type":"function","function":{"name":"f_new","arguments":"{}"}}]},{"role":"tool","tool_call_id":"dup","content":"new"}]}`},
	}

	for _, sc := range scopes {
		t.Run(sc.name, func(t *testing.T) {
			body := []byte(sc.payload)
			out := RewriteInboundDeepSeekCallIDs(sc.platform, sc.model, body)
			require.Equal(t, sc.payload, string(out), "non-DeepSeek multi-turn repeated tool_call_id must stay byte-for-byte intact")
			require.Contains(t, string(out), "legacy_lookup_with_a_much_longer_name")
			require.Contains(t, string(out), "f_new")
		})
	}
}

func TestNormalizeDeepSeekResponsesRequestBody_KimiAndDeepSeekURLRelayScoping(t *testing.T) {
	t.Parallel()
	kvvChatPayload := []byte(`{"model":"kimi-k3.1-pro","messages":[` +
		`{"role":"user","content":"hi"},` +
		`{"role":"assistant","tool_calls":[{"id":"dup","type":"function","function":{"name":"legacy_lookup_with_a_much_longer_name","arguments":"{\"key\":\"old\"}"}}]},` +
		`{"role":"tool","tool_call_id":"dup","content":"old_result"},` +
		`{"role":"assistant","tool_calls":[{"id":"dup","type":"function","function":{"name":"f_new","arguments":"{\"key\":\"new\"}"}}]},` +
		`{"role":"tool","tool_call_id":"dup","content":"new_result"}]}`)

	t.Run("kimi account with direct IP base_url preserves KVV turns", func(t *testing.T) {
		acc := &Account{
			Platform: PlatformKimi,
			Type:     AccountTypeAPIKey,
			Credentials: map[string]any{
				"base_url": "https://198.51.100.10:8443/v1",
			},
		}
		require.False(t, deepSeekDuplicateCallIDRewrite(acc, kvvChatPayload))
		out := normalizeDeepSeekResponsesRequestBody(acc, kvvChatPayload)
		require.Equal(t, string(kvvChatPayload), string(out))
	})

	t.Run("kimi account whose relay URL contains deepseek preserves KVV turns", func(t *testing.T) {
		acc := &Account{
			Platform: PlatformKimi,
			Type:     AccountTypeAPIKey,
			Credentials: map[string]any{
				"base_url": "https://platform.deepseek.example/v1",
			},
		}
		require.False(t, deepSeekDuplicateCallIDRewrite(acc, kvvChatPayload))
		out := normalizeDeepSeekResponsesRequestBody(acc, kvvChatPayload)
		require.Equal(t, string(kvvChatPayload), string(out))
	})

	t.Run("openai relay whose URL contains deepseek still preserves kimi model", func(t *testing.T) {
		acc := &Account{
			Platform: PlatformOpenAI,
			Type:     AccountTypeAPIKey,
			Credentials: map[string]any{
				"base_url": "https://platform.deepseek.example/v1",
			},
		}
		require.False(t, deepSeekDuplicateCallIDRewrite(acc, kvvChatPayload))
		out := normalizeDeepSeekResponsesRequestBody(acc, kvvChatPayload)
		require.Equal(t, string(kvvChatPayload), string(out))
	})

	t.Run("openai relay whose URL contains deepseek preserves alias mapped to kimi", func(t *testing.T) {
		acc := &Account{
			Platform: PlatformOpenAI,
			Type:     AccountTypeAPIKey,
			Credentials: map[string]any{
				"base_url":      "https://platform.deepseek.example/v1",
				"model_mapping": map[string]any{"custom-k3": "kimi-k3.1-pro"},
			},
		}
		body := []byte(`{"model":"custom-k3","messages":[{"role":"assistant","tool_calls":[{"id":"dup","type":"function","function":{"name":"legacy_lookup_with_a_much_longer_name","arguments":"{}"}}]},{"role":"tool","tool_call_id":"dup","content":"old"},{"role":"assistant","tool_calls":[{"id":"dup","type":"function","function":{"name":"f_new","arguments":"{}"}}]},{"role":"tool","tool_call_id":"dup","content":"new"}]}`)
		require.False(t, deepSeekDuplicateCallIDRewrite(acc, body))
		require.Equal(t, string(body), string(normalizeDeepSeekResponsesRequestBody(acc, body)))
	})

	t.Run("openai oauth gpt-5 with tool calls preserves reasoning.summary", func(t *testing.T) {
		acc := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}
		body := []byte(`{"model":"gpt-5","reasoning":{"effort":"high","summary":"auto"},"input":[{"type":"function_call","call_id":"call_1","name":"f","arguments":"{}"}]}`)
		require.False(t, deepSeekDuplicateCallIDRewrite(acc, body))
		require.Equal(t, string(body), string(normalizeDeepSeekResponsesRequestBody(acc, body)))
	})

	t.Run("openai relay with deepseek base URL and neutral alias keeps dedupe", func(t *testing.T) {
		acc := &Account{
			Platform: PlatformOpenAI,
			Type:     AccountTypeAPIKey,
			Credentials: map[string]any{
				"base_url": "https://api.deepseek.com/v1",
			},
		}
		body := []byte(`{"model":"some-alias","input":[{"type":"function_call","call_id":"call_a","name":"do"},{"type":"function_call","call_id":"call_a","name":"do"},{"type":"function_call_output","call_id":"call_a","output":"1"}]}`)
		require.True(t, deepSeekDuplicateCallIDRewrite(acc, body))
		out := normalizeDeepSeekResponsesRequestBody(acc, body)
		require.Len(t, gjson.GetBytes(out, "input").Array(), 2)
	})

	t.Run("openai relay serving deepseek model keeps dedupe", func(t *testing.T) {
		acc := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
		body := []byte(`{"model":"deepseek-v4.1-flash","input":[{"type":"function_call","call_id":"call_a","name":"do"},{"type":"function_call","call_id":"call_a","name":"do"},{"type":"function_call_output","call_id":"call_a","output":"1"}]}`)
		require.True(t, deepSeekDuplicateCallIDRewrite(acc, body))
	})

	t.Run("multi-segment vendor prefixes and kimi variants are explicit non-DeepSeek", func(t *testing.T) {
		for _, model := range []string{
			"accounts/fireworks/models/kimi-k2",
			"openrouter/moonshotai/k2.5",
			"openrouter/anthropic/claude-sonnet-4",
			"kimi_k2",
			"k2p5",
			"k3p1",
		} {
			require.True(t, isExplicitNonDeepSeekModel(model), "model=%q", model)
		}
		require.False(t, isExplicitNonDeepSeekModel("openrouter/deepseek/deepseek-chat"))
		require.False(t, isExplicitNonDeepSeekModel("some-alias"))
	})

	t.Run("explicit account model_mapping to kimi overrides deepseek-named client alias", func(t *testing.T) {
		acc := &Account{
			Platform: PlatformOpenAI,
			Type:     AccountTypeAPIKey,
			Credentials: map[string]any{
				"base_url":      "https://platform.deepseek.example/v1",
				"model_mapping": map[string]any{"deepseek-alias": "kimi-k3.1-pro"},
			},
		}
		body := []byte(`{"model":"deepseek-alias","messages":[{"role":"assistant","tool_calls":[{"id":"dup","type":"function","function":{"name":"legacy_lookup_with_a_much_longer_name","arguments":"{}"}}]},{"role":"tool","tool_call_id":"dup","content":"old"},{"role":"assistant","tool_calls":[{"id":"dup","type":"function","function":{"name":"f_new","arguments":"{}"}}]},{"role":"tool","tool_call_id":"dup","content":"new"}]}`)
		require.False(t, deepSeekDuplicateCallIDRewrite(acc, body))
		require.Equal(t, string(body), string(normalizeDeepSeekResponsesRequestBody(acc, body)))
	})
}

func TestRewriteDeepSeekCallIDsForAccount_PreservesMappedAliasesAndNativeDeepSeek(t *testing.T) {
	t.Parallel()
	kvvChatPayload := []byte(`{"model":"custom-k3","messages":[` +
		`{"role":"assistant","tool_calls":[{"id":"dup","type":"function","function":{"name":"legacy_lookup_with_a_much_longer_name","arguments":"{\"key\":\"old\"}"}}]},` +
		`{"role":"tool","tool_call_id":"dup","content":"old_result"},` +
		`{"role":"assistant","tool_calls":[{"id":"dup","type":"function","function":{"name":"f_new","arguments":"{\"key\":\"new\"}"}}]},` +
		`{"role":"tool","tool_call_id":"dup","content":"new_result"}]}`)

	t.Run("openai account mapping opaque alias to kimi preserves both turns even with deepseek relay URL", func(t *testing.T) {
		acc := &Account{
			Platform: PlatformOpenAI,
			Type:     AccountTypeAPIKey,
			Credentials: map[string]any{
				"base_url":      "https://platform.deepseek.example/v1",
				"model_mapping": map[string]any{"custom-k3": "kimi-k3.1-pro"},
			},
		}
		out := RewriteDeepSeekCallIDsForAccount(acc, kvvChatPayload)
		require.Equal(t, string(kvvChatPayload), string(out))
		require.Contains(t, string(out), "legacy_lookup_with_a_much_longer_name")
		require.Contains(t, string(out), "f_new")
	})

	t.Run("openai account with neutral non-deepseek URL and opaque alias preserves both turns", func(t *testing.T) {
		acc := &Account{
			Platform: PlatformOpenAI,
			Type:     AccountTypeAPIKey,
			Credentials: map[string]any{
				"base_url": "https://api.openai.com/v1",
			},
		}
		out := RewriteDeepSeekCallIDsForAccount(acc, kvvChatPayload)
		require.Equal(t, string(kvvChatPayload), string(out))
	})

	t.Run("native deepseek account preserves duplicate history intact", func(t *testing.T) {
		acc := &Account{Platform: PlatformDeepseek, Type: AccountTypeAPIKey}
		out := RewriteDeepSeekCallIDsForAccount(acc, kvvChatPayload)
		require.Equal(t, string(kvvChatPayload), string(out))
	})

	t.Run("openai relay mapping opaque alias to deepseek still dedupes", func(t *testing.T) {
		acc := &Account{
			Platform: PlatformOpenAI,
			Type:     AccountTypeAPIKey,
			Credentials: map[string]any{
				"base_url":      "https://relay.example/v1",
				"model_mapping": map[string]any{"custom-k3": "deepseek-chat"},
			},
		}
		out := RewriteDeepSeekCallIDsForAccount(acc, kvvChatPayload)
		require.Len(t, gjson.GetBytes(out, "messages").Array(), 2)
	})

	t.Run("channel model override to kimi suppresses dedupe on deepseek URL relay", func(t *testing.T) {
		acc := &Account{
			Platform: PlatformOpenAI,
			Type:     AccountTypeAPIKey,
			Credentials: map[string]any{
				"base_url": "https://platform.deepseek.example/v1",
			},
		}
		out := RewriteDeepSeekCallIDsForAccount(acc, kvvChatPayload, "kimi-k3.1-pro")
		require.Equal(t, string(kvvChatPayload), string(out))
	})
}
