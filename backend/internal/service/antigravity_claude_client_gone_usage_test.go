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

// Antigravity Claude (Forward, Gemini SSE converted to Claude SSE): the client leaving
// closes the upstream at once instead of draining it, and a request the upstream accepted
// is billed from what was observed plus the shared estimates.

// driveAntigravityClaudeGone runs Forward against a held upstream and makes the client
// leave: by cancelling the request context once waitFor shows up in the relayed bytes,
// or, with failWrites, by failing every client write while the context stays alive.
func driveAntigravityClaudeGone(t *testing.T, events []string, waitFor string, failWrites bool) geminiGoneOutcome {
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
	body := geminiGoneClaudeBody(true)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(string(body))).WithContext(ctx)
	done := make(chan geminiGoneOutcome, 1)
	go func() {
		result, err := svc.Forward(ctx, c, antigravityGoneAccount(), body, false)
		done <- geminiGoneOutcome{result: result, err: err}
	}()
	if !failWrites {
		select {
		case <-accepted:
		case <-time.After(5 * time.Second):
			t.Fatal("the upstream was never reached")
		}
		if waitFor != "" {
			require.Eventually(t, func() bool { return strings.Contains(rec.Snapshot(), waitFor) },
				5*time.Second, 5*time.Millisecond, "the content should reach the client")
		}
		cancel()
	}
	select {
	case out := <-done:
		out.body, out.up = rec.Snapshot(), up
		return out
	case <-time.After(5 * time.Second):
		t.Fatal("Forward did not return after the client left")
	}
	return geminiGoneOutcome{}
}

// No usage figure: output from the content relayed, input from the Claude request body.
func TestAntigravityForward_ClaudeStreamClientGoneEstimatesMissingUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	events := []string{geminiGoneCodeAssist(geminiGoneBareEvent1), geminiGoneCodeAssist(geminiGoneBareEvent2)}
	out := driveAntigravityClaudeGone(t, events, " a time", false)
	require.NoErrorf(t, out.err, "result=%v", out.result)
	require.NotNil(t, out.result)
	require.True(t, out.result.ClientDisconnect)
	total := estimateRequestInputTokens(geminiGoneClaudeBody(true))
	require.Positive(t, total)
	split := splitIncompleteInput(total)
	require.Equal(t, split.Fresh, out.result.Usage.InputTokens)
	require.Equal(t, split.CacheRead, out.result.Usage.CacheReadInputTokens)
	require.True(t, out.result.Usage.CacheReadEstimated)
	require.Equal(t, 4, out.result.Usage.OutputTokens) // "Once upon a time": 16 characters
	requireAntigravityUpstreamClosed(t, out.up)
}

// A failed client write is a disconnect: the upstream is closed at once instead of being
// drained until it ends on its own (this upstream never ends by itself).
func TestAntigravityForward_ClaudeStreamClientWriteFailureClosesUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	out := driveAntigravityClaudeGone(t, []string{geminiGoneCodeAssist(geminiGoneBareEvent1)}, "", true)
	require.NoErrorf(t, out.err, "result=%v", out.result)
	require.NotNil(t, out.result)
	require.True(t, out.result.ClientDisconnect)
	requireAntigravityUpstreamClosed(t, out.up)
}
