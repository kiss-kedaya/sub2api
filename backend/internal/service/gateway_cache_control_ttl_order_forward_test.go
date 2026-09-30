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

// 生产实测：apikey 中转账号 30761/30762（platform=anthropic，anthropic_passthrough 未开）
// 6h 194 次 400，原文
//
//	a ttl='1h' cache_control block must not come after a ttl='5m' cache_control block.
//
// 这一条链路里网关不注入任何 ttl，违规全部来自客户端 body，但出站前必须被规整，
// 否则用户看到的是一次必然失败的转发。
func TestForwardAnthropicAPIKeyNormalizesTTLOrderBeforeUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Request.Header.Set("User-Agent", "Go-http-client/2.0")
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &anthropicHTTPUpstreamRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid-ttl"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"msg_1","type":"message","role":"assistant","model":"claude-sonnet-5-5","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":10,"output_tokens":2}}`)),
		},
	}

	cfg := &config.Config{
		Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize},
	}
	svc := &GatewayService{
		cfg:                  cfg,
		responseHeaderFilter: compileResponseHeaderFilter(cfg),
		httpUpstream:         upstream,
		rateLimitService:     &RateLimitService{},
		deferredService:      &DeferredService{},
	}

	account := &Account{
		ID:          30761,
		Name:        "ocapi.cc",
		Platform:    PlatformAnthropic,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "upstream-key",
			"base_url": "https://api.anthropic.com",
		},
		Status:      StatusActive,
		Schedulable: true,
	}

	// 客户端原样：tools 断点没写 ttl（默认 5m），system 写 5m，messages 写 1h。
	body := []byte(`{"model":"claude-sonnet-5-5","max_tokens":32000,"stream":false,` +
		`"tools":[{"name":"Bash","input_schema":{"type":"object"},"cache_control":{"type":"ephemeral"}}],` +
		`"system":[{"type":"text","text":"sys","cache_control":{"type":"ephemeral","ttl":"5m"}}],` +
		`"messages":[{"role":"user","content":[{"type":"text","text":"hi","cache_control":{"type":"ephemeral","ttl":"1h"}}]}]}`)
	parsed := &ParsedRequest{Body: NewRequestBodyRef(body), Model: "claude-sonnet-5-5"}

	_, err := svc.Forward(context.Background(), c, account, parsed)
	require.NoError(t, err)

	wire := upstream.lastBody
	require.NotEmpty(t, wire, "必须真的走到上游")
	assertNoTTLOrderViolation(t, wire)

	require.Equal(t, cacheTTLTarget1h, gjson.GetBytes(wire, "tools.0.cache_control.ttl").String())
	require.Equal(t, cacheTTLTarget1h, gjson.GetBytes(wire, "system.0.cache_control.ttl").String())
	require.Equal(t, cacheTTLTarget1h, gjson.GetBytes(wire, "messages.0.content.0.cache_control.ttl").String())

	// 除 ttl 外一个字节都不能动。
	require.Equal(t, "Bash", gjson.GetBytes(wire, "tools.0.name").String())
	require.Equal(t, "ephemeral", gjson.GetBytes(wire, "tools.0.cache_control.type").String())
	require.Equal(t, "claude-sonnet-5-5", gjson.GetBytes(wire, "model").String())
	require.Equal(t, int64(32000), gjson.GetBytes(wire, "max_tokens").Int())
	require.Equal(t, "hi", gjson.GetBytes(wire, "messages.0.content.0.text").String())
	require.Equal(t, "sys", gjson.GetBytes(wire, "system.0.text").String())
}
