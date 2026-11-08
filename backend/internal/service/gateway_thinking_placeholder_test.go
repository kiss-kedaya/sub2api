//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestEnsureDeepSeekThinkingReplayPlaceholders(t *testing.T) {
	tool := `{"name":"f","description":"d","input_schema":{"type":"object"}}`
	assistantTool := `{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"f","input":{}}]}`
	user := `{"role":"user","content":[{"type":"text","text":"q"}]}`
	toolResult := `{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"ok"}]}`
	body := []byte(`{"model":"deepseek-v4.1-flash","tools":[` + tool + `],"messages":[` + user + `,` + assistantTool + `,` + toolResult + `]}`)

	out, changed := ensureDeepSeekThinkingReplayPlaceholders(body, "deepseek-v4.1-flash")
	require.True(t, changed)
	content := gjson.GetBytes(out, "messages.1.content")
	require.Equal(t, "thinking", content.Get("0.type").String())
	require.Equal(t, deepSeekThinkingPlaceholderText, content.Get("0.thinking").String())
	require.Equal(t, "tool_use", content.Get("1.type").String())

	withThinking := []byte(`{"model":"deepseek-v4.1-flash","messages":[` + user + `,{"role":"assistant","content":[{"type":"thinking","thinking":"real"},{"type":"tool_use","id":"toolu_9","name":"f","input":{}}]},` + toolResult + `]}`)
	require.Equal(t, string(withThinking), func() string {
		o, _ := ensureDeepSeekThinkingReplayPlaceholders(withThinking, "deepseek-v4.1-flash")
		return string(o)
	}())

	textOnly := []byte(`{"model":"deepseek-v4.1-flash","messages":[` + user + `,{"role":"assistant","content":[{"type":"text","text":"hello"}]},` + toolResult + `]}`)
	o, changed := ensureDeepSeekThinkingReplayPlaceholders(textOnly, "deepseek-v4.1-flash")
	require.False(t, changed)
	require.Equal(t, string(textOnly), string(o))

	emptyThinking := []byte(`{"model":"deepseek-v4.1-flash","messages":[` + user + `,{"role":"assistant","content":[{"type":"thinking","thinking":""},{"type":"tool_use","id":"toolu_9","name":"f","input":{}}]},` + toolResult + `]}`)
	filled, changed := ensureDeepSeekThinkingReplayPlaceholders(emptyThinking, "deepseek-v4.1-flash")
	require.True(t, changed)
	require.Equal(t, deepSeekThinkingPlaceholderText, gjson.GetBytes(filled, "messages.1.content.0.thinking").String())
	require.Len(t, gjson.GetBytes(filled, "messages.1.content").Array(), 2, "must fill the existing block, not insert another")

	claude := []byte(`{"model":"claude-opus-5-5","messages":[` + user + `,` + assistantTool + `,` + toolResult + `]}`)
	o, changed = ensureDeepSeekThinkingReplayPlaceholders(claude, "claude-opus-5-5")
	require.False(t, changed)
	require.Equal(t, string(claude), string(o))
}
