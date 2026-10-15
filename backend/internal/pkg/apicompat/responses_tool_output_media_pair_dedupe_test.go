package apicompat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// 成对删除的核心不变量：call 与 output 必须同生同死。此前两侧各自独立计数，
// 会出现「call 还在、output 被删」或反过来，上游（DeepSeek）要求补齐而让客户端
// 重放整轮 → 同一个工具被执行两次。
func TestDedupeResponsesCallIDs_DropsCallAndOutputAsPair(t *testing.T) {
	t.Run("重复 id 的两侧一起删，只留第一对", func(t *testing.T) {
		var input any
		require.NoError(t, json.Unmarshal([]byte(`[
			{"type":"function_call","call_id":"call_a","name":"exec_command"},
			{"type":"function_call_output","call_id":"call_a","output":"first"},
			{"type":"function_call","call_id":"call_a","name":"exec_command"},
			{"type":"function_call_output","call_id":"call_a","output":"second"},
			{"type":"message","role":"user","content":"next"}
		]`), &input))

		out, changed := DedupeResponsesCallIDs(input)
		require.True(t, changed)
		items, ok := out.([]any)
		require.True(t, ok)
		// 第一对留下，第二对（call+output）整体删除，user 消息原样保留。
		require.Len(t, items, 3)
		require.Equal(t, "call_a", responseItemField(t, items, 0, "call_id"))
		require.Equal(t, "function_call", responseItemField(t, items, 0, "type"))
		require.Equal(t, "call_a", responseItemField(t, items, 1, "call_id"))
		require.Equal(t, "function_call_output", responseItemField(t, items, 1, "type"))
		require.Equal(t, "first", responseItemField(t, items, 1, "output"))
		require.Equal(t, "next", responseItemField(t, items, 2, "content"))

		// 幂等：再跑一次不变。
		same, changedAgain := DedupeResponsesCallIDs(out)
		require.False(t, changedAgain)
		require.Equal(t, out, same)
	})

	t.Run("只剩孤立 call 时不动（真实缺 output 由上游报错，不在这里改写）", func(t *testing.T) {
		var input any
		require.NoError(t, json.Unmarshal([]byte(`[
			{"type":"function_call","call_id":"call_solo","name":"exec_command"},
			{"type":"function_call","call_id":"call_solo","name":"exec_command"}
		]`), &input))

		out, changed := DedupeResponsesCallIDs(input)
		require.True(t, changed)
		items, ok := out.([]any)
		require.True(t, ok)
		require.Len(t, items, 1)
	})

	t.Run("孤立 output 重复时也只留第一个", func(t *testing.T) {
		var input any
		require.NoError(t, json.Unmarshal([]byte(`[
			{"type":"function_call_output","call_id":"call_orphan","output":"one"},
			{"type":"function_call_output","call_id":"call_orphan","output":"two"}
		]`), &input))

		out, changed := DedupeResponsesCallIDs(input)
		require.True(t, changed)
		items, ok := out.([]any)
		require.True(t, ok)
		require.Len(t, items, 1)
		require.Equal(t, "one", responseItemField(t, items, 0, "output"))
	})

	t.Run("不同 id 的并行调用原样保留", func(t *testing.T) {
		var input any
		require.NoError(t, json.Unmarshal([]byte(`[
			{"type":"function_call","call_id":"call_a","name":"exec_command"},
			{"type":"function_call","call_id":"call_b","name":"exec_command"},
			{"type":"function_call_output","call_id":"call_a","output":"a"},
			{"type":"function_call_output","call_id":"call_b","output":"b"}
		]`), &input))

		out, changed := DedupeResponsesCallIDs(input)
		require.False(t, changed)
		items, ok := out.([]any)
		require.True(t, ok)
		require.Len(t, items, 4)
	})
}

func TestDedupeChatToolCallIDs_DropsCallAndOutputAsPair(t *testing.T) {
	var messages any
	require.NoError(t, json.Unmarshal([]byte(`[
		{"role":"assistant","tool_calls":[{"id":"call_a","type":"function"},{"id":"call_b","type":"function"}]},
		{"role":"tool","tool_call_id":"call_a","content":"a1"},
		{"role":"assistant","tool_calls":[{"id":"call_a","type":"function"}]},
		{"role":"tool","tool_call_id":"call_a","content":"a2"},
		{"role":"tool","tool_call_id":"call_b","content":"b1"}
	]`), &messages))

	out, changed := DedupeChatToolCallIDs(messages)
	require.True(t, changed)
	items, ok := out.([]any)
	require.True(t, ok)
	// 第二条 assistant 的重复 call_b/id=call_a 及其 tool 消息被成对删除；
	// call_b 的输出不重复，保留。
	require.Len(t, items, 3)
	first, ok := items[0].(map[string]any)
	require.True(t, ok)
	calls, ok := first["tool_calls"].([]any)
	require.True(t, ok)
	require.Len(t, calls, 2)
	callA, ok := calls[0].(map[string]any)
	require.True(t, ok)
	callB, ok := calls[1].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "call_a", callA["id"])
	require.Equal(t, "call_b", callB["id"])
	toolA, ok := items[1].(map[string]any)
	require.True(t, ok)
	toolB, ok := items[2].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "call_a", toolA["tool_call_id"])
	require.Equal(t, "a1", toolA["content"])
	require.Equal(t, "call_b", toolB["tool_call_id"])
}

func TestDedupeResponsesCallIDs_CollidingItemIDAndNestedCopy(t *testing.T) {
	t.Parallel()
	callID := "call_00_gEGoDdh7olwRXAaeGk7k6101"
	var input any
	require.NoError(t, json.Unmarshal([]byte(`[
		{"type":"function_call","id":"`+callID+`","call_id":"`+callID+`","name":"shell","arguments":"{\"cmd\":\"pwd\"}"},
		{"type":"function_call_output","call_id":"`+callID+`","output":"ok"},
		{"type":"message","role":"assistant","content":[
			{"type":"output_text","text":"ran"},
			{"type":"function_call","id":"`+callID+`","call_id":"`+callID+`","name":"shell","arguments":"{\"cmd\":\"pwd\"}"}
		]}
	]`), &input))

	out, changed := DedupeResponsesCallIDs(input)
	require.True(t, changed)
	items, ok := out.([]any)
	require.True(t, ok)
	require.Len(t, items, 3)
	require.Equal(t, "fc_00_gEGoDdh7olwRXAaeGk7k6101", responseItemField(t, items, 0, "id"))
	require.Equal(t, callID, responseItemField(t, items, 0, "call_id"))
	require.Equal(t, callID, responseItemField(t, items, 1, "call_id"))
	msg, ok := items[2].(map[string]any)
	require.True(t, ok)
	content, ok := msg["content"].([]any)
	require.True(t, ok)
	require.Len(t, content, 1)
	part, ok := content[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "output_text", part["type"])

	again, changedAgain := DedupeResponsesCallIDs(out)
	require.False(t, changedAgain)
	require.Equal(t, out, again)
}

func TestDedupeResponsesCallIDs_IDOnlyCallStillDedupes(t *testing.T) {
	t.Parallel()
	var input any
	require.NoError(t, json.Unmarshal([]byte(`[
		{"type":"function_call","id":"call_00_only","name":"shell","arguments":"{}"},
		{"type":"function_call","call_id":"call_00_only","name":"shell","arguments":"{}"}
	]`), &input))
	out, changed := DedupeResponsesCallIDs(input)
	require.True(t, changed)
	items, ok := out.([]any)
	require.True(t, ok)
	require.Len(t, items, 1)
	require.Equal(t, "call_00_only", responseItemField(t, items, 0, "call_id"))
	require.NotEqual(t, "call_00_only", responseItemField(t, items, 0, "id"))
}
