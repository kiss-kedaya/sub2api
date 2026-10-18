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
