//go:build unit

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const hostedChatFetchPageURL = "https://example.com/deepseek-fetch"
const hostedChatFetchContent = "# Fetched page\n\nThis full page contains the fetch-only verification phrase."

// Feed Chat Completions responses through the actual Forward conversion and
// hosted continuation loop. The provider endpoints remain independent JSON
// requests, using the group's tool credential on both search and fetch.
type hostedChatFetchUpstream struct {
	modelPaths     []string
	modelRequests  [][]byte
	searchRequests [][]byte
	fetchRequests  [][]byte
	providerKeys   []string
}

func (u *hostedChatFetchUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	kind, response := "application/json", ""
	switch req.URL.Path {
	case DeepSeekToolsPathSearch:
		u.searchRequests = append(u.searchRequests, body)
		u.providerKeys = append(u.providerKeys, req.Header.Get("Authorization"))
		response = `{"results":[{"title":"Page to read","url":"https://example.com/deepseek-fetch","summary":"A short search summary without the page verification phrase."}]}`
	case DeepSeekToolsPathFetch:
		u.fetchRequests = append(u.fetchRequests, body)
		u.providerKeys = append(u.providerKeys, req.Header.Get("Authorization"))
		encoded, _ := json.Marshal(map[string]string{"title": "Fetched page", "content": hostedChatFetchContent})
		response = string(encoded)
	case "/v1/chat/completions":
		u.modelPaths = append(u.modelPaths, req.URL.Path)
		u.modelRequests = append(u.modelRequests, body)
		round := len(u.modelRequests)
		if round > 3 {
			return nil, fmt.Errorf("unexpected Chat fetch continuation %d", round)
		}
		response = hostedChatFetchJSON(round)
		if gjson.GetBytes(body, "stream").Bool() {
			kind, response = "text/event-stream", hostedChatFetchSSE(round)
		}
	default:
		return nil, fmt.Errorf("unexpected hosted Chat fetch path %q", req.URL.Path)
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{kind}}, Body: io.NopCloser(strings.NewReader(response))}, nil
}

func (u *hostedChatFetchUpstream) DoWithTLS(req *http.Request, proxy string, id int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, id, concurrency)
}

func hostedChatFetchCall(round int) (name, callID, arguments string) {
	if round == 1 {
		return deepSeekHostedSearchToolName, "call_search_before_fetch", `{"query":"DeepSeek fetch guide","count":1}`
	}
	return deepSeekHostedFetchToolName, "call_fetch_page", `{"url":"https://example.com/deepseek-fetch"}`
}

func hostedChatFetchJSON(round int) string {
	message := map[string]any{"role": "assistant", "content": "The fetched page contains the fetch-only verification phrase."}
	finish := "stop"
	if round < 3 {
		name, callID, arguments := hostedChatFetchCall(round)
		finish = "tool_calls"
		message = map[string]any{"role": "assistant", "content": nil, "tool_calls": []any{
			map[string]any{"id": callID, "type": "function", "function": map[string]any{"name": name, "arguments": arguments}},
		}}
	}
	raw, _ := json.Marshal(map[string]any{
		"id": fmt.Sprintf("chatcmpl_fetch_%d", round), "object": "chat.completion", "created": 1793000000,
		"model": "deepseek-v4.1-flash", "choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": finish}},
		"usage": map[string]any{"prompt_tokens": round * 10, "completion_tokens": round + 1, "total_tokens": round*10 + round + 1},
	})
	return string(raw)
}

func hostedChatFetchSSE(round int) string {
	var out strings.Builder
	emit := func(choices []any, usage map[string]any) {
		frame := map[string]any{"id": fmt.Sprintf("chatcmpl_fetch_%d", round), "object": "chat.completion.chunk", "created": 1793000000, "model": "deepseek-v4.1-flash", "choices": choices}
		if usage != nil {
			frame["usage"] = usage
		}
		raw, _ := json.Marshal(frame)
		fmt.Fprintf(&out, "data: %s\n\n", raw)
	}
	chunk := func(delta map[string]any, finish any) {
		emit([]any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}, nil)
	}
	chunk(map[string]any{"role": "assistant"}, nil)
	finish := "stop"
	if round < 3 {
		name, callID, arguments := hostedChatFetchCall(round)
		finish = "tool_calls"
		chunk(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": callID, "type": "function", "function": map[string]any{"name": name, "arguments": ""}}}}, nil)
		// Deliberately split inside a JSON string, as actual Chat streams do.
		split := len(arguments) / 2
		for _, fragment := range []string{arguments[:split], arguments[split:]} {
			chunk(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "function": map[string]any{"arguments": fragment}}}}, nil)
		}
	} else {
		chunk(map[string]any{"content": "The fetched page contains "}, nil)
		chunk(map[string]any{"content": "the fetch-only verification phrase."}, nil)
	}
	chunk(map[string]any{}, finish)
	emit([]any{}, map[string]any{"prompt_tokens": round * 10, "completion_tokens": round + 1, "total_tokens": round*10 + round + 1})
	_, _ = out.WriteString("data: [DONE]\n\n")
	return out.String()
}

func hostedChatFetchPairedToolResult(t *testing.T, request []byte, callID, toolName string) string {
	t.Helper()
	var assistantCalls, toolResults int
	var result string
	for _, message := range gjson.GetBytes(request, "messages").Array() {
		if message.Get("role").String() == "assistant" {
			for _, call := range message.Get("tool_calls").Array() {
				if call.Get("id").String() == callID {
					assistantCalls++
					require.Equal(t, toolName, call.Get("function.name").String())
					require.True(t, gjson.Valid(call.Get("function.arguments").String()), "fragmented tool arguments must be reassembled before continuation")
				}
			}
		}
		if message.Get("role").String() == "tool" && message.Get("tool_call_id").String() == callID {
			toolResults++
			result = message.Get("content").String()
		}
	}
	require.Equal(t, 1, assistantCalls, "the continuation must retain exactly one assistant call for %s", callID)
	require.Equal(t, 1, toolResults, "the continuation must pair exactly one server result for %s", callID)
	return result
}

func TestDeepSeekHostedFetchChatSearchFetchThenSummary(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			upstream := &hostedChatFetchUpstream{}
			svc, c, recorder, account, body := hostedSearchFixture(t, &hostedSearchUpstream{}, stream, "auto", false)
			svc.httpUpstream = upstream
			account.Credentials["api_protocol"] = APIProtocolChatCompletions
			body, err := sjson.SetBytes(body, "tool_choice", map[string]string{"type": "web_search"})
			require.NoError(t, err)

			result, err := svc.Forward(context.Background(), c, account, body)
			require.NoError(t, err, recorder.Body.String())
			require.NotNil(t, result)
			require.Equal(t, []string{"/v1/chat/completions", "/v1/chat/completions", "/v1/chat/completions"}, upstream.modelPaths)
			require.Len(t, upstream.modelRequests, 3)
			require.Len(t, upstream.searchRequests, 1)
			require.Len(t, upstream.fetchRequests, 1)
			require.Equal(t, "DeepSeek fetch guide", gjson.GetBytes(upstream.searchRequests[0], "query").String())
			require.Equal(t, hostedChatFetchPageURL, gjson.GetBytes(upstream.fetchRequests[0], "url").String())
			apiKey := c.MustGet("api_key").(*APIKey)
			require.NotNil(t, apiKey.Group.SearchProviderAPIKey)
			expectedAuthorization := "Bearer " + *apiKey.Group.SearchProviderAPIKey
			require.Equal(t, []string{expectedAuthorization, expectedAuthorization}, upstream.providerKeys)
			for _, request := range upstream.modelRequests {
				require.False(t, gjson.GetBytes(request, "input").Exists(), "the model account must retain Chat Completions protocol")
				var fetchDeclared bool
				for _, tool := range gjson.GetBytes(request, "tools").Array() {
					fetchDeclared = fetchDeclared || tool.Get("function.name").String() == deepSeekHostedFetchToolName
				}
				require.True(t, fetchDeclared, "the model must be able to open the search result in subsequent rounds")
			}
			require.Equal(t, deepSeekHostedSearchToolName, gjson.GetBytes(upstream.modelRequests[0], "tool_choice.function.name").String())
			require.Equal(t, "auto", gjson.GetBytes(upstream.modelRequests[1], "tool_choice").String())
			searchResult := hostedChatFetchPairedToolResult(t, upstream.modelRequests[1], "call_search_before_fetch", deepSeekHostedSearchToolName)
			require.Equal(t, hostedChatFetchPageURL, gjson.Get(searchResult, "results.0.url").String())
			hostedChatFetchPairedToolResult(t, upstream.modelRequests[2], "call_search_before_fetch", deepSeekHostedSearchToolName)
			fetchResult := hostedChatFetchPairedToolResult(t, upstream.modelRequests[2], "call_fetch_page", deepSeekHostedFetchToolName)
			require.Equal(t, "Fetched page", gjson.Get(fetchResult, "title").String())
			require.Equal(t, hostedChatFetchContent, gjson.Get(fetchResult, "content").String())
			require.False(t, gjson.Get(fetchResult, "truncated").Bool())
			require.Equal(t, 2, result.SearchCount, "one successful search and one successful fetch add two tool charges")
			require.Zero(t, result.WebSearchCalls, "tool surcharges must retain three rounds of model token billing")
			require.Equal(t, 60, result.Usage.InputTokens)
			require.Equal(t, 9, result.Usage.OutputTokens)
			final := hostedSearchFinal(t, recorder, stream)
			require.Equal(t, "completed", final.Get("status").String())
			require.Len(t, final.Get("output").Array(), 3)
			require.Equal(t, "web_search_call", final.Get("output.0.type").String())
			require.Equal(t, "search", final.Get("output.0.action.type").String())
			require.Equal(t, "web_search_call", final.Get("output.1.type").String())
			require.Equal(t, "open_page", final.Get("output.1.action.type").String())
			require.Equal(t, hostedChatFetchPageURL, final.Get("output.1.action.url").String())
			require.Equal(t, "completed", final.Get("output.1.status").String())
			require.Equal(t, "message", final.Get("output.2.type").String())
			require.Contains(t, final.Get("output.2.content.0.text").String(), "fetch-only verification phrase")
			require.Equal(t, int64(60), final.Get("usage.input_tokens").Int())
			require.Equal(t, int64(9), final.Get("usage.output_tokens").Int())
			require.NotContains(t, recorder.Body.String(), `"name":"`+deepSeekHostedSearchToolName+`"`)
			require.NotContains(t, recorder.Body.String(), `"name":"`+deepSeekHostedFetchToolName+`"`)
			if stream {
				require.Equal(t, 1, strings.Count(recorder.Body.String(), "event: response.completed\n"))
				require.NotContains(t, recorder.Body.String(), "event: response.failed\n")
			}
		})
	}
}
