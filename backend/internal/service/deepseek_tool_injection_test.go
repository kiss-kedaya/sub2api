package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func TestDeepSeekToolInjectionKeySettingControlsLegacyRequestBuilders(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		for _, setting := range []string{"default", "enabled", "disabled", "other-platform"} {
			t.Run(fmt.Sprintf("passthrough=%t/%s", passthrough, setting), func(t *testing.T) {
				svc, c, _, account, _ := hostedSearchFixture(t, &hostedSearchUpstream{}, false, "auto", false)
				svc.cfg.Gateway.DeepSeekResponseCompatEnabled = true
				key, ok := c.MustGet("api_key").(*APIKey)
				require.True(t, ok)
				if setting != "default" {
					enabled := setting != "disabled"
					key.DeepSeekToolInjectionEnabled = &enabled
				}
				if setting == "other-platform" {
					account.Platform = PlatformOpenAI
				}
				body := []byte(`{"model":"deepseek-v4.1-flash","input":"hi","tools":[{"type":"function","name":"client_lookup","parameters":{"type":"object"}}]}`)
				var req *http.Request
				var err error
				if passthrough {
					req, err = svc.buildUpstreamRequestOpenAIPassthrough(context.Background(), c, account, body, "model-test-token")
				} else {
					req, err = svc.buildUpstreamRequest(context.Background(), c, account, body, "model-test-token", false, "", false)
				}
				require.NoError(t, err)
				upstream, err := io.ReadAll(req.Body)
				require.NoError(t, err)
				require.NoError(t, req.Body.Close())
				require.JSONEq(t, gjson.GetBytes(body, "tools.0").Raw, gjson.GetBytes(upstream, "tools.0").Raw)
				wantInjection := setting == "default" || setting == "enabled"
				if wantInjection {
					require.Len(t, gjson.GetBytes(upstream, "tools").Array(), 2)
					require.Equal(t, deepSeekSearchToolName, gjson.GetBytes(upstream, "tools.1.function.name").String())
				} else {
					require.Len(t, gjson.GetBytes(upstream, "tools").Array(), 1)
				}
			})
		}
	}
}

func TestDeepSeekToolInjectionHostedRequestDefaultAndExplicitSettings(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, setting := range []string{"default", "enabled", "disabled"} {
			t.Run(fmt.Sprintf("stream=%t/%s", stream, setting), func(t *testing.T) {
				upstream := &hostedSearchUpstream{modelResponses: [][]byte{hostedSearchResponse("resp_answer", 10, 2, hostedSearchAnswer())}}
				svc, c, recorder, account, body := hostedSearchFixture(t, upstream, stream, "auto", true)
				if setting != "default" {
					enabled := setting == "enabled"
					key, ok := c.MustGet("api_key").(*APIKey)
					require.True(t, ok)
					key.DeepSeekToolInjectionEnabled = &enabled
				}
				result, err := svc.Forward(context.Background(), c, account, body)
				require.NoError(t, err, recorder.Body.String())
				require.NotNil(t, result)
				require.Len(t, upstream.modelRequests, 1)
				require.Empty(t, upstream.searchRequests)
				require.Empty(t, upstream.fetchRequests)
				first := upstream.modelRequests[0]
				require.Equal(t, deepSeekHostedSearchToolName, gjson.GetBytes(first, "tools.0.name").String(), "the client-declared search remains supported")
				require.JSONEq(t, gjson.GetBytes(body, "tools.1").Raw, gjson.GetBytes(first, "tools.1").Raw)
				if setting == "disabled" {
					require.Len(t, gjson.GetBytes(first, "tools").Array(), 2)
					require.NotContains(t, gjson.GetBytes(first, "tools.0.description").String(), "web_fetch")
				} else {
					require.Len(t, gjson.GetBytes(first, "tools").Array(), 3)
					require.Equal(t, deepSeekHostedFetchToolName, gjson.GetBytes(first, "tools.2.name").String())
				}
			})
		}
	}
}

func TestDeepSeekToolInjectionDisabledPreservesDeclaredHostedSearchAndBilling(t *testing.T) {
	for _, chat := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("chat=%t/stream=%t", chat, stream), func(t *testing.T) {
				upstream := &hostedSearchUpstream{modelResponses: [][]byte{
					hostedSearchResponse("resp_search", 10, 2, hostedSearchCall()), hostedSearchResponse("resp_answer", 20, 3, hostedSearchAnswer()),
				}, searchResponse: `{"results":[{"title":"Docs","url":"https://example.com/search","summary":"Search result"}]}`}
				svc, c, recorder, account, body := hostedSearchFixture(t, upstream, stream, "auto", false)
				disabled := false
				key, ok := c.MustGet("api_key").(*APIKey)
				require.True(t, ok)
				key.DeepSeekToolInjectionEnabled = &disabled
				chatUpstream := &hostedChatSearchUpstream{}
				if chat {
					account.Credentials["api_protocol"] = APIProtocolChatCompletions
					svc.httpUpstream = chatUpstream
				}
				result, err := svc.Forward(context.Background(), c, account, body)
				require.NoError(t, err, recorder.Body.String())
				require.NotNil(t, result)
				require.Equal(t, 1, result.SearchCount)
				require.Zero(t, result.WebSearchCalls)
				require.Equal(t, 30, result.Usage.InputTokens)
				require.Equal(t, 5, result.Usage.OutputTokens)
				requests := upstream.modelRequests
				if chat {
					require.Len(t, chatUpstream.searchRequests, 1)
					requests = chatUpstream.modelRequests
				} else {
					require.Len(t, upstream.searchRequests, 1)
					require.Empty(t, upstream.fetchRequests)
				}
				require.Len(t, requests, 2)
				for _, request := range requests {
					require.Len(t, gjson.GetBytes(request, "tools").Array(), 1, "no additional Fetch tool in either protocol or continuation")
				}
				final := hostedSearchFinal(t, recorder, stream)
				require.Equal(t, "web_search_call", final.Get("output.0.type").String())
			})
		}
	}
}

func TestDeepSeekToolInjectionDisabledKeepsClientFetchFunctionClientOwned(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			clientFetch := hostedFetchCall("client_fetch", "https://example.com/page")
			upstream := &hostedSearchUpstream{modelResponses: [][]byte{hostedSearchResponse("resp_client", 10, 2, clientFetch)}}
			svc, c, recorder, account, body := hostedSearchFixture(t, upstream, stream, "auto", false)
			disabled := false
			key, ok := c.MustGet("api_key").(*APIKey)
			require.True(t, ok)
			key.DeepSeekToolInjectionEnabled = &disabled
			const clientTool = `{"type":"function","name":"web_fetch","description":"Client-owned fetch","parameters":{"type":"object","properties":{"url":{"type":"string"}}}}`
			body, err := sjson.SetRawBytes(body, "tools.-1", []byte(clientTool))
			require.NoError(t, err)
			result, err := svc.Forward(context.Background(), c, account, body)
			require.NoError(t, err, recorder.Body.String())
			require.NotNil(t, result)
			require.Len(t, upstream.modelRequests, 1)
			require.Len(t, gjson.GetBytes(upstream.modelRequests[0], "tools").Array(), 2)
			require.JSONEq(t, clientTool, gjson.GetBytes(upstream.modelRequests[0], "tools.1").Raw)
			require.Empty(t, upstream.searchRequests)
			require.Empty(t, upstream.fetchRequests, "a client function must never become a paid gateway fetch")
			require.Zero(t, result.SearchCount)
			final := hostedSearchFinal(t, recorder, stream)
			require.Equal(t, "function_call", final.Get("output.0.type").String())
			require.Equal(t, "web_fetch", final.Get("output.0.name").String())
			require.Equal(t, "client_fetch", final.Get("output.0.call_id").String())
		})
	}
}

func TestDeepSeekToolInjectionDisabledDoesNotDisableIndependentTools(t *testing.T) {
	for _, path := range []string{DeepSeekToolsPathSearch, DeepSeekToolsPathFetch} {
		t.Run(path, func(t *testing.T) {
			upstream := &hostedSearchUpstream{searchResponse: `{"results":[{"title":"Docs","url":"https://example.com/"}]}`, fetchResponse: `{"title":"Docs","content":"Page text"}`}
			svc, c, recorder, account, _ := hostedSearchFixture(t, upstream, false, "auto", false)
			disabled := false
			key, ok := c.MustGet("api_key").(*APIKey)
			require.True(t, ok)
			key.DeepSeekToolInjectionEnabled = &disabled
			body := []byte(`{"query":"Docs"}`)
			if path == DeepSeekToolsPathFetch {
				body = []byte(`{"url":"https://example.com/"}`)
			}
			result, err := svc.ForwardDeepSeekTools(context.Background(), c, account, key.Group, path, "", body)
			require.NoError(t, err, recorder.Body.String())
			require.NotNil(t, result)
			require.Equal(t, http.StatusOK, recorder.Code)
			require.Equal(t, 1, result.WebSearchCalls)
		})
	}
}

func TestDeepSeekToolInjectionDisabledLeavesLegacyClientSearchOutputUntouched(t *testing.T) {
	upstream := &hostedSearchUpstream{}
	svc, c, _, account, _ := hostedSearchFixture(t, upstream, false, "auto", false)
	disabled := false
	key, ok := c.MustGet("api_key").(*APIKey)
	require.True(t, ok)
	key.DeepSeekToolInjectionEnabled = &disabled
	group, enabled := deepSeekSearchGroupEnabled(c, account)
	require.True(t, enabled, "the key setting controls injection, not group search permission")
	body := hostedSearchResponse("resp_client_search", 10, 2, hostedSearchCall())
	next, changed := svc.applyDeepSeekSearchResults(c, account, group, body)
	require.False(t, changed)
	require.Equal(t, body, next)
	require.Empty(t, upstream.searchRequests, "do not run a client-owned tool merely because its name is web_search")
}

func TestDeepSeekToolInjectionDisabledCallLimitPreservesClientFetchChoice(t *testing.T) {
	body := []byte(`{"tools":[{"type":"function","name":"web_search"},{"type":"function","name":"web_fetch","description":"Client fetch"}],"tool_choice":{"type":"function","name":"web_fetch"}}`)
	next, err := deepSeekHostedRemoveToolWithFetch(body, false)
	require.NoError(t, err)
	require.Len(t, gjson.GetBytes(next, "tools").Array(), 1)
	require.JSONEq(t, gjson.GetBytes(body, "tools.1").Raw, gjson.GetBytes(next, "tools.0").Raw)
	require.JSONEq(t, gjson.GetBytes(body, "tool_choice").Raw, gjson.GetBytes(next, "tool_choice").Raw)
}
