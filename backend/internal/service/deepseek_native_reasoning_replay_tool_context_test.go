//go:build unit

package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// 生产直连 34739 复现结论：native /v1/responses 的“当前 tool turn”若只有
// function_call + function_call_output 而没有 reasoning_text，DeepSeek 精确
// 400；补一条 user 消息后 200。只删空 reasoning 会留下孤立 call/output，属漏修。
// 期望行为：缺 reasoning 的 call/output 成对转成 user 上下文，内容为
// "[Previous tool context]\n" + 原始 item.Raw，name/arguments/output 全部保留。

const deepSeekToolContextExpectedPrefix = "[Previous tool context]\n"

func deepSeekToolCtxItems(t *testing.T, body []byte) []gjson.Result {
	t.Helper()
	require.True(t, json.Valid(body), "transformed request must stay valid JSON")
	input := gjson.GetBytes(body, "input")
	require.True(t, input.IsArray(), "input must stay an array")
	return input.Array()
}

func deepSeekToolCtxFind(items []gjson.Result, itemType, callID string) (gjson.Result, bool) {
	for _, item := range items {
		if item.Get("type").String() == itemType && item.Get("call_id").String() == callID {
			return item, true
		}
	}
	return gjson.Result{}, false
}

func deepSeekToolCtxCountType(items []gjson.Result, itemType string) int {
	count := 0
	for _, item := range items {
		if item.Get("type").String() == itemType {
			count++
		}
	}
	return count
}

// deepSeekToolCtxUserTexts 返回所有“带工具上下文前缀的 user 消息”的完整文本。
func deepSeekToolCtxUserTexts(items []gjson.Result) map[string]bool {
	out := make(map[string]bool, len(items))
	for _, item := range items {
		if item.Get("role").String() != "user" || item.Get("content.0.type").String() != "input_text" {
			continue
		}
		text := item.Get("content.0.text").String()
		if strings.HasPrefix(text, deepSeekToolContextExpectedPrefix) {
			out[text] = true
		}
	}
	return out
}

func deepSeekToolCtxRequireNoToolItems(t *testing.T, items []gjson.Result) {
	t.Helper()
	require.Zero(t, deepSeekToolCtxCountType(items, "function_call"),
		"function_call must be converted, not silently dropped or left orphaned")
	require.Zero(t, deepSeekToolCtxCountType(items, "function_call_output"),
		"function_call_output must be converted with its call, never dropped alone")
}

func deepSeekToolCtxRequireEffortUnchanged(t *testing.T, out []byte) {
	t.Helper()
	require.Equal(t, "high", gjson.GetBytes(out, "reasoning.effort").String(),
		"reasoning.effort=high must survive the tool-context rewrite")
	require.Equal(t, "deepseek-v4.1-flash", gjson.GetBytes(out, "model").String())
}

// 当前 tool turn 缺 reasoning（空 reasoning 项 / 完全没有 reasoning 项）时，
// call 与匹配 output 必须成对转成 user 上下文，且原文完整保留。
func TestDeepSeekNativeToolContextActiveTurnWithoutReasoning(t *testing.T) {
	gin.SetMode(gin.TestMode)
	account := deepSeekNativeReplayAccount()

	t.Run("empty_reasoning_item", func(t *testing.T) {
		svc := &OpenAIGatewayService{cache: &reasoningRecordingCache{}}
		c, _ := deepSeekNativeReplayContext(21, "tool-context-empty-reasoning")
		body := []byte(`{"model":"deepseek-v4.1-flash","reasoning":{"effort":"high"},"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"look it up"}]},
			{"type":"reasoning","id":"rs_missing_tool","summary":[]},
			{"type":"function_call","call_id":"call_lookup","name":"lookup","arguments":"{\"q\":\"x\"}"},
			{"type":"function_call_output","call_id":"call_lookup","output":"42"}
		]}`)

		original := deepSeekToolCtxItems(t, body)
		call, ok := deepSeekToolCtxFind(original, "function_call", "call_lookup")
		require.True(t, ok)
		output, ok := deepSeekToolCtxFind(original, "function_call_output", "call_lookup")
		require.True(t, ok)

		out := svc.prepareDeepSeekNativeReasoningReplay(c, account, body)
		items := deepSeekToolCtxItems(t, out)

		deepSeekToolCtxRequireNoToolItems(t, items)
		require.False(t, gjson.GetBytes(out, `input.#(type=="reasoning")`).Exists(),
			"the unrecoverable reasoning item must not be forwarded empty")
		require.Equal(t, map[string]bool{
			deepSeekToolContextExpectedPrefix + call.Raw:   true,
			deepSeekToolContextExpectedPrefix + output.Raw: true,
		}, deepSeekToolCtxUserTexts(items), "call and output evidence must both survive verbatim")
		deepSeekToolCtxRequireEffortUnchanged(t, out)
	})

	t.Run("reasoning_item_absent", func(t *testing.T) {
		svc := &OpenAIGatewayService{cache: &reasoningRecordingCache{}}
		c, _ := deepSeekNativeReplayContext(21, "tool-context-no-reasoning")
		body := []byte(`{"model":"deepseek-v4.1-flash","reasoning":{"effort":"high"},"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"look it up"}]},
			{"type":"function_call","call_id":"call_lookup","name":"lookup","arguments":"{}"},
			{"type":"function_call_output","call_id":"call_lookup","output":"42"}
		]}`)

		original := deepSeekToolCtxItems(t, body)
		call, ok := deepSeekToolCtxFind(original, "function_call", "call_lookup")
		require.True(t, ok)
		output, ok := deepSeekToolCtxFind(original, "function_call_output", "call_lookup")
		require.True(t, ok)

		out := svc.prepareDeepSeekNativeReasoningReplay(c, account, body)
		items := deepSeekToolCtxItems(t, out)

		deepSeekToolCtxRequireNoToolItems(t, items)
		require.Equal(t, map[string]bool{
			deepSeekToolContextExpectedPrefix + call.Raw:   true,
			deepSeekToolContextExpectedPrefix + output.Raw: true,
		}, deepSeekToolCtxUserTexts(items))
		deepSeekToolCtxRequireEffortUnchanged(t, out)
	})
}

// 真实有效的 reasoning + 原始 tool pair 必须字节原样，不能因为兜底逻辑被降级。
func TestDeepSeekNativeToolContextKeepsValidReasoningAndToolPair(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &OpenAIGatewayService{cache: &reasoningRecordingCache{}}
	c, _ := deepSeekNativeReplayContext(21, "tool-context-valid")
	body := []byte(`{"model":"deepseek-v4.1-flash","reasoning":{"effort":"high"},"input":[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"look it up"}]},
		{"type":"reasoning","id":"rs_valid_tool","summary":[],"content":[{"type":"reasoning_text","text":"valid tool thought"}]},
		{"type":"function_call","call_id":"call_keep","name":"lookup","arguments":"{\"q\":\"keep\"}"},
		{"type":"function_call_output","call_id":"call_keep","output":"kept"}
	]}`)

	out := svc.prepareDeepSeekNativeReasoningReplay(c, deepSeekNativeReplayAccount(), body)

	require.Equal(t, string(body), string(out),
		"a covered tool turn with real reasoning_text must stay byte-for-byte identical")
	items := deepSeekToolCtxItems(t, out)
	call, ok := deepSeekToolCtxFind(items, "function_call", "call_keep")
	require.True(t, ok)
	require.Equal(t, `{"q":"keep"}`, call.Get("arguments").String())
	output, ok := deepSeekToolCtxFind(items, "function_call_output", "call_keep")
	require.True(t, ok)
	require.Equal(t, "kept", output.Get("output").String())
	require.Empty(t, deepSeekToolCtxUserTexts(items))
}

// 并行两 call + 两 output（缺 reasoning）必须全部一致转换，不能只处理第一对。
func TestDeepSeekNativeToolContextConvertsParallelCallsAndOutputs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &OpenAIGatewayService{cache: &reasoningRecordingCache{}}
	c, _ := deepSeekNativeReplayContext(21, "tool-context-parallel")
	body := []byte(`{"model":"deepseek-v4.1-flash","reasoning":{"effort":"high"},"input":[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"run both"}]},
		{"type":"reasoning","id":"rs_parallel_missing","summary":[]},
		{"type":"function_call","call_id":"call_a","name":"lookup","arguments":"{\"q\":\"a\"}"},
		{"type":"function_call","call_id":"call_b","name":"lookup","arguments":"{\"q\":\"b\"}"},
		{"type":"function_call_output","call_id":"call_a","output":"A"},
		{"type":"function_call_output","call_id":"call_b","output":"B"}
	]}`)

	original := deepSeekToolCtxItems(t, body)
	expected := make(map[string]bool, 4)
	for _, item := range original {
		switch item.Get("type").String() {
		case "function_call", "function_call_output":
			expected[deepSeekToolContextExpectedPrefix+item.Raw] = true
		}
	}
	require.Len(t, expected, 4)

	out := svc.prepareDeepSeekNativeReasoningReplay(c, deepSeekNativeReplayAccount(), body)
	items := deepSeekToolCtxItems(t, out)

	deepSeekToolCtxRequireNoToolItems(t, items)
	require.Equal(t, expected, deepSeekToolCtxUserTexts(items),
		"both parallel calls and both outputs must be converted with identical treatment")
	deepSeekToolCtxRequireEffortUnchanged(t, out)
	require.Contains(t, deepSeekToolCtxUserTexts(items), deepSeekToolContextExpectedPrefix+
		`{"type":"function_call_output","call_id":"call_b","output":"B"}`)
}

// 第一轮 tool pair 有真实 reasoning，保持原样；后续 output 之后再次出现且缺
// reasoning 的 call/output 仍要兜底转换。
func TestDeepSeekNativeToolContextLaterTurnAfterOutputFallsBack(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &OpenAIGatewayService{cache: &reasoningRecordingCache{}}
	c, _ := deepSeekNativeReplayContext(21, "tool-context-later-turn")
	body := []byte(`{"model":"deepseek-v4.1-flash","reasoning":{"effort":"high"},"input":[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"first lookup"}]},
		{"type":"reasoning","id":"rs_later_valid","summary":[],"content":[{"type":"reasoning_text","text":"first tool thought"}]},
		{"type":"function_call","call_id":"call_first","name":"lookup","arguments":"{\"q\":\"first\"}"},
		{"type":"function_call_output","call_id":"call_first","output":"first result"},
		{"type":"function_call","call_id":"call_second","name":"lookup","arguments":"{\"q\":\"second\"}"},
		{"type":"function_call_output","call_id":"call_second","output":"second result"}
	]}`)

	original := deepSeekToolCtxItems(t, body)
	firstCall, ok := deepSeekToolCtxFind(original, "function_call", "call_first")
	require.True(t, ok)
	firstOutput, ok := deepSeekToolCtxFind(original, "function_call_output", "call_first")
	require.True(t, ok)
	secondCall, ok := deepSeekToolCtxFind(original, "function_call", "call_second")
	require.True(t, ok)
	secondOutput, ok := deepSeekToolCtxFind(original, "function_call_output", "call_second")
	require.True(t, ok)

	out := svc.prepareDeepSeekNativeReasoningReplay(c, deepSeekNativeReplayAccount(), body)
	items := deepSeekToolCtxItems(t, out)

	keptCall, ok := deepSeekToolCtxFind(items, "function_call", "call_first")
	require.True(t, ok, "the reasoning-covered first call must stay a function_call")
	require.Equal(t, firstCall.Raw, keptCall.Raw)
	keptOutput, ok := deepSeekToolCtxFind(items, "function_call_output", "call_first")
	require.True(t, ok)
	require.Equal(t, firstOutput.Raw, keptOutput.Raw)

	_, stillCall := deepSeekToolCtxFind(items, "function_call", "call_second")
	require.False(t, stillCall, "the later uncovered call must be converted, not forwarded bare")
	_, stillOutput := deepSeekToolCtxFind(items, "function_call_output", "call_second")
	require.False(t, stillOutput)
	require.Equal(t, map[string]bool{
		deepSeekToolContextExpectedPrefix + secondCall.Raw:   true,
		deepSeekToolContextExpectedPrefix + secondOutput.Raw: true,
	}, deepSeekToolCtxUserTexts(items))
	deepSeekToolCtxRequireEffortUnchanged(t, out)
}
