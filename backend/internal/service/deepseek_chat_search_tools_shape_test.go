package service

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// A Chat Completions request cannot carry {"type":"web_search"}: DeepSeek and
// every compatible relay reject it with "tools[0].type is invalid or missing".
// The declaration must become the gateway-owned function tool.
func TestPrepareDeepSeekChatSearchToolsRewritesHostedType(t *testing.T) {
	body := []byte(`{"model":"deepseek-flash","messages":[{"role":"user","content":"hi"}],` +
		`"tools":[{"type":"web_search"}],"tool_choice":{"type":"web_search"}}`)

	next, changed := prepareDeepSeekChatSearchTools(body)
	require.True(t, changed)
	require.False(t, gjson.GetBytes(next, `tools.#(type=="web_search")`).Exists(),
		"native hosted tool must not survive into a chat request")
	require.Equal(t, "function", gjson.GetBytes(next, "tools.0.type").String())
	require.Equal(t, deepSeekSearchToolName, gjson.GetBytes(next, "tools.0.function.name").String())
	require.Equal(t, "function", gjson.GetBytes(next, "tool_choice.type").String())
	require.Equal(t, deepSeekSearchToolName, gjson.GetBytes(next, "tool_choice.function.name").String())
}

// Client functions are never dropped or renamed, and an ordinary request with
// no hosted tool is returned untouched.
func TestPrepareDeepSeekChatSearchToolsKeepsClientTools(t *testing.T) {
	client := []byte(`{"model":"deepseek-flash","messages":[],"tools":[` +
		`{"type":"function","function":{"name":"my_fn","parameters":{"type":"object"}}}]}`)
	next, changed := prepareDeepSeekChatSearchTools(client)
	require.False(t, changed)
	require.Equal(t, string(client), string(next))

	mixed := []byte(`{"model":"deepseek-flash","messages":[],"tools":[` +
		`{"type":"function","function":{"name":"my_fn","parameters":{"type":"object"}}},` +
		`{"type":"web_search"}]}`)
	next, changed = prepareDeepSeekChatSearchTools(mixed)
	require.True(t, changed)
	require.Equal(t, "my_fn", gjson.GetBytes(next, "tools.0.function.name").String())
	require.Len(t, gjson.GetBytes(next, "tools").Array(), 2)
	require.Equal(t, deepSeekSearchToolName, gjson.GetBytes(next, "tools.1.function.name").String())
}

// A request that already declares the function tool by hand is not duplicated.
func TestPrepareDeepSeekChatSearchToolsDoesNotDuplicate(t *testing.T) {
	body := []byte(`{"model":"deepseek-flash","messages":[],"tools":[` +
		`{"type":"function","function":{"name":"web_search","parameters":{"type":"object"}}},` +
		`{"type":"web_search"}]}`)
	next, changed := prepareDeepSeekChatSearchTools(body)
	require.True(t, changed)
	require.Len(t, gjson.GetBytes(next, "tools").Array(), 1)
}
