//go:build unit

package service

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// 本文件只覆盖 replay 的“形状边界”：EasyInputMessage 省略 type、纯结构兜底、
// 恢复预算耗尽后的残留项、工具 output 边界、显式 reasoning 与平台隔离。
// 用例只依赖 prepareDeepSeekNativeReasoningReplay 的公开行为，不绑定内部实现。

func deepSeekBoundaryCachedEntry(t *testing.T, text string) string {
	t.Helper()
	content, err := json.Marshal([]map[string]string{{"type": "reasoning_text", "text": text}})
	require.NoError(t, err)
	entry, err := json.Marshal(deepSeekNativeReasoningEntry{Content: content})
	require.NoError(t, err)
	return string(entry)
}

func deepSeekBoundaryCachedEntryWithEncrypted(t *testing.T, text, encrypted string) string {
	t.Helper()
	content, err := json.Marshal([]map[string]string{{"type": "reasoning_text", "text": text}})
	require.NoError(t, err)
	entry, err := json.Marshal(deepSeekNativeReasoningEntry{
		Content:         content,
		EncryptedDigest: deepSeekNativeReasoningDigest(encrypted),
	})
	require.NoError(t, err)
	return string(entry)
}

func deepSeekBoundaryItems(t *testing.T, body []byte) []gjson.Result {
	t.Helper()
	require.True(t, json.Valid(body), "transformed request must stay valid JSON")
	input := gjson.GetBytes(body, "input")
	require.True(t, input.IsArray(), "input must stay an array")
	return input.Array()
}

func deepSeekBoundaryMessageByText(items []gjson.Result, text string) (gjson.Result, bool) {
	for _, item := range items {
		if strings.TrimSpace(deepSeekNativeMessageText(item.Get("content"))) == text {
			return item, true
		}
	}
	return gjson.Result{}, false
}

// EasyInputMessage 的 type 是可省略的（本仓 fixture 也这么发）。省略 type 的
// user 消息必须重置 reasoning 覆盖窗口，省略 type 的 assistant 消息必须走同一套
// 降级/合成消息清洗，否则 DeepSeek thinking 模式仍会 400。
func TestDeepSeekNativeReasoningReplayBoundaryEasyInputMessage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const thread = "boundary-easy-thread"
	const cachedText = "cached thinking for turn one"

	t.Run("assistant_without_type_demoted", func(t *testing.T) {
		svc := &OpenAIGatewayService{cache: &reasoningRecordingCache{}}
		c, _ := deepSeekNativeReplayContext(21, thread)
		body := []byte(`{"model":"deepseek-v4.1-flash","reasoning":{"effort":"max"},"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]},
			{"role":"assistant","content":[{"type":"output_text","text":"answer without reasoning"}]}
		]}`)

		out := svc.prepareDeepSeekNativeReasoningReplay(c, deepSeekNativeReplayAccount(), body)
		items := deepSeekBoundaryItems(t, out)
		require.Len(t, items, 2)
		require.Equal(t, "user", items[1].Get("role").String(),
			"assistant without an explicit type is still an EasyInputMessage")
		require.Equal(t, "input_text", items[1].Get("content.0.type").String(),
			"demoted assistant content must use input_text")
		require.Equal(t, "answer without reasoning", items[1].Get("content.0.text").String())
	})

	t.Run("user_without_type_resets_coverage", func(t *testing.T) {
		svc := &OpenAIGatewayService{cache: &reasoningRecordingCache{getResp: map[string]string{
			deepSeekNativeReasoningCacheKey(21, "rs_boundary_t1"): deepSeekBoundaryCachedEntryWithEncrypted(t, cachedText, "op-t1"),
		}}}
		c, _ := deepSeekNativeReplayContext(21, thread)
		body := []byte(`{"model":"deepseek-v4.1-flash","input":[
			{"role":"user","content":"q1"},
			{"type":"reasoning","id":"rs_boundary_t1","summary":[],"encrypted_content":"op-t1"},
			{"role":"assistant","content":[{"type":"output_text","text":"a1"}]},
			{"role":"user","content":"q2"},
			{"role":"assistant","content":[{"type":"output_text","text":"a2"}]}
		]}`)

		out := svc.prepareDeepSeekNativeReasoningReplay(c, deepSeekNativeReplayAccount(), body)
		items := deepSeekBoundaryItems(t, out)
		require.Len(t, items, 5)
		require.Equal(t, cachedText, items[1].Get("content.0.text").String())
		require.Equal(t, "assistant", items[2].Get("role").String(),
			"assistant covered by the restored reasoning stays assistant")
		require.Equal(t, "user", items[3].Get("role").String())
		require.Equal(t, "user", items[4].Get("role").String(),
			"assistant after a type-less user turn must not reuse the previous turn's reasoning")
		require.Equal(t, "input_text", items[4].Get("content.0.type").String())
	})

	t.Run("synthetic_previous_without_type_demoted", func(t *testing.T) {
		svc := &OpenAIGatewayService{cache: &reasoningRecordingCache{}}
		c, _ := deepSeekNativeReplayContext(21, thread)
		body := []byte(`{"model":"deepseek-v4.1-flash","input":[
			{"role":"assistant","content":"[Previous web search]\nStatus: completed"},
			{"type":"message","role":"user","content":"hi"}
		]}`)

		out := svc.prepareDeepSeekNativeReasoningReplay(c, deepSeekNativeReplayAccount(), body)
		items := deepSeekBoundaryItems(t, out)
		require.Equal(t, "user", items[0].Get("role").String(),
			"synthetic client context must be demoted even without an explicit type")
	})
}

// cache 不可用或请求没有 key 身份时，缓存回放无法进行，但纯结构兜底（把缺
// reasoning 的 assistant 历史降级为 user）不依赖缓存，必须照常完成。
func TestDeepSeekNativeReasoningReplayBoundaryStructuralFallbackWithoutCacheOrIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"deepseek-v4.1-flash","input":[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]},
		{"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer"}]},
		{"type":"message","role":"user","content":[{"type":"input_text","text":"next"}]}
	]}`)

	t.Run("nil_cache", func(t *testing.T) {
		svc := &OpenAIGatewayService{}
		c, _ := deepSeekNativeReplayContext(21, "boundary-nil-cache")

		out := svc.prepareDeepSeekNativeReasoningReplay(c, deepSeekNativeReplayAccount(), body)
		items := deepSeekBoundaryItems(t, out)
		require.Len(t, items, 3)
		require.Equal(t, "user", items[1].Get("role").String())
		require.Equal(t, "input_text", items[1].Get("content.0.type").String())
		require.Equal(t, "answer", items[1].Get("content.0.text").String())
		require.Equal(t, "user", items[2].Get("role").String())
	})

	t.Run("zero_key_id", func(t *testing.T) {
		svc := &OpenAIGatewayService{cache: &reasoningRecordingCache{}}
		c, _ := deepSeekNativeReplayContext(0, "boundary-zero-key")

		out := svc.prepareDeepSeekNativeReasoningReplay(c, deepSeekNativeReplayAccount(), body)
		items := deepSeekBoundaryItems(t, out)
		require.Len(t, items, 3)
		require.Equal(t, "user", items[1].Get("role").String())
		require.Equal(t, "input_text", items[1].Get("content.0.type").String())
	})
}

// 恢复预算（条目数 / 总字节）耗尽后，剩余 reasoning 不能被原样带空 content
// 出站；要么恢复，要么整项剔除。当前实现对剩余项直接 break，会把空 reasoning
// 继续送给 DeepSeek。
func TestDeepSeekNativeReasoningReplayBoundaryDropsUnrestoredReasoningAfterBudget(t *testing.T) {
	gin.SetMode(gin.TestMode)
	account := deepSeekNativeReplayAccount()

	t.Run("item_budget", func(t *testing.T) {
		const total = deepSeekNativeReasoningMaxItems + 1
		items := make([]any, 0, total+1)
		items = append(items, map[string]any{
			"type": "message", "role": "user",
			"content": []any{map[string]any{"type": "input_text", "text": "long thread"}},
		})
		getResp := make(map[string]string, deepSeekNativeReasoningMaxItems)
		for i := 0; i < total; i++ {
			id := fmt.Sprintf("rs_budget_%d", i)
			items = append(items, map[string]any{"type": "reasoning", "id": id, "summary": []any{}})
			if i < deepSeekNativeReasoningMaxItems {
				getResp[deepSeekNativeReasoningCacheKey(21, id)] = deepSeekBoundaryCachedEntry(t, "cached")
			}
		}
		body, err := json.Marshal(map[string]any{"model": "deepseek-v4.1-flash", "input": items})
		require.NoError(t, err)

		svc := &OpenAIGatewayService{cache: &reasoningRecordingCache{getResp: getResp}}
		c, _ := deepSeekNativeReplayContext(21, "boundary-item-budget")
		out := svc.prepareDeepSeekNativeReasoningReplay(c, account, body)

		replayed := 0
		for _, item := range deepSeekBoundaryItems(t, out) {
			if item.Get("type").String() != "reasoning" {
				continue
			}
			replayed++
			require.True(t, hasDeepSeekNativeReasoningText(item.Get("content")),
				"reasoning %s left the gateway without reasoning_text", item.Get("id").String())
		}
		require.Positive(t, replayed)
		require.Less(t, replayed, total, "the uncached tail must be dropped, not forwarded empty")
	})

	t.Run("byte_budget", func(t *testing.T) {
		const chunks = 5
		entry := deepSeekBoundaryCachedEntry(t, strings.Repeat("r", 512<<10))
		items := []any{map[string]any{
			"type": "message", "role": "user",
			"content": []any{map[string]any{"type": "input_text", "text": "large history"}},
		}}
		getResp := make(map[string]string, chunks)
		for i := 0; i < chunks; i++ {
			id := fmt.Sprintf("rs_big_%d", i)
			items = append(items, map[string]any{"type": "reasoning", "id": id, "summary": []any{}})
			getResp[deepSeekNativeReasoningCacheKey(21, id)] = entry
		}
		body, err := json.Marshal(map[string]any{"model": "deepseek-v4.1-flash", "input": items})
		require.NoError(t, err)

		svc := &OpenAIGatewayService{cache: &reasoningRecordingCache{getResp: getResp}}
		c, _ := deepSeekNativeReplayContext(21, "boundary-byte-budget")
		out := svc.prepareDeepSeekNativeReasoningReplay(c, account, body)

		restored := 0
		for _, item := range deepSeekBoundaryItems(t, out) {
			if item.Get("type").String() != "reasoning" {
				continue
			}
			restored++
			require.True(t, hasDeepSeekNativeReasoningText(item.Get("content")),
				"reasoning %s exceeded the restore byte budget but was forwarded without content", item.Get("id").String())
		}
		require.Positive(t, restored)
		require.Less(t, restored, chunks, "items past the byte budget must be dropped, not forwarded empty")
	})
}

// 工具轮次结束后，上一轮 reasoning 不能继续覆盖后续 assistant；工具对本身
// 不做删除主张，只钉住 coverage 边界。
func TestDeepSeekNativeReasoningReplayBoundaryToolOutputResetsCoverage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &OpenAIGatewayService{cache: &reasoningRecordingCache{getResp: map[string]string{
		deepSeekNativeReasoningCacheKey(21, "rs_tool_turn"): deepSeekBoundaryCachedEntryWithEncrypted(t, "tool-turn thinking", "op-tool"),
	}}}
	c, _ := deepSeekNativeReplayContext(21, "boundary-tool-thread")
	body := []byte(`{"model":"deepseek-v4.1-flash","reasoning":{"effort":"max"},"input":[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"look it up"}]},
		{"type":"reasoning","id":"rs_tool_turn","summary":[],"encrypted_content":"op-tool"},
		{"type":"function_call","call_id":"call_boundary","name":"lookup","arguments":"{}"},
		{"type":"function_call_output","call_id":"call_boundary","output":"42"},
		{"type":"message","role":"assistant","content":[{"type":"output_text","text":"the answer is 42"}]}
	]}`)

	out := svc.prepareDeepSeekNativeReasoningReplay(c, deepSeekNativeReplayAccount(), body)
	items := deepSeekBoundaryItems(t, out)
	require.Len(t, items, 5)
	require.Equal(t, "tool-turn thinking", items[1].Get("content.0.text").String())
	// 只钉 coverage：工具对仍在，不在本用例主张删除。
	require.Equal(t, "function_call", items[2].Get("type").String())
	require.Equal(t, "call_boundary", items[2].Get("call_id").String())
	require.Equal(t, "function_call_output", items[3].Get("type").String())
	require.Equal(t, "user", items[4].Get("role").String(),
		"assistant after a tool output must not inherit the tool turn's reasoning coverage")
	require.Equal(t, "input_text", items[4].Get("content.0.type").String())
}

// 显式有效 reasoning 原样保留（缓存不得改写）；非 DeepSeek 平台完全不动；
// 兼容 none+reasoning 现有 fallback，并钉住 max/none 普通会话的默认行为。
func TestDeepSeekNativeReasoningReplayBoundaryPreservesExplicitReasoningAndScopesPlatforms(t *testing.T) {
	gin.SetMode(gin.TestMode)
	deepseek := deepSeekNativeReplayAccount()

	t.Run("explicit_reasoning_kept_untouched", func(t *testing.T) {
		svc := &OpenAIGatewayService{cache: &reasoningRecordingCache{getResp: map[string]string{
			deepSeekNativeReasoningCacheKey(21, "rs_explicit"): deepSeekBoundaryCachedEntry(t, "poisoned cache text"),
		}}}
		c, _ := deepSeekNativeReplayContext(21, "boundary-explicit")
		body := []byte(`{"model":"deepseek-v4.1-flash","input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]},
			{"type":"reasoning","id":"rs_explicit","summary":[],"content":[{"type":"reasoning_text","text":"explicit reasoning"}]},
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer"}]}
		]}`)

		out := svc.prepareDeepSeekNativeReasoningReplay(c, deepseek, body)
		require.Equal(t, string(body), string(out),
			"a request with valid reasoning_text must stay byte-for-byte identical")
		require.Equal(t, "explicit reasoning", gjson.GetBytes(out, "input.1.content.0.text").String())
	})

	t.Run("other_platform_untouched", func(t *testing.T) {
		svc := &OpenAIGatewayService{cache: &reasoningRecordingCache{}}
		c, _ := deepSeekNativeReplayContext(21, "boundary-openai")
		body := []byte(`{"model":"gpt-5.5","input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]},
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer"}]}
		]}`)
		account := &Account{ID: 8, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
			Credentials: map[string]any{"api_key": "openai-boundary-key"}}

		out := svc.prepareDeepSeekNativeReasoningReplay(c, account, body)
		require.Equal(t, string(body), string(out), "non-DeepSeek accounts must not be reshaped")
	})

	t.Run("none_effort_plain_conversation_untouched", func(t *testing.T) {
		svc := &OpenAIGatewayService{cache: &reasoningRecordingCache{}}
		c, _ := deepSeekNativeReplayContext(21, "boundary-none")
		body := []byte(`{"model":"deepseek-v4.1-flash","reasoning":{"effort":"none"},"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]},
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"next"}]}
		]}`)

		out := svc.prepareDeepSeekNativeReasoningReplay(c, deepseek, body)
		require.Equal(t, string(body), string(out),
			"reasoning.effort=none without reasoning items must keep assistant turns")
	})

	t.Run("max_effort_plain_conversation_demoted", func(t *testing.T) {
		svc := &OpenAIGatewayService{cache: &reasoningRecordingCache{}}
		c, _ := deepSeekNativeReplayContext(21, "boundary-max")
		body := []byte(`{"model":"deepseek-v4.1-flash","reasoning":{"effort":"max"},"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]},
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"next"}]}
		]}`)

		out := svc.prepareDeepSeekNativeReasoningReplay(c, deepseek, body)
		items := deepSeekBoundaryItems(t, out)
		require.Equal(t, "user", items[1].Get("role").String())
		require.Equal(t, "input_text", items[1].Get("content.0.type").String())
	})

	t.Run("none_effort_reasoning_fallback_still_demotes", func(t *testing.T) {
		svc := &OpenAIGatewayService{cache: &reasoningRecordingCache{}}
		c, _ := deepSeekNativeReplayContext(21, "boundary-none-fallback")
		body := []byte(`{"model":"deepseek-v4.1-flash","reasoning":{"effort":"none"},"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]},
			{"type":"reasoning","id":"rs_none_missing","summary":[]},
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"next"}]}
		]}`)

		out := svc.prepareDeepSeekNativeReasoningReplay(c, deepseek, body)
		require.False(t, gjson.GetBytes(out, `input.#(type=="reasoning")`).Exists(),
			"uncached reasoning must never be forwarded without reasoning_text")
		answer, ok := deepSeekBoundaryMessageByText(deepSeekBoundaryItems(t, out), "answer")
		require.True(t, ok)
		require.Equal(t, "user", answer.Get("role").String(),
			"existing none+reasoning fallback must stay compatible")
		require.Equal(t, "input_text", answer.Get("content.0.type").String())
	})
}
