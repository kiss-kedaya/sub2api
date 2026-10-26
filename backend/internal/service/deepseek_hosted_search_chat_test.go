package service

import (
	"bytes"
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
)

// Chat accounts must retain their configured protocol while hosted search owns
// the otherwise unsupported Responses server tool. Feed actual CC shapes into
// Forward so both protocol conversion legs participate in these regressions.
type hostedChatSearchUpstream struct {
	modelPaths     []string
	modelRequests  [][]byte
	searchRequests [][]byte
	searchKeys     []string
	searchStatus   int
}

func (u *hostedChatSearchUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	status, kind, response := http.StatusOK, "application/json", ""
	if strings.HasSuffix(req.URL.Path, "/tools/search") {
		u.searchRequests = append(u.searchRequests, body)
		u.searchKeys = append(u.searchKeys, req.Header.Get("Authorization"))
		response = `{"results":[{"title":"DeepSeek Search API","url":"https://example.com/deepseek-search","summary":"The dedicated search API returns structured web results."}]}`
		if u.searchStatus > 0 {
			status = u.searchStatus
			response = `{"error":{"message":"search provider unavailable"}}`
		}
	} else {
		u.modelPaths = append(u.modelPaths, req.URL.Path)
		u.modelRequests = append(u.modelRequests, body)
		round := len(u.modelRequests)
		if round > 2 {
			return nil, fmt.Errorf("unexpected Chat search continuation %d", round)
		}
		response = hostedChatSearchJSON(round)
		if gjson.GetBytes(body, "stream").Bool() {
			kind, response = "text/event-stream", hostedChatSearchSSE(round)
		}
	}
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{kind}}, Body: io.NopCloser(strings.NewReader(response))}, nil
}

func (u *hostedChatSearchUpstream) DoWithTLS(req *http.Request, proxy string, id int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, id, concurrency)
}

func hostedChatSearchJSON(round int) string {
	message := map[string]any{"role": "assistant", "content": "The dedicated search API returns structured web results."}
	finish := "stop"
	if round == 1 {
		finish = "tool_calls"
		message = map[string]any{"role": "assistant", "content": nil, "tool_calls": []any{
			map[string]any{"id": "call_search_chat", "type": "function", "function": map[string]any{
				"name": deepSeekHostedSearchToolName, "arguments": `{"query":"DeepSeek search API","count":1}`,
			}},
		}}
	}
	raw, _ := json.Marshal(map[string]any{
		"id": fmt.Sprintf("chatcmpl_search_%d", round), "object": "chat.completion", "created": 1793000000,
		"model": "deepseek-v4.1-flash", "choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": finish}},
		"usage": map[string]any{"prompt_tokens": round * 10, "completion_tokens": round + 1, "total_tokens": round*10 + round + 1},
	})
	return string(raw)
}

func hostedChatSearchSSE(round int) string {
	var out strings.Builder
	emit := func(choices []any, usage map[string]any) {
		frame := map[string]any{"id": fmt.Sprintf("chatcmpl_search_%d", round), "object": "chat.completion.chunk", "created": 1793000000, "model": "deepseek-v4.1-flash", "choices": choices}
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
	if round == 1 {
		finish = "tool_calls"
		chunk(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "call_search_chat", "type": "function", "function": map[string]any{"name": deepSeekHostedSearchToolName, "arguments": ""}}}}, nil)
		chunk(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "function": map[string]any{"arguments": `{"query":"DeepSeek `}}}}, nil)
		chunk(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "function": map[string]any{"arguments": `search API","count":1}`}}}}, nil)
	} else {
		chunk(map[string]any{"content": "The dedicated search API returns "}, nil)
		chunk(map[string]any{"content": "structured web results."}, nil)
	}
	chunk(map[string]any{}, finish)
	emit([]any{}, map[string]any{"prompt_tokens": round * 10, "completion_tokens": round + 1, "total_tokens": round*10 + round + 1})
	_, _ = out.WriteString("data: [DONE]\n\n")
	return out.String()
}

func TestDeepSeekHostedSearchChatAccountsExecuteAndResume(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			upstream := &hostedChatSearchUpstream{}
			svc, c, recorder, account, body := hostedSearchFixture(t, &hostedSearchUpstream{}, stream, "auto", false)
			svc.httpUpstream = upstream
			account.Credentials["api_protocol"] = APIProtocolChatCompletions
			body = bytes.Replace(body, []byte(`"tool_choice":"auto"`), []byte(`"tool_choice":{"type":"web_search"}`), 1)
			result, err := svc.Forward(context.Background(), c, account, body)
			require.NoError(t, err, recorder.Body.String())
			require.NotNil(t, result)
			require.Equal(t, []string{"/v1/chat/completions", "/v1/chat/completions"}, upstream.modelPaths, "hosted execution must not override the selected account's protocol")
			require.Len(t, upstream.modelRequests, 2)
			require.Len(t, upstream.searchRequests, 1)
			apiKey, ok := c.MustGet("api_key").(*APIKey)
			require.True(t, ok)
			searchProviderKey := apiKey.Group.SearchProviderAPIKey
			require.NotNil(t, searchProviderKey)
			expectedSearchAuthorization := "Bearer " + *searchProviderKey
			require.Equal(t, []string{expectedSearchAuthorization}, upstream.searchKeys)
			require.Equal(t, "DeepSeek search API", gjson.GetBytes(upstream.searchRequests[0], "query").String())
			first := upstream.modelRequests[0]
			require.False(t, gjson.GetBytes(first, "input").Exists())
			require.Equal(t, deepSeekHostedSearchToolName, gjson.GetBytes(first, "tools.0.function.name").String())
			require.Equal(t, "function", gjson.GetBytes(first, "tool_choice.type").String())
			require.Equal(t, deepSeekHostedSearchToolName, gjson.GetBytes(first, "tool_choice.function.name").String())
			var toolOutput gjson.Result
			for _, message := range gjson.GetBytes(upstream.modelRequests[1], "messages").Array() {
				if message.Get("role").String() == "tool" && message.Get("tool_call_id").String() == "call_search_chat" {
					toolOutput = message
				}
			}
			require.True(t, toolOutput.Exists(), "second CC request must pair the assistant call with an actual tool result")
			require.Contains(t, toolOutput.Get("content").String(), "structured web results")
			require.Equal(t, 1, result.SearchCount)
			require.Zero(t, result.WebSearchCalls, "search adds a surcharge and retains token billing")
			require.Equal(t, 30, result.Usage.InputTokens)
			require.Equal(t, 5, result.Usage.OutputTokens)
			final := hostedSearchFinal(t, recorder, stream)
			require.Equal(t, "completed", final.Get("status").String())
			require.Equal(t, "web_search_call", final.Get("output.0.type").String())
			require.Equal(t, "completed", final.Get("output.0.status").String())
			require.Equal(t, "https://example.com/deepseek-search", final.Get("output.0.action.sources.0.url").String())
			require.Equal(t, "message", final.Get("output.1.type").String())
			require.Contains(t, final.Get("output.1.content.0.text").String(), "structured web results")
			require.Equal(t, int64(30), final.Get("usage.input_tokens").Int())
			require.Equal(t, int64(5), final.Get("usage.output_tokens").Int())
			require.NotContains(t, recorder.Body.String(), deepSeekHostedSearchToolName)
		})
	}
}

func TestDeepSeekHostedSearchChatProviderFailurePreservesModelTokenBilling(t *testing.T) {
	upstream := &hostedChatSearchUpstream{searchStatus: http.StatusUnauthorized}
	svc, c, recorder, account, body := hostedSearchFixture(t, &hostedSearchUpstream{}, true, "auto", false)
	svc.httpUpstream = upstream
	account.Credentials["api_protocol"] = APIProtocolChatCompletions
	result, err := svc.Forward(context.Background(), c, account, body)
	require.NoError(t, err, recorder.Body.String())
	require.NotNil(t, result)
	require.Len(t, upstream.modelRequests, 2)
	require.Len(t, upstream.searchRequests, 1)
	require.Zero(t, result.SearchCount)
	require.Zero(t, result.WebSearchCalls)
	require.Equal(t, 30, result.Usage.InputTokens)
	require.Equal(t, 5, result.Usage.OutputTokens)
	require.Contains(t, string(upstream.modelRequests[1]), "search provider request failed")
	final := hostedSearchFinal(t, recorder, true)
	require.Equal(t, "failed", final.Get("output.0.status").String())
}
