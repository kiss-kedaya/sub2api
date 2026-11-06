//go:build unit

package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Antigravity Gemini (ForwardGemini): the client leaving closes the upstream at once
// instead of draining it, and a request the upstream accepted is billed from what was
// observed plus the shared estimates (gemini_family_incomplete_usage.go).

func antigravityGoneAccount() *Account {
	return &Account{
		ID: 8201, Name: "antigravity-gone", Platform: PlatformAntigravity, Type: AccountTypeOAuth,
		Status: StatusActive, Concurrency: 1,
		Credentials: map[string]any{
			"access_token":  "test-access-token",
			"project_id":    "test-project",
			"model_mapping": map[string]any{"gemini-2.5-flash": "gemini-2.5-flash"},
		},
	}
}

// driveAntigravityGone runs ForwardGemini against a held upstream and makes the client
// leave, the same two ways as driveGeminiGone.
func driveAntigravityGone(t *testing.T, events []string, stream bool, action string, waitEvents int, failWrites bool) geminiGoneOutcome {
	t.Helper()
	up := newGeminiGoneUpstream(t, events...)
	target, err := url.Parse(up.server.URL)
	require.NoError(t, err)
	accepted, once := make(chan struct{}), sync.Once{}
	svc := &AntigravityGatewayService{
		settingService: NewSettingService(&antigravitySettingRepoStub{}, &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}),
		tokenProvider:  &AntigravityTokenProvider{},
		httpUpstream: &geminiGoneRedirectUpstream{target: target, client: up.server.Client(),
			onAccepted: func() { once.Do(func() { close(accepted) }) }},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rec := &geminiGoneRecorder{ResponseRecorder: httptest.NewRecorder(), failWrites: failWrites}
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-2.5-flash:"+action+"?alt=sse", strings.NewReader(geminiGoneRequest)).WithContext(ctx)
	done := make(chan geminiGoneOutcome, 1)
	go func() {
		result, err := svc.ForwardGemini(ctx, c, antigravityGoneAccount(), "gemini-2.5-flash", action, stream, []byte(geminiGoneRequest), false)
		done <- geminiGoneOutcome{result: result, err: err}
	}()
	if !failWrites {
		select {
		case <-accepted:
		case <-time.After(5 * time.Second):
			t.Fatal("the upstream was never reached")
		}
		if waitEvents > 0 {
			require.Eventually(t, func() bool { return strings.Count(rec.Snapshot(), "data:") >= waitEvents },
				5*time.Second, 5*time.Millisecond, "the events should reach the client")
		}
		cancel()
	}
	select {
	case out := <-done:
		out.body, out.up = rec.Snapshot(), up
		return out
	case <-time.After(5 * time.Second):
		t.Fatal("ForwardGemini did not return after the client left")
	}
	return geminiGoneOutcome{}
}

func requireAntigravityUpstreamClosed(t *testing.T, up *geminiGoneUpstream) {
	t.Helper()
	select {
	case <-up.gone:
	case <-time.After(5 * time.Second):
		t.Fatal("the upstream connection was not closed")
	}
}

// Guard: usage the upstream already reported is kept exactly, no estimate on top.
func TestAntigravityForwardGemini_StreamClientGoneKeepsReportedUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	events := []string{geminiGoneCodeAssist(geminiGoneEvent1), geminiGoneCodeAssist(geminiGoneEvent2)}
	out := driveAntigravityGone(t, events, true, "streamGenerateContent", 2, false)
	require.NoErrorf(t, out.err, "result=%v", out.result)
	require.NotNil(t, out.result)
	require.True(t, out.result.ClientDisconnect)
	require.Equal(t, 120, out.result.Usage.InputTokens)
	require.Equal(t, 13, out.result.Usage.OutputTokens)
	require.False(t, out.result.Usage.CacheReadEstimated)
	requireAntigravityUpstreamClosed(t, out.up)
	requireGeminiGoneBilled(t, out.result, antigravityGoneAccount(), 120, 13)
}

// No usage figure: output from the relayed content, input from the request body.
func TestAntigravityForwardGemini_StreamClientGoneEstimatesMissingUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	events := []string{geminiGoneCodeAssist(geminiGoneBareEvent1), geminiGoneCodeAssist(geminiGoneBareEvent2)}
	out := driveAntigravityGone(t, events, true, "streamGenerateContent", 2, false)
	require.NoErrorf(t, out.err, "result=%v", out.result)
	require.NotNil(t, out.result)
	require.True(t, out.result.ClientDisconnect)
	require.Equal(t, 5, out.result.Usage.InputTokens)
	require.Equal(t, 95, out.result.Usage.CacheReadInputTokens)
	require.Equal(t, 4, out.result.Usage.OutputTokens)
	require.True(t, out.result.Usage.CacheReadEstimated)
	requireGeminiGoneBilled(t, out.result, antigravityGoneAccount(), 100, 4)
}

// A failed client write is a disconnect: the upstream is closed at once instead of
// being drained until it ends on its own (this upstream never ends by itself).
func TestAntigravityForwardGemini_ClientWriteFailureClosesUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	events := []string{geminiGoneCodeAssist(geminiGoneEvent1), geminiGoneCodeAssist(geminiGoneEvent2)}
	out := driveAntigravityGone(t, events, true, "streamGenerateContent", 0, true)
	require.NoError(t, out.err)
	require.NotNil(t, out.result)
	require.True(t, out.result.ClientDisconnect)
	require.Equal(t, 120, out.result.Usage.InputTokens, "the usage already parsed is kept")
	requireAntigravityUpstreamClosed(t, out.up)
}

// Non-stream: the client leaves while the upstream is being collected. No drain window;
// the accepted request is billed from the request body estimate.
func TestAntigravityForwardGemini_NonStreamClientGoneBillsEstimatedInput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	out := driveAntigravityGone(t, nil, false, "generateContent", 0, false)
	require.NoErrorf(t, out.err, "result=%v", out.result)
	require.NotNil(t, out.result)
	require.True(t, out.result.ClientDisconnect)
	require.False(t, out.result.Stream)
	require.Equal(t, 5, out.result.Usage.InputTokens)
	require.Equal(t, 95, out.result.Usage.CacheReadInputTokens)
	require.True(t, out.result.Usage.CacheReadEstimated)
	require.Zero(t, out.result.Usage.OutputTokens)
	requireGeminiGoneBilled(t, out.result, antigravityGoneAccount(), 100, 0)
}
