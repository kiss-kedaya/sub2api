package service

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestForwardOpenAIWSV2_ClientCancellationClosesUpstreamAndEstimates covers the HTTP/SSE
// ingress: the client cancels after partial output while the upstream still has a
// terminal event queued. The upstream is closed at once and the queued terminal event
// is never read, so the turn is billed from an estimate of what was produced instead
// of the usage that event would have carried.
func TestForwardOpenAIWSV2_ClientCancellationClosesUpstreamAndEstimates(t *testing.T) {
	gin.SetMode(gin.TestMode)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	writer := &cancelOnFirstWriteResponseWriter{cancel: cancel}
	c, _ := gin.CreateTestContext(writer)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
	c.Request.Header.Set("User-Agent", "unit-test-agent/1.0")

	captureConn := &openAIWSCaptureConn{
		events: [][]byte{
			[]byte(`{"type":"response.created","response":{"id":"resp_cancel_1","model":"gpt-5.5"}}`),
			[]byte(`{"type":"response.output_text.delta","delta":"partial"}`),
			[]byte(`{"type":"response.completed","response":{"id":"resp_cancel_1","model":"gpt-5.5","usage":{"input_tokens":3,"output_tokens":5}}}`),
		},
	}

	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.APIKeyEnabled = true
	cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
	cfg.Gateway.OpenAIWS.IngressModeDefault = OpenAIWSIngressModeCtxPool
	cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
	cfg.Gateway.OpenAIWS.MinIdlePerAccount = 0
	cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
	cfg.Gateway.OpenAIWS.QueueLimitPerConn = 8
	cfg.Gateway.OpenAIWS.DialTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 5
	cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3

	pool := newOpenAIWSConnPool(cfg)
	pool.setClientDialerForTest(&openAIWSCaptureDialer{conn: captureConn})
	svc := &OpenAIGatewayService{
		cfg:              cfg,
		httpUpstream:     &httpUpstreamRecorder{},
		cache:            &stubGatewayCache{},
		openaiWSResolver: NewOpenAIWSProtocolResolver(cfg),
		toolCorrector:    NewCodexToolCorrector(),
		openaiWSPool:     pool,
	}
	account := &Account{
		ID:          9101,
		Name:        "openai-ws-client-cancel",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{"api_key": "sk-test"},
		Extra: map[string]any{
			"openai_apikey_responses_websockets_v2_mode": OpenAIWSIngressModeCtxPool,
		},
	}

	result, err := svc.Forward(ctx, c, account, []byte(`{"model":"gpt-5.5","stream":true,"input":"Please write a long story about a fox who learns to code."}`))

	require.ErrorIs(t, err, context.Canceled, "the client leaving is reported as a cancellation, not an upstream failure")
	require.NotNil(t, result, "what was observed must still reach billing")
	require.True(t, result.ClientDisconnect)
	require.Equal(t, "resp_cancel_1", result.RequestID)
	require.Empty(t, result.UpstreamTerminalEvent)
	require.NotContains(t, writer.body.String(), "response.failed")

	require.Positive(t, result.Usage.InputTokens, "input is estimated from the request body")
	require.Equal(t, incompleteCacheReadTokens(result.Usage.InputTokens), result.Usage.CacheReadInputTokens)
	require.True(t, result.Usage.CacheReadEstimated)
	require.Equal(t, estimateTokensForText("partial"), result.Usage.OutputTokens, "output is estimated from the streamed content")

	captureConn.mu.Lock()
	closed, unread := captureConn.closed, len(captureConn.events)
	captureConn.mu.Unlock()
	require.True(t, closed, "the upstream connection is closed, not returned to the pool")
	require.Equal(t, 1, unread, "the terminal event was left unread: nothing is drained")
}

type cancelOnFirstWriteResponseWriter struct {
	mu     sync.Mutex
	header http.Header
	body   bytes.Buffer
	cancel context.CancelFunc
	writes int
	status int
}

func (w *cancelOnFirstWriteResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *cancelOnFirstWriteResponseWriter) WriteHeader(status int) {
	w.status = status
}

func (w *cancelOnFirstWriteResponseWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.writes == 0 && w.cancel != nil {
		w.cancel()
	}
	w.writes++
	return w.body.Write(p)
}

func (w *cancelOnFirstWriteResponseWriter) Flush() {}
