//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestInjectDeepSeekSearchToolAddsToolWhenAbsent(t *testing.T) {
	body := []byte(`{"model":"deepseek-chat","messages":[]}`)
	next, changed := injectDeepSeekSearchTool(body)
	require.True(t, changed)

	tools := gjson.GetBytes(next, "tools")
	require.True(t, tools.IsArray())
	require.Equal(t, 1, len(tools.Array()))
	require.Equal(t, "web_search", tools.Array()[0].Get("function.name").String())
	// 原有字段必须原样保留
	require.Equal(t, "deepseek-chat", gjson.GetBytes(next, "model").String())
}

func TestInjectDeepSeekSearchToolSkipsExistingTool(t *testing.T) {
	body := []byte(`{"tools":[{"type":"function","function":{"name":"web_search"}}]}`)
	next, changed := injectDeepSeekSearchTool(body)
	require.False(t, changed)
	require.Equal(t, string(body), string(next))
}

func TestInjectDeepSeekSearchToolAppendsToExistingTools(t *testing.T) {
	body := []byte(`{"tools":[{"type":"function","function":{"name":"calc"}}]}`)
	next, changed := injectDeepSeekSearchTool(body)
	require.True(t, changed)

	tools := gjson.GetBytes(next, "tools").Array()
	require.Equal(t, 2, len(tools))
	require.Equal(t, "calc", tools[0].Get("function.name").String())
	require.Equal(t, "web_search", tools[1].Get("function.name").String())
}

func TestDeepSeekSearchCallQueryParsesArguments(t *testing.T) {
	query, count := deepSeekSearchCallQuery(`{"query":"deepseek v4","count":5}`)
	require.Equal(t, "deepseek v4", query)
	require.Equal(t, 5, count)

	query, count = deepSeekSearchCallQuery(`{"query":"x"}`)
	require.Equal(t, "x", query)
	require.Equal(t, 0, count, "missing count stays 0 so the caller can clamp to default")
}

func TestBuildDeepSeekSearchToolResultKeepsSummaryDropsContent(t *testing.T) {
	raw := []byte(`{"results":[{"title":"T","url":"https://e.com","snippet":"S","summary":"SUM","content":"HUGE BODY","date":"2026-01-01"}]}`)
	out, err := buildDeepSeekSearchToolResult(raw)
	require.NoError(t, err)
	require.NotContains(t, out, "HUGE BODY", "content must not be forwarded to the model")
	require.Contains(t, out, "SUM")
	require.Equal(t, int64(1), gjson.Get(out, "count").Int())
}

func TestClampDeepSeekSearchCount(t *testing.T) {
	require.Equal(t, 10, clampDeepSeekSearchCount(0))
	require.Equal(t, 1, clampDeepSeekSearchCount(1))
	require.Equal(t, 50, clampDeepSeekSearchCount(999))
}

func TestCollectDeepSeekSearchCallsFromResponsesShape(t *testing.T) {
	body := []byte(`{"output":[
		{"type":"function_call","call_id":"c1","name":"web_search","arguments":"{\"query\":\"a\"}"},
		{"type":"function_call","call_id":"c2","name":"other","arguments":"{}"}
	]}`)
	calls := collectDeepSeekSearchCalls(body)
	require.Len(t, calls, 1)
	require.Equal(t, "c1", calls[0].CallID)
}

func TestCollectDeepSeekSearchCallsFromChatShape(t *testing.T) {
	body := []byte(`{"choices":[{"message":{"tool_calls":[
		{"id":"c9","function":{"name":"web_search","arguments":"{\"query\":\"b\"}"}}
	]}}]}`)
	calls := collectDeepSeekSearchCalls(body)
	require.Len(t, calls, 1)
	require.Equal(t, "c9", calls[0].CallID)
}

func TestAttachDeepSeekSearchResultsResponsesShape(t *testing.T) {
	body := []byte(`{"output":[{"type":"function_call","call_id":"c1","name":"web_search"}]}`)
	next, changed := attachDeepSeekSearchResults(body, map[string]string{"c1": `{"count":1}`})
	require.True(t, changed)

	output := gjson.GetBytes(next, "output").Array()
	require.Len(t, output, 2, "the tool result is appended, the call is kept")
	require.Equal(t, "function_call_output", output[1].Get("type").String())
	require.Equal(t, "c1", output[1].Get("call_id").String())
}

func TestAttachDeepSeekSearchResultsChatShape(t *testing.T) {
	body := []byte(`{"choices":[{"message":{"content":"hi"}}]}`)
	next, changed := attachDeepSeekSearchResults(body, map[string]string{"c1": `{"count":2}`})
	require.True(t, changed)
	require.Equal(t, "c1", gjson.GetBytes(next, "choices.0.message.search_results.call_id").String())
}
