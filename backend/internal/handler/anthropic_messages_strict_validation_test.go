package handler

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateStrictOpusMessagesRequest(t *testing.T) {
	for _, tt := range []struct {
		name    string
		model   string
		body    string
		message string
		ok      bool
	}{
		{
			name:  "缺 max_tokens",
			model: "claude-opus-5-5",
			body:  `{"model":"claude-opus-5-5","messages":[{"role":"user","content":"hi"}]}`,
			// 必须 400，且不得被补成 128000 / 4096 后放行。
			message: "max_tokens: Field required",
		},
		{
			name:    "max_tokens 小于 1",
			model:   "claude-opus-5-5",
			body:    `{"model":"claude-opus-5-5","max_tokens":0,"messages":[{"role":"user","content":"hi"}]}`,
			message: "max_tokens: Field required",
		},
		{
			name:    "temperature 越界",
			model:   "claude-opus-5-5",
			body:    `{"model":"claude-opus-5-5","max_tokens":16,"temperature":5,"messages":[{"role":"user","content":"hi"}]}`,
			message: "temperature: range: 0..1",
		},
		{
			name:    "text 块缺 text",
			model:   "claude-opus-5-5",
			body:    `{"model":"claude-opus-5-5","max_tokens":16,"messages":[{"role":"user","content":[{"type":"text"}]}]}`,
			message: "messages.0.content.0.text: text content blocks must contain non-whitespace text",
		},
		{
			name:    "messages 为空数组",
			model:   "claude-opus-5-5",
			body:    `{"model":"claude-opus-5-5","max_tokens":16,"messages":[]}`,
			message: "field messages is required",
		},
		{
			name:    "messages 缺失",
			model:   "claude-opus-5-5",
			body:    `{"model":"claude-opus-5-5","max_tokens":16}`,
			message: "field messages is required",
		},
		{
			name:    "开 thinking 又传非默认 temperature",
			model:   "claude-opus-5-5",
			body:    `{"model":"claude-opus-5-5","max_tokens":1200,"temperature":0.5,"thinking":{"type":"adaptive"},"messages":[{"role":"user","content":"hi"}]}`,
			message: "temperature may only be set to 1 when thinking is enabled",
		},
		{
			name:    "开 thinking 又传 top_p",
			model:   "claude-opus-5-5",
			body:    `{"model":"claude-opus-5-5","max_tokens":1200,"top_p":0.3,"thinking":{"type":"adaptive"},"messages":[{"role":"user","content":"hi"}]}`,
			message: "top_p may only be set to 1 when thinking is enabled",
		},
		{
			name:  "合格的 thinking 请求放行",
			model: "claude-opus-5-5",
			body:  `{"model":"claude-opus-5-5","max_tokens":1200,"thinking":{"type":"adaptive"},"output_config":{"effort":"high"},"messages":[{"role":"user","content":"A bat and a ball cost $1.10 total."}]}`,
			ok:    true,
		},
		{
			name:  "thinking.type=enabled 不在本函数拦（交由既有 adaptive 门处理）",
			model: "claude-opus-5-5",
			body:  `{"model":"claude-opus-5-5","max_tokens":1200,"thinking":{"type":"enabled","budget_tokens":2048},"messages":[{"role":"user","content":"hi"}]}`,
			ok:    true,
		},
		{
			name:  "非 opus-5-5 不拦",
			model: "claude-sonnet-4-5",
			body:  `{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":[{"type":"text"}]}]}`,
			ok:    true,
		},
		{
			name:  "带厂商前缀 / 点号写法同样命中",
			model: "anthropic/claude-opus-5.5",
			body:  `{"model":"anthropic/claude-opus-5.5","messages":[{"role":"user","content":"hi"}]}`,
			// IsOpus55 归一化后应识别为该模型，因此缺 max_tokens 要拦。
			message: "max_tokens: Field required",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			message, ok := ValidateStrictOpusMessagesRequest(tt.model, []byte(tt.body))
			require.Equal(t, tt.ok, ok)
			require.Equal(t, tt.message, message)
		})
	}
}
