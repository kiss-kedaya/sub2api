package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func TestDeepSeekPassthrough_StripsThinkingAndOrphanDSML(t *testing.T) {
	raw := "<thinking>plan</thinking>Let me run.</\uFF5C\uFF5CDSML\uFF5C\uFF5C parameter>\n</\uFF5C\uFF5CDSML\uFF5C\uFF5C invoke>\n</\uFF5C\uFF5CDSML\uFF5C\uFF5C calls>"
	payload, err := sjson.SetBytes([]byte(`{"type":"response.output_text.delta","output_index":0,"delta":""}`), "delta", raw)
	require.NoError(t, err)
	var st deepSeekPassthrough
	before, after, next, drop := st.rewrite(nil, "response.output_text.delta", payload)
	require.False(t, drop)
	require.NotNil(t, next)
	assert.Equal(t, "Let me run.", gjson.GetBytes(next, "delta").String())
	assert.NotContains(t, string(next), "DSML")
	assert.NotContains(t, string(next), "thinking>")
	joined := append(append([]string{}, before...), after...)
	blob := ""
	for _, line := range joined {
		blob += line
	}
	assert.Contains(t, blob, "response.reasoning_summary_text.delta")
	assert.Contains(t, blob, "plan")
	assert.NotContains(t, blob, "DSML")
}

func TestDeepSeekPassthrough_DSMLBecomesFunctionCallBeforeCompleted(t *testing.T) {
	raw := "Look.<\uFF5C\uFF5CDSML\uFF5C\uFF5C calls><\uFF5C\uFF5CDSML\uFF5C\uFF5C invoke name=\"bash\"><\uFF5C\uFF5CDSML\uFF5C\uFF5C parameter name=\"command\" string=\"true\">pwd</\uFF5C\uFF5CDSML\uFF5C\uFF5C parameter></\uFF5C\uFF5CDSML\uFF5C\uFF5C invoke></\uFF5C\uFF5CDSML\uFF5C\uFF5C calls>"
	delta, err := sjson.SetBytes([]byte(`{"type":"response.output_text.delta","output_index":0,"delta":""}`), "delta", raw)
	require.NoError(t, err)
	var st deepSeekPassthrough
	_, after, next, drop := st.rewrite(nil, "response.output_text.delta", delta)
	require.False(t, drop)
	assert.Equal(t, "Look.", gjson.GetBytes(next, "delta").String())
	blob := ""
	for _, line := range after {
		blob += line + "\n"
	}
	assert.Contains(t, blob, "response.function_call_arguments.done")
	assert.Contains(t, blob, `"name":"bash"`)
	assert.Contains(t, blob, "command")
	assert.Contains(t, blob, "pwd")

	completed, err := sjson.SetBytes([]byte(`{"type":"response.completed","response":{"output":[{"type":"message","content":[{"type":"output_text","text":""}]}]}}`), "response.output.0.content.0.text", raw)
	require.NoError(t, err)
	before, _, next, _ := st.rewrite(nil, "response.completed", completed)
	assert.Empty(t, before)
	assert.Equal(t, "Look.", gjson.GetBytes(next, "response.output.0.content.0.text").String())
	assert.Equal(t, "function_call", gjson.GetBytes(next, "response.output.1.type").String())
	assert.Equal(t, "bash", gjson.GetBytes(next, "response.output.1.name").String())
	assert.NotContains(t, string(next), "DSML")
}
