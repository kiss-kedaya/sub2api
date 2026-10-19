package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// 上游原文（实测）：
//
//	claude-opus-5-5 requires adaptive thinking; omit thinking or use
//	thinking.type=adaptive and output_config.effort
//
//	claude-sonnet-5-5 requires adaptive thinking or thinking.type=between_tools
//
//	"thinking.type.enabled" is not supported for this model.
//
// fable-5 全系连 disabling 都不允许（always-on adaptive）。
func TestAnthropicModelRequiresAdaptiveThinking(t *testing.T) {
	tests := []struct {
		model string
		want  bool
	}{
		// 有上游原文的型号族
		{"claude-opus-5-5", true},
		{"claude-opus-5.5", true},
		{"claude-sonnet-5-5", true},
		{"claude-opus-5-6", true},
		{"claude-sonnet-5-6", true},
		{"claude-fable-5", true},
		{"claude-fable-5-1", true},
		// 带厂商前缀的别名（claudeVersionRe 未锚定，必须同样命中）
		{"anthropic/claude-opus-5.5", true},
		{"us.anthropic.claude-opus-5-5-v1", true},
		{"openrouter/anthropic/claude-fable-5-1", true},

		// claude-opus-5 故意不在内：它的 400 是 ".enabled.budget_tokens: Field required"，
		// 即 enabled 仍可用、只是必须带预算。
		{"claude-opus-5", false},
		{"us.anthropic.claude-opus-5-v1", false},
		{"claude-sonnet-5", false},
		// 5.5 之前的型号仍是老协议
		{"claude-opus-4-7", false},
		{"claude-opus-4-8", false},
		{"claude-sonnet-4-6", false},
		{"claude-haiku-4-5", false},
		{"claude-3-opus-20240229", false},
		// 5.5 不能被次版本前缀匹配误伤
		{"claude-opus-5-50", true},
		{"", false},
		{"gpt-5.6-sol", false},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			require.Equal(t, tt.want, anthropicModelRequiresAdaptiveThinking(tt.model))
		})
	}
}

func TestSanitizeAnthropicThinkingForModel(t *testing.T) {
	t.Run("enabled 转 adaptive 并删掉预算", func(t *testing.T) {
		body := []byte(`{"model":"claude-opus-5-5","thinking":{"type":"enabled","budget_tokens":10000},"max_tokens":32000,"messages":[]}`)
		out := sanitizeAnthropicThinkingForModel(body, "claude-opus-5-5")

		require.Equal(t, "adaptive", gjson.GetBytes(out, "thinking.type").String())
		require.False(t, gjson.GetBytes(out, "thinking.budget_tokens").Exists())
		// 其它字段一个字节都不能动
		require.Equal(t, int64(32000), gjson.GetBytes(out, "max_tokens").Int())
		require.Equal(t, "claude-opus-5-5", gjson.GetBytes(out, "model").String())
	})

	t.Run("sonnet 5.5 走同一条路", func(t *testing.T) {
		body := []byte(`{"thinking":{"type":"enabled","budget_tokens":1024},"messages":[]}`)
		out := sanitizeAnthropicThinkingForModel(body, "anthropic/claude-sonnet-5.5")
		require.Equal(t, "adaptive", gjson.GetBytes(out, "thinking.type").String())
		require.False(t, gjson.GetBytes(out, "thinking.budget_tokens").Exists())
	})

	t.Run("已经是 adaptive 时字节级 no-op", func(t *testing.T) {
		body := []byte(`{"thinking":{"type":"adaptive","budget_tokens":5000},"messages":[]}`)
		require.Equal(t, string(body), string(sanitizeAnthropicThinkingForModel(body, "claude-opus-5-5")))
	})

	// 上游实测（2026-10-19）：opus-5-5 对 disabled 同样返回
	// "requires adaptive thinking"，所以必须整段删除（等价于省略 thinking）。
	t.Run("disabled 整段删除", func(t *testing.T) {
		body := []byte(`{"model":"claude-opus-5-5","thinking":{"type":"disabled"},"max_tokens":1024,"messages":[]}`)
		out := sanitizeAnthropicThinkingForModel(body, "claude-opus-5-5")
		require.False(t, gjson.GetBytes(out, "thinking").Exists())
		require.Equal(t, int64(1024), gjson.GetBytes(out, "max_tokens").Int())
	})

	t.Run("disabled 连同 budget_tokens 一起消失", func(t *testing.T) {
		body := []byte(`{"thinking":{"type":"disabled","budget_tokens":1024},"messages":[]}`)
		out := sanitizeAnthropicThinkingForModel(body, "claude-sonnet-5-5")
		require.False(t, gjson.GetBytes(out, "thinking").Exists())
	})

	t.Run("disabled 删除后一并清理依赖 thinking 的 context_management", func(t *testing.T) {
		body := []byte(`{"thinking":{"type":"disabled"},"context_management":{"edits":[{"type":"clear_thinking_20251015"},{"type":"compact_20251015"}]},"messages":[]}`)
		out := sanitizeAnthropicThinkingForModel(body, "claude-opus-5-5")
		require.False(t, gjson.GetBytes(out, "thinking").Exists())
		require.Len(t, gjson.GetBytes(out, "context_management.edits").Array(), 1)
		require.Equal(t, "compact_20251015", gjson.GetBytes(out, "context_management.edits.0.type").String())
	})

	// sonnet 5.5 接受 between_tools；opus 5.5 不接受（实测 400），要转 adaptive。
	t.Run("sonnet between_tools 原样保留", func(t *testing.T) {
		body := []byte(`{"thinking":{"type":"between_tools"},"messages":[]}`)
		require.Equal(t, string(body), string(sanitizeAnthropicThinkingForModel(body, "claude-sonnet-5-5")))
	})

	t.Run("opus between_tools 转 adaptive", func(t *testing.T) {
		body := []byte(`{"thinking":{"type":"between_tools","budget_tokens":2048},"messages":[]}`)
		out := sanitizeAnthropicThinkingForModel(body, "claude-opus-5-5")
		require.Equal(t, "adaptive", gjson.GetBytes(out, "thinking.type").String())
		require.False(t, gjson.GetBytes(out, "thinking.budget_tokens").Exists())
	})

	t.Run("老模型不碰", func(t *testing.T) {
		body := []byte(`{"thinking":{"type":"enabled","budget_tokens":10000},"messages":[]}`)
		require.Equal(t, string(body), string(sanitizeAnthropicThinkingForModel(body, "claude-opus-4-8")))
		require.Equal(t, string(body), string(sanitizeAnthropicThinkingForModel(body, "claude-opus-5")))
		disabled := []byte(`{"thinking":{"type":"disabled"},"messages":[]}`)
		require.Equal(t, string(disabled), string(sanitizeAnthropicThinkingForModel(disabled, "claude-opus-4-8")))
	})

	t.Run("无 thinking / 非对象都不碰", func(t *testing.T) {
		for _, raw := range []string{
			`{"thinking":{"type":"interleaved"}}`,
			`{"messages":[]}`,
			`{"thinking":"adaptive"}`,
			`{"thinking":null}`,
		} {
			body := []byte(raw)
			require.Equal(t, raw, string(sanitizeAnthropicThinkingForModel(body, "claude-opus-5-5")))
		}
	})

	t.Run("空 body 安全", func(t *testing.T) {
		require.Nil(t, sanitizeAnthropicThinkingForModel(nil, "claude-opus-5-5"))
		require.Empty(t, sanitizeAnthropicThinkingForModel([]byte{}, "claude-opus-5-5"))
	})
}

// 端到端：apikey 直连账号（无 passthrough）+ claude-opus-5-5，
// 客户端发 thinking.type=enabled → 出站必须是 adaptive。
func TestForwardAnthropicAPIKeyConvertsEnabledThinkingForOpus55(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &anthropicHTTPUpstreamRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5-5","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":10,"output_tokens":2}}`)),
		},
	}
	cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}
	svc := &GatewayService{
		cfg:                  cfg,
		responseHeaderFilter: compileResponseHeaderFilter(cfg),
		httpUpstream:         upstream,
		rateLimitService:     &RateLimitService{},
		deferredService:      &DeferredService{},
	}
	account := &Account{
		ID: 29168, Name: "custom-relay.example.com", Platform: PlatformAnthropic, Type: AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{"api_key": "k", "base_url": "https://api.anthropic.com"},
		Status:      StatusActive, Schedulable: true,
	}

	body := []byte(`{"model":"claude-opus-5-5","max_tokens":32000,"stream":false,` +
		`"thinking":{"type":"enabled","budget_tokens":10000},` +
		`"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`)
	parsed := &ParsedRequest{Body: NewRequestBodyRef(body), Model: "claude-opus-5-5"}

	_, err := svc.Forward(context.Background(), c, account, parsed)
	require.NoError(t, err)

	wire := upstream.lastBody
	require.NotEmpty(t, wire)
	require.Equal(t, "adaptive", gjson.GetBytes(wire, "thinking.type").String())
	require.False(t, gjson.GetBytes(wire, "thinking.budget_tokens").Exists())
	require.Equal(t, "claude-opus-5-5", gjson.GetBytes(wire, "model").String())
	require.Equal(t, int64(32000), gjson.GetBytes(wire, "max_tokens").Int())
	require.Equal(t, "hi", gjson.GetBytes(wire, "messages.0.content.0.text").String())
}

// 端到端：CC 入站（/v1/chat/completions）经 CC→Responses→Anthropic 转换后，
// 合成的 thinking.type=enabled 必须在出站前改成 adaptive，否则 opus-5-5 必 400。
func TestForwardAsChatCompletionsRewritesSynthesizedThinkingForOpus55(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &anthropicHTTPUpstreamRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body: io.NopCloser(strings.NewReader(strings.Join([]string{
				`event: message_start`,
				`data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude-opus-5-5","usage":{"input_tokens":3,"output_tokens":0}}}`,
				``,
				`event: content_block_start`,
				`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":"ok"}}`,
				``,
				`event: message_delta`,
				`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`,
				``,
				`event: message_stop`,
				`data: {"type":"message_stop"}`,
				``,
			}, "\n"))),
		},
	}
	cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}
	svc := &GatewayService{
		cfg:                  cfg,
		responseHeaderFilter: compileResponseHeaderFilter(cfg),
		httpUpstream:         upstream,
		rateLimitService:     &RateLimitService{},
		deferredService:      &DeferredService{},
	}
	account := &Account{
		ID: 201, Name: "anthropic-apikey-cc", Platform: PlatformAnthropic, Type: AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{"api_key": "k", "base_url": "https://api.anthropic.com"},
		Status:      StatusActive, Schedulable: true,
	}

	body := []byte(`{"model":"claude-opus-5-5","stream":false,"reasoning_effort":"high",` +
		`"messages":[{"role":"user","content":"hi"}]}`)

	_, _ = svc.ForwardAsChatCompletions(context.Background(), c, account, body, nil)

	wire := upstream.lastBody
	require.NotEmpty(t, wire, "upstream request must have been built")
	require.Equal(t, "adaptive", gjson.GetBytes(wire, "thinking.type").String(),
		"CC→Anthropic conversion synthesizes enabled; it must be rewritten to adaptive")
	require.False(t, gjson.GetBytes(wire, "thinking.budget_tokens").Exists())
	require.Equal(t, "claude-opus-5-5", gjson.GetBytes(wire, "model").String())
}
