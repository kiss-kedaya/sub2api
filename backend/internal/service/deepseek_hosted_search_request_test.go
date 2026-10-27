//go:build unit

package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestPrepareDeepSeekHostedSearchReplacesOnlyHostedTool(t *testing.T) {
	for _, searchType := range []string{"web_search", "web_search_preview", "web_search_preview_2025_03_11", "web_search_2025_08_26"} {
		t.Run(searchType, func(t *testing.T) {
			body := []byte(`{"model":"deepseek-flash","stream":true,"tools":[{"type":"function","name":"local_tool","parameters":{"type":"object"}},{"type":"` + searchType + `"},{"type":"local_shell"}],"tool_choice":{"type":"` + searchType + `"},"input":"news"}`)
			next, changed, err := prepareDeepSeekHostedSearchRequest(body)
			require.NoError(t, err)
			require.True(t, changed)
			require.JSONEq(t, gjson.GetBytes(body, "tools.0").Raw, gjson.GetBytes(next, "tools.0").Raw)
			require.JSONEq(t, gjson.GetBytes(body, "tools.2").Raw, gjson.GetBytes(next, "tools.2").Raw)
			require.Equal(t, "function", gjson.GetBytes(next, "tools.1.type").String())
			require.Equal(t, deepSeekHostedSearchToolName, gjson.GetBytes(next, "tools.1.name").String())
			require.False(t, gjson.GetBytes(next, "tools.1.function").Exists(), "Responses tools require a flat function schema")
			require.Equal(t, int64(128), gjson.GetBytes(next, "tools.1.parameters.properties.query.maxLength").Int())
			require.Equal(t, int64(1), gjson.GetBytes(next, "tools.1.parameters.properties.count.minimum").Int())
			require.Equal(t, int64(50), gjson.GetBytes(next, "tools.1.parameters.properties.count.maximum").Int())
			require.Equal(t, deepSeekHostedSearchToolName, gjson.GetBytes(next, "tool_choice.name").String())
			require.True(t, gjson.GetBytes(next, "stream").Bool())
		})
	}
}

func TestPrepareDeepSeekHostedSearchCodexContentTypes(t *testing.T) {
	for _, contentTypes := range []string{`["text","image"]`, `["text"]`, `["image","text"]`, `null`} {
		t.Run(contentTypes, func(t *testing.T) {
			body := []byte(`{"model":"deepseek-flash","stream":true,"tools":[{"type":"web_search","external_web_access":true,"indexed_web_access":null,"search_content_types":` + contentTypes + `,"filters":null,"user_location":null}],"tool_choice":{"type":"web_search"},"input":"Find the current documentation"}`)
			next, changed, err := prepareDeepSeekHostedSearchRequest(body)
			require.NoError(t, err)
			require.True(t, changed)
			require.Equal(t, deepSeekHostedSearchToolName, gjson.GetBytes(next, "tools.0.name").String())
			require.Equal(t, deepSeekHostedSearchToolName, gjson.GetBytes(next, "tool_choice.name").String())
			require.False(t, gjson.GetBytes(next, "tools.0.search_content_types").Exists(), "provider-specific options must not leak into the function schema")
			if contentTypes != "null" {
				require.Contains(t, gjson.GetBytes(next, "tools.0.description").String(), "text web results")
				require.Contains(t, gjson.GetBytes(next, "tools.0.description").String(), "image search results are unavailable")
			}
		})
	}
	for _, contentTypes := range []string{`["image"]`, `[]`, `"text"`, `[1]`, `["video"]`, `["text",null]`} {
		t.Run("invalid_"+contentTypes, func(t *testing.T) {
			body := []byte(`{"tools":[{"type":"web_search","search_content_types":` + contentTypes + `}],"input":"news"}`)
			next, changed, err := prepareDeepSeekHostedSearchRequest(body)
			require.Error(t, err)
			require.False(t, changed)
			require.Equal(t, body, next)
		})
	}
}

func TestPrepareDeepSeekHostedSearchRespectsChoiceAndNoSearchRequests(t *testing.T) {
	for _, choice := range []string{`"auto"`, `"required"`, `"none"`} {
		body := []byte(`{"tools":[{"type":"web_search"}],"tool_choice":` + choice + `,"input":"hi"}`)
		next, changed, err := prepareDeepSeekHostedSearchRequest(body)
		require.NoError(t, err)
		require.Equal(t, choice != `"none"`, changed)
		require.Equal(t, choice, gjson.GetBytes(next, "tool_choice").Raw)
		if choice == `"none"` {
			require.Equal(t, body, next)
		}
	}
	body := []byte(`{"input":"hi","tools":[{"type":"function","name":"web_search","parameters":{}}],"extra":{"keep":true}}`)
	next, changed, err := prepareDeepSeekHostedSearchRequest(body)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, body, next, "a client's ordinary search-named function belongs to the client")
}

func TestPrepareDeepSeekHostedSearchRejectsOwnedNameCollision(t *testing.T) {
	for _, tool := range []string{
		`{"type":"function","name":"` + deepSeekHostedSearchToolName + `"}`,
		`{"type":"function","function":{"name":"` + deepSeekHostedSearchToolName + `"}}`,
	} {
		body := []byte(`{"tools":[{"type":"web_search"},` + tool + `],"input":"hi"}`)
		next, changed, err := prepareDeepSeekHostedSearchRequest(body)
		require.ErrorContains(t, err, "conflicts with a client tool")
		require.False(t, changed)
		require.Equal(t, body, next)
	}
}

func TestPrepareDeepSeekHostedSearchOptionsAreKeptOrExplicitlyRejected(t *testing.T) {
	body := []byte(`{"tools":[{"type":"web_search","filters":{"allowed_domains":["EXAMPLE.com","docs.example.com","example.com"]},"user_location":{"type":"approximate","city":"Tokyo"},"search_context_size":"high"}],"input":"news"}`)
	next, changed, err := prepareDeepSeekHostedSearchRequest(body)
	require.NoError(t, err)
	require.True(t, changed)
	description := gjson.GetBytes(next, "tools.0.description").String()
	require.Contains(t, description, "example.com, docs.example.com")
	require.Contains(t, description, "Tokyo")
	require.Contains(t, description, "high")
	domains, err := deepSeekHostedSearchAllowedDomains(gjson.GetBytes(body, "tools.0"))
	require.NoError(t, err)
	require.Equal(t, []string{"example.com", "docs.example.com"}, domains)
	for _, options := range []string{
		`"external_web_access":false`, `"filters":{"blocked_domains":["example.com"]}`,
		`"indexed_web_access":true`, `"indexed_web_access":"true"`,
		`"search_context_size":"huge"`, `"user_location":"Tokyo"`, `"unrecognized":true`,
		`"filters":{"allowed_domains":["example.com OR site:evil.com"]}`,
		`"filters":{"allowed_domains":["https://example.com"]}`,
		`"filters":{"allowed_domains":["*.example.com"]}`,
		`"filters":{"allowed_domains":["127.0.0.1"]}`,
		`"filters":{"allowed_domains":["127.1"]}`,
		`"filters":{"allowed_domains":["0x7f.0x0.0x0.0x1"]}`,
		`"filters":{"allowed_domains":[]}`,
	} {
		invalid := []byte(`{"tools":[{"type":"web_search",` + options + `}],"input":"news"}`)
		_, changed, err := prepareDeepSeekHostedSearchRequest(invalid)
		require.Error(t, err, options)
		require.False(t, changed)
	}
}

func TestPrepareDeepSeekHostedSearchReplaysHistoryWithoutOrphanTools(t *testing.T) {
	body := []byte(`{"tools":[],"input":[{"type":"web_search_call","id":"ws_old","status":"completed","action":{"type":"search","query":"old question","sources":[{"url":"https://example.com/public"},{"url":"http://127.0.0.1/private"},{"url":"https://user:secret@example.com/"},{"url":"file:///etc/passwd"},{"url":"http://private.internal/data"}]}},{"type":"reasoning","id":"rs_old","encrypted_content":"opaque"},{"type":"function_call","call_id":"local_call","name":"client_tool","arguments":"{}"},{"type":"function_call_output","call_id":"local_call","output":"ok"}]}`)
	next, changed, err := prepareDeepSeekHostedSearchRequest(body)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, "message", gjson.GetBytes(next, "input.0.type").String())
	require.Equal(t, "assistant", gjson.GetBytes(next, "input.0.role").String())
	history := gjson.GetBytes(next, "input.0.content.0.text").String()
	require.Contains(t, history, "old question")
	require.Contains(t, history, "https://example.com/public")
	require.NotContains(t, history, "127.0.0.1")
	require.NotContains(t, history, "secret")
	require.NotContains(t, history, "file:")
	require.NotContains(t, history, "private.internal")
	for i := 1; i < 4; i++ {
		require.JSONEq(t, gjson.GetBytes(body, "input").Array()[i].Raw, gjson.GetBytes(next, "input").Array()[i].Raw)
	}
	require.Empty(t, gjson.GetBytes(next, "tools").Array(), "completed history alone must not enable a fresh search")
}

func TestDeepSeekHostedSearchNextRequestPreservesNativeOutputAndOwnsOnlySearch(t *testing.T) {
	request := []byte(`{"model":"deepseek-flash","input":"question","stream":true,"store":true,"previous_response_id":"resp_old","tool_choice":{"type":"function","name":"` + deepSeekHostedSearchToolName + `"},"tools":[{"type":"function","name":"` + deepSeekHostedSearchToolName + `"}],"metadata":{"keep":"opaque"}}`)
	response := []byte(`{"output":[{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"opaque-cipher"},{"type":"function_call","id":"fc_1","call_id":"search1","name":"` + deepSeekHostedSearchToolName + `","arguments":"{\"query\":\"latest\"}"},{"type":"function_call","id":"fc_2","call_id":"local2","name":"client_tool","arguments":"{}"}]}`)
	next, err := deepSeekHostedSearchNextRequest(request, response, map[string]string{"search1": `{"results":[]}`, "local2": "must-not-attach"})
	require.NoError(t, err)
	items := gjson.GetBytes(next, "input").Array()
	require.Len(t, items, 5)
	require.Equal(t, "user", items[0].Get("role").String())
	require.Equal(t, "question", items[0].Get("content").String())
	require.JSONEq(t, gjson.GetBytes(response, "output.0").Raw, items[1].Raw)
	require.JSONEq(t, gjson.GetBytes(response, "output.1").Raw, items[2].Raw)
	require.JSONEq(t, gjson.GetBytes(response, "output.2").Raw, items[3].Raw)
	require.Equal(t, "function_call_output", items[4].Get("type").String())
	require.Equal(t, "search1", items[4].Get("call_id").String())
	require.NotContains(t, string(next), "must-not-attach")
	require.True(t, gjson.GetBytes(next, "stream").Bool())
	require.False(t, gjson.GetBytes(next, "store").Bool())
	require.False(t, gjson.GetBytes(next, "previous_response_id").Exists())
	require.Equal(t, "auto", gjson.GetBytes(next, "tool_choice").String())
	require.JSONEq(t, gjson.GetBytes(request, "metadata").Raw, gjson.GetBytes(next, "metadata").Raw)
	// A second round retains, rather than repeats, the already-appended result.
	last := []byte(`{"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}]}`)
	final, err := deepSeekHostedSearchNextRequest(next, last, nil)
	require.NoError(t, err)
	count := 0
	for _, item := range gjson.GetBytes(final, "input").Array() {
		if item.Get("type").String() == "function_call_output" && item.Get("call_id").String() == "search1" {
			count++
		}
	}
	require.Equal(t, 1, count)
	var valid map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(final, &valid))
}

func TestDeepSeekHostedSearchNextRequestRejectsOrphanOwnedCall(t *testing.T) {
	request := []byte(`{"input":[],"stream":false}`)
	response := []byte(`{"output":[{"type":"function_call","call_id":"missing","name":"` + deepSeekHostedSearchToolName + `"}]}`)
	_, err := deepSeekHostedSearchNextRequest(request, response, nil)
	require.ErrorContains(t, err, "has no result")
}

func TestPrepareDeepSeekHostedSearchNoneStillReplaysHistory(t *testing.T) {
	body := []byte(`{"tool_choice":"none","tools":[{"type":"web_search"},{"type":"function","name":"client_tool","parameters":{}}],"input":[{"type":"web_search_call","status":"completed","action":{"query":"previous question"}}]}`)
	next, changed, err := prepareDeepSeekHostedSearchRequest(body)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, "none", gjson.GetBytes(next, "tool_choice").String())
	require.Len(t, gjson.GetBytes(next, "tools").Array(), 1)
	require.Equal(t, "client_tool", gjson.GetBytes(next, "tools.0.name").String())
	require.Equal(t, "message", gjson.GetBytes(next, "input.0.type").String())
	require.NotContains(t, string(next), deepSeekHostedSearchToolName, "history replay must not enable the owned function")
}

func TestDeepSeekHostedSearchNextRequestRejectsDuplicateOwnedResults(t *testing.T) {
	call := `{"type":"function_call","call_id":"same","name":"` + deepSeekHostedSearchToolName + `"}`
	for _, response := range []string{
		`{"output":[` + call + `,` + call + `]}`,
		`{"output":[` + call + `,{"type":"function_call_output","call_id":"same","output":"already there"}]}`,
	} {
		_, err := deepSeekHostedSearchNextRequest([]byte(`{"input":[]}`), []byte(response), map[string]string{"same": "owned result"})
		require.Error(t, err)
	}
	_, err := deepSeekHostedSearchNextRequest([]byte(`{"input":[{"type":"function_call_output","call_id":"same","output":"previous result"}]}`), []byte(`{"output":[`+call+`]}`), map[string]string{"same": "duplicate result"})
	require.ErrorContains(t, err, "completed owned search call")
}
