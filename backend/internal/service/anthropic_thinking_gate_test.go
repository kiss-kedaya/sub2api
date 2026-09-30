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

	t.Run("老模型不碰", func(t *testing.T) {
		body := []byte(`{"thinking":{"type":"enabled","budget_tokens":10000},"messages":[]}`)
		require.Equal(t, string(body), string(sanitizeAnthropicThinkingForModel(body, "claude-opus-4-8")))
		require.Equal(t, string(body), string(sanitizeAnthropicThinkingForModel(body, "claude-opus-5")))
	})

	t.Run("disabled / 无 thinking / 非对象都不碰", func(t *testing.T) {
		for _, raw := range []string{
			`{"thinking":{"type":"disabled"}}`,
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
		ID: 29168, Name: "mdkj.lol", Platform: PlatformAnthropic, Type: AccountTypeAPIKey,
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
