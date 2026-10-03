//go:build unit

package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const thinkingDisplayUpdatesBody = `{"model":"claude-sonnet-4-6","max_tokens":64,"thinking":{"type":"adaptive","display":"updates"},"messages":[{"role":"user","content":"hi"}]}`

func TestEnsureThinkingDisplayUpdatesBeta(t *testing.T) {
	token := claude.BetaThinkingDisplayUpdates
	for _, tc := range []struct {
		name string
		beta string
		body string
		drop map[string]struct{}
		want string
	}{
		{name: "updates without beta adds token", beta: "", body: thinkingDisplayUpdatesBody, want: token},
		{name: "updates appends to existing betas", beta: "a-1,b-2", body: thinkingDisplayUpdatesBody, want: "a-1,b-2," + token},
		{name: "already present is not duplicated", beta: "a-1, " + token, body: thinkingDisplayUpdatesBody, want: "a-1, " + token},
		{name: "case and whitespace in body value", beta: "a-1", body: `{"thinking":{"display":"  UPDATES "}}`, want: "a-1," + token},
		{name: "summarized untouched", beta: "a-1", body: `{"thinking":{"display":"summarized"}}`, want: "a-1"},
		{name: "omitted untouched", beta: "", body: `{"thinking":{"display":"omitted"}}`, want: ""},
		{name: "no display untouched", beta: "a-1", body: `{"thinking":{"type":"adaptive"}}`, want: "a-1"},
		{name: "non-string display untouched", beta: "a-1", body: `{"thinking":{"display":true}}`, want: "a-1"},
		{name: "empty body untouched", beta: "a-1", body: ``, want: "a-1"},
		{name: "admin drop policy respected", beta: "a-1", body: thinkingDisplayUpdatesBody, drop: map[string]struct{}{token: {}}, want: "a-1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, ensureThinkingDisplayUpdatesBeta(tc.beta, []byte(tc.body), tc.drop))
		})
	}
}

func TestApplyThinkingDisplayUpdatesBetaHeader_MergesMultiLineClientBeta(t *testing.T) {
	h := http.Header{}
	h["anthropic-beta"] = []string{"a-1", "b-2"}
	h["Anthropic-Beta"] = []string{"c-3"}

	applyThinkingDisplayUpdatesBetaHeader(h, []byte(thinkingDisplayUpdatesBody))

	require.Equal(t, []string{"a-1,b-2,c-3," + claude.BetaThinkingDisplayUpdates}, h["anthropic-beta"])
	require.Empty(t, h["Anthropic-Beta"], "canonical duplicate must be collapsed")
}

func TestApplyThinkingDisplayUpdatesBetaHeader_LeavesHeaderByteIdenticalWhenNotNeeded(t *testing.T) {
	h := http.Header{}
	h["anthropic-beta"] = []string{"a-1", "b-2"}

	applyThinkingDisplayUpdatesBetaHeader(h, []byte(`{"thinking":{"display":"summarized"}}`))
	require.Equal(t, []string{"a-1", "b-2"}, h["anthropic-beta"], "requests without display=updates stay byte-identical")

	h["anthropic-beta"] = []string{"a-1", claude.BetaThinkingDisplayUpdates}
	applyThinkingDisplayUpdatesBetaHeader(h, []byte(thinkingDisplayUpdatesBody))
	require.Equal(t, []string{"a-1", claude.BetaThinkingDisplayUpdates}, h["anthropic-beta"], "client-supplied token is forwarded as-is")
}

// OAuth 伪装路径会整份替换客户端 beta：补的 token 必须出现在最终上游头里。
func TestBuildUpstreamRequest_OAuthMimicAddsThinkingDisplayUpdatesBeta(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Request.Header.Set("anthropic-beta", "custom-client-beta")

	svc := &GatewayService{cfg: &config.Config{}}
	account := &Account{Platform: PlatformAnthropic, Type: AccountTypeOAuth}
	req, _, err := svc.buildUpstreamRequest(context.Background(), c, account,
		[]byte(thinkingDisplayUpdatesBody), "test-token", "oauth", "claude-sonnet-4-6", false, true)
	require.NoError(t, err)

	beta := getHeaderRaw(req.Header, "anthropic-beta")
	require.True(t, anthropicBetaTokensContains(beta, claude.BetaThinkingDisplayUpdates), "beta=%s", beta)
	require.True(t, anthropicBetaTokensContains(beta, claude.BetaOAuth), "mimic betas must remain, beta=%s", beta)
}

func TestBuildUpstreamRequest_OAuthMimicWithoutDisplayUpdatesKeepsMimicBetas(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	svc := &GatewayService{cfg: &config.Config{}}
	account := &Account{Platform: PlatformAnthropic, Type: AccountTypeOAuth}
	req, _, err := svc.buildUpstreamRequest(context.Background(), c, account,
		[]byte(`{"model":"claude-sonnet-4-6","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`),
		"test-token", "oauth", "claude-sonnet-4-6", false, true)
	require.NoError(t, err)

	beta := getHeaderRaw(req.Header, "anthropic-beta")
	require.False(t, anthropicBetaTokensContains(beta, claude.BetaThinkingDisplayUpdates),
		"the token must not leak into requests that do not use display=updates, beta=%s", beta)
}

// API Key 透传路径原样转发客户端 beta：只在 display=updates 时补 token。
func TestBuildUpstreamRequestAnthropicAPIKeyPassthrough_AddsThinkingDisplayUpdatesBeta(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Request.Header.Set("anthropic-beta", "client-beta-1")

	svc := &GatewayService{cfg: &config.Config{
		Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}},
	}}
	account := &Account{
		Platform:    PlatformAnthropic,
		Type:        AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "k", "base_url": "https://relay.example.com"},
		Extra:       map[string]any{"anthropic_passthrough": true},
	}

	req, _, err := svc.buildUpstreamRequestAnthropicAPIKeyPassthrough(context.Background(), c, account,
		[]byte(thinkingDisplayUpdatesBody), "k")
	require.NoError(t, err)
	require.Equal(t, "client-beta-1,"+claude.BetaThinkingDisplayUpdates, getHeaderRaw(req.Header, "anthropic-beta"))
}
