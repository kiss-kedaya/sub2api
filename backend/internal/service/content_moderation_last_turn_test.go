package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// An operator reading a block wants the text that triggered it, parsed out of
// the request body — not a summary and not every earlier turn.
func TestExtractContentModerationLastUserTurn_ParsesOnlyLatestTurn(t *testing.T) {
	body := []byte(`{"messages":[
		{"role":"system","content":"system prompt"},
		{"role":"user","content":"第一轮：请帮我写个脚本"},
		{"role":"assistant","content":"好的"},
		{"role":"user","content":[{"type":"text","text":"第二轮：这段内容应该被审计看到"}]}
	]}`)
	got := ExtractContentModerationLastUserTurn(ContentModerationProtocolOpenAIChat, body)
	require.Equal(t, "第二轮：这段内容应该被审计看到", got)
	require.NotContains(t, got, "第一轮")
	require.NotContains(t, got, "system prompt")
}

// The log excerpt must not be cut at the moderation-input ceiling: a long last
// turn has to come back whole so the operator can read it.
func TestExtractContentModerationLastUserTurn_KeepsLongTurn(t *testing.T) {
	long := strings.Repeat("长", maxModerationInputRunes+500)
	body := []byte(`{"messages":[{"role":"user","content":"` + long + `"}]}`)
	got := ExtractContentModerationLastUserTurn(ContentModerationProtocolOpenAIChat, body)
	require.Equal(t, len([]rune(long)), len([]rune(got)))
}

// Responses-style payloads resolve the same way.
func TestExtractContentModerationLastUserTurn_Responses(t *testing.T) {
	body := []byte(`{"input":[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"old turn"}]},
		{"type":"message","role":"assistant","content":[{"type":"output_text","text":"reply"}]},
		{"type":"message","role":"user","content":[{"type":"input_text","text":"latest turn"}]}
	]}`)
	require.Equal(t, "latest turn", ExtractContentModerationLastUserTurn(ContentModerationProtocolOpenAIResponses, body))
}

// Codex/GPT 的 Responses 请求尾部常跟 function_call / function_call_output。
// 详情展示必须回溯到最近一条 user 文本，而不是因为末条不是 user 就只剩上游错误。
func TestExtractContentModerationLastUserTurn_ResponsesTrailingToolCall(t *testing.T) {
	body := []byte(`{"input":[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"帮我检查这段代码"}]},
		{"type":"function_call","call_id":"call_1","name":"shell","arguments":"{}"},
		{"type":"function_call_output","call_id":"call_1","output":"ok"}
	]}`)
	require.Equal(t, "帮我检查这段代码", ExtractContentModerationLastUserTurn(ContentModerationProtocolOpenAIResponses, body))
}

// 尾部 reasoning/summary 同样不能顶替用户输入。
func TestExtractContentModerationLastUserTurn_ResponsesTrailingReasoningSummary(t *testing.T) {
	body := []byte(`{"input":[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"真实用户输入"}]},
		{"type":"reasoning","summary":[{"type":"summary_text","text":"模型内部摘要"}]}
	]}`)
	require.Equal(t, "真实用户输入", ExtractContentModerationLastUserTurn(ContentModerationProtocolOpenAIResponses, body))
}
