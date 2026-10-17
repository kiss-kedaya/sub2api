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

func TestDeepSeekPassthrough_LeavesLiveToolEventsUntouched(t *testing.T) {
	added := []byte(`{"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","id":"call_abc","call_id":"call_abc","name":"bash","arguments":""}}`)
	var st deepSeekPassthrough
	_, _, next, drop := st.rewrite(nil, "response.output_item.added", added)
	require.False(t, drop)
	assert.Nil(t, next)

	delta := []byte(`{"type":"response.function_call_arguments.delta","output_index":1,"item_id":"call_abc","delta":"{}"}`)
	_, _, next, drop = st.rewrite(nil, "response.function_call_arguments.delta", delta)
	require.False(t, drop)
	assert.Nil(t, next)

	done := []byte(`{"type":"response.output_item.done","output_index":1,"item":{"type":"function_call","id":"call_abc","call_id":"call_abc","name":"bash","arguments":"{}"}}`)
	_, _, next, drop = st.rewrite(nil, "response.output_item.done", done)
	require.False(t, drop)
	assert.Nil(t, next)
}

func TestDeepSeekPassthrough_CompletedDropsDuplicateWithoutRewritingID(t *testing.T) {
	completed := []byte(`{"type":"response.completed","response":{"output":[{"type":"function_call","id":"call_abc","call_id":"call_abc","name":"bash","arguments":"{}"},{"type":"function_call","id":"call_abc","call_id":"call_abc","name":"bash","arguments":"{\"cmd\":\"pwd\"}"}]}}`)
	var st deepSeekPassthrough
	_, _, next, drop := st.rewrite(nil, "response.completed", completed)
	require.False(t, drop)
	require.NotNil(t, next)
	require.Equal(t, 1, len(gjson.GetBytes(next, "response.output").Array()))
	assert.Equal(t, "call_abc", gjson.GetBytes(next, "response.output.0.id").String())
	assert.Equal(t, "call_abc", gjson.GetBytes(next, "response.output.0.call_id").String())
	assert.Contains(t, gjson.GetBytes(next, "response.output.0.arguments").String(), "pwd")
}

func TestRewriteDeepSeekResponseBody_DedupesWhenThinkingPresent(t *testing.T) {
	body := []byte(`{"output":[{"type":"message","content":[{"type":"output_text","text":"<think>secret</think>done"}]},{"type":"function_call","id":"call_a","call_id":"call_a","name":"bash","arguments":"{}"},{"type":"function_call","id":"call_b","call_id":"call_a","name":"bash","arguments":"{\"cmd\":\"pwd\"}"}]}`)
	out := rewriteDeepSeekResponseBody(nil, body)
	require.Equal(t, 2, len(gjson.GetBytes(out, "output").Array()))
	assert.Equal(t, "done", gjson.GetBytes(out, "output.0.content.0.text").String())
	assert.Equal(t, "function_call", gjson.GetBytes(out, "output.1.type").String())
	assert.Equal(t, "fc_a", gjson.GetBytes(out, "output.1.id").String())
	assert.Equal(t, "call_a", gjson.GetBytes(out, "output.1.call_id").String())
	assert.Contains(t, gjson.GetBytes(out, "output.1.arguments").String(), "pwd")
	assert.NotContains(t, string(out), "call_b")
}
