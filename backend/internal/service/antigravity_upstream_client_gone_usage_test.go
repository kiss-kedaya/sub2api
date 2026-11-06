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

// Antigravity upstream passthrough (ForwardUpstream, an Anthropic compatible /v1/messages
// stream): the client leaving closes the upstream at once instead of draining it, and a
// request the upstream accepted is billed from what was observed plus the shared estimates.

const (
	antigravityUpstreamDelta1 = "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Once upon\"}}\n\n"
	antigravityUpstreamDelta2 = "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\" a time\"}}\n\n"
)

func driveAntigravityUpstreamGone(t *testing.T, stream bool, events []string, waitFor string, failWrites bool) geminiGoneOutcome {
	t.Helper()
	up := newGeminiGoneUpstream(t, events...)
	target, err := url.Parse(up.server.URL)
	require.NoError(t, err)
	accepted, once := make(chan struct{}), sync.Once{}
	svc := &AntigravityGatewayService{
		settingService: NewSettingService(&antigravitySettingRepoStub{}, &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}),
		httpUpstream: &geminiGoneRedirectUpstream{target: target, client: up.server.Client(),
			onAccepted: func() { once.Do(func() { close(accepted) }) }},
	}
	account := &Account{
		ID: 8203, Name: "antigravity-upstream-gone", Platform: PlatformAntigravity, Type: AccountTypeUpstream,
		Status: StatusActive, Concurrency: 1,
		Credentials: map[string]any{"base_url": up.server.URL, "api_key": "test-key"},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rec := &geminiGoneRecorder{ResponseRecorder: httptest.NewRecorder(), failWrites: failWrites}
	c, _ := gin.CreateTestContext(rec)
	body := geminiGoneClaudeBody(stream)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(string(body))).WithContext(ctx)
	done := make(chan geminiGoneOutcome, 1)
	go func() {
		result, err := svc.Forward(ctx, c, account, body, false)
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

// No usage event at all: output from the deltas relayed, input from the request body.
func TestAntigravityForwardUpstream_StreamClientGoneEstimatesMissingUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	out := driveAntigravityUpstreamGone(t, true, []string{antigravityUpstreamDelta1, antigravityUpstreamDelta2}, " a time", false)
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
func TestAntigravityForwardUpstream_ClientWriteFailureClosesUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	out := driveAntigravityUpstreamGone(t, true, []string{antigravityUpstreamDelta1}, "", true)
	require.NoErrorf(t, out.err, "result=%v", out.result)
	require.NotNil(t, out.result)
	require.True(t, out.result.ClientDisconnect)
	requireAntigravityUpstreamClosed(t, out.up)
}

// Non-stream: the client leaves while the upstream body is being read. No drain window;
// the accepted request is billed from the request body estimate.
func TestAntigravityForwardUpstream_NonStreamClientGoneBillsEstimatedInput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	out := driveAntigravityUpstreamGone(t, false, nil, "", false)
	require.NoErrorf(t, out.err, "result=%v", out.result)
	require.NotNil(t, out.result)
	require.True(t, out.result.ClientDisconnect)
	total := estimateRequestInputTokens(geminiGoneClaudeBody(false))
	require.Positive(t, total)
	require.Equal(t, splitIncompleteInput(total).Fresh, out.result.Usage.InputTokens)
	require.True(t, out.result.Usage.CacheReadEstimated)
	require.Zero(t, out.result.Usage.OutputTokens)
}
