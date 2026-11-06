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

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Claude compatible Gemini bridge (GeminiMessagesCompatService.Forward): the client leaving
// closes the upstream at once, and a request the upstream accepted is billed from what was
// observed plus the shared estimates (gemini_family_incomplete_usage.go).

func geminiGoneClaudeBody(stream bool) []byte {
	flag := "false"
	if stream {
		flag = "true"
	}
	return []byte(`{"model":"gemini-2.5-flash","max_tokens":64,"stream":` + flag +
		`,"messages":[{"role":"user","content":"` + strings.Repeat("abcd", 100) + `"}]}`)
}

// driveGeminiClaudeGone runs Forward against a held upstream and makes the client leave:
// by cancelling the request context once waitFor shows up in the relayed bytes, or, with
// failWrites, by failing every client write while the context stays alive.
func driveGeminiClaudeGone(t *testing.T, stream bool, events []string, waitFor string, failWrites bool) geminiGoneOutcome {
	t.Helper()
	up := newGeminiGoneUpstream(t, events...)
	target, err := url.Parse(up.server.URL)
	require.NoError(t, err)
	accepted, once := make(chan struct{}), sync.Once{}
	svc := newGeminiGoneService(&geminiGoneRedirectUpstream{target: target, client: up.server.Client(),
		onAccepted: func() { once.Do(func() { close(accepted) }) }})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rec := &geminiGoneRecorder{ResponseRecorder: httptest.NewRecorder(), failWrites: failWrites}
	c, _ := gin.CreateTestContext(rec)
	body := geminiGoneClaudeBody(stream)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(string(body))).WithContext(ctx)
	done := make(chan geminiGoneOutcome, 1)
	go func() {
		result, err := svc.Forward(ctx, c, geminiGoneAPIKeyAccount(), body)
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

func requireClaudeCompatUpstreamClosed(t *testing.T, up *geminiGoneUpstream) {
	t.Helper()
	select {
	case <-up.gone:
	case <-time.After(5 * time.Second):
		t.Fatal("the upstream connection was not closed")
	}
}

// Guard: usage the upstream already reported is kept exactly, no estimate on top.
func TestGeminiCompatForward_StreamClientGoneKeepsReportedUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	out := driveGeminiClaudeGone(t, true, []string{geminiGoneEvent1, geminiGoneEvent2}, " a time", false)
	require.NoErrorf(t, out.err, "result=%v", out.result)
	require.NotNil(t, out.result)
	require.True(t, out.result.ClientDisconnect)
	require.Equal(t, 120, out.result.Usage.InputTokens)
	require.Equal(t, 13, out.result.Usage.OutputTokens)
	require.False(t, out.result.Usage.CacheReadEstimated)
	requireClaudeCompatUpstreamClosed(t, out.up)
	requireGeminiGoneBilled(t, out.result, geminiGoneAPIKeyAccount(), 120, 13)
}

// No usage figure: output from the text deltas relayed, input from the Claude request body.
func TestGeminiCompatForward_StreamClientGoneEstimatesMissingUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	out := driveGeminiClaudeGone(t, true, []string{geminiGoneBareEvent1, geminiGoneBareEvent2}, " a time", false)
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
	requireGeminiGoneBilled(t, out.result, geminiGoneAPIKeyAccount(), total, 4)
}

// A failed client write is a disconnect: the upstream is closed at once instead of being
// drained until it ends on its own (this upstream never ends by itself).
func TestGeminiCompatForward_StreamClientWriteFailureClosesUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	out := driveGeminiClaudeGone(t, true, []string{geminiGoneBareEvent1}, "", true)
	require.NoErrorf(t, out.err, "result=%v", out.result)
	require.NotNil(t, out.result)
	require.True(t, out.result.ClientDisconnect)
	requireClaudeCompatUpstreamClosed(t, out.up)
}

// Non-stream: the client leaves while the upstream body is being read. No drain window;
// the accepted request is billed from the request body estimate.
func TestGeminiCompatForward_NonStreamClientGoneBillsEstimatedInput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	out := driveGeminiClaudeGone(t, false, nil, "", false)
	require.NoErrorf(t, out.err, "result=%v", out.result)
	require.NotNil(t, out.result)
	require.True(t, out.result.ClientDisconnect)
	total := estimateRequestInputTokens(geminiGoneClaudeBody(false))
	require.Positive(t, total)
	require.Equal(t, splitIncompleteInput(total).Fresh, out.result.Usage.InputTokens)
	require.True(t, out.result.Usage.CacheReadEstimated)
	require.Zero(t, out.result.Usage.OutputTokens)
	requireGeminiGoneBilled(t, out.result, geminiGoneAPIKeyAccount(), total, 0)
}
