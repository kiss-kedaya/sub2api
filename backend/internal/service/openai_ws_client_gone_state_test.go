//go:build unit

package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// goneClientWS is a client websocket whose writes start failing like a closed socket
// from the failFrom-th write on, and whose reads report a clean close.
type goneClientWS struct {
	mu       sync.Mutex
	writes   [][]byte
	failFrom int
}

func (c *goneClientWS) Read(context.Context) (coderws.MessageType, []byte, error) {
	return 0, nil, coderws.CloseError{Code: coderws.StatusNormalClosure, Reason: "done"}
}

func (c *goneClientWS) Write(_ context.Context, _ coderws.MessageType, payload []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failFrom > 0 && len(c.writes)+1 >= c.failFrom {
		return errors.New("write: broken pipe")
	}
	c.writes = append(c.writes, append([]byte(nil), payload...))
	return nil
}

func (c *goneClientWS) Close(coderws.StatusCode, string) error { return nil }
func (c *goneClientWS) CloseNow() error                        { return nil }
func (c *goneClientWS) SetReadLimit(int64)                     {}

const (
	goneStateRequest = `{"type":"response.create","model":"gpt-5.1","stream":true,"input":"Please write a long story about a fox who learns to code."}`
	goneStateID      = "resp_gone_state"
	goneStateCreated = `{"type":"response.created","response":{"id":"resp_gone_state","model":"gpt-5.1"}}`
	goneStateDelta1  = `{"type":"response.output_text.delta","delta":"Once upon a time a quick brown fox "}`
	goneStateDelta2  = `{"type":"response.output_text.delta","delta":"sat down at a keyboard."}`
	goneStateDone    = `{"type":"response.completed","response":{"id":"resp_gone_state","model":"gpt-5.1","usage":{"input_tokens":6,"output_tokens":4}}}`
)

type goneStateRun struct {
	svc      *OpenAIGatewayService
	pool     *openAIWSConnPool
	conn     *openAIWSCaptureConn
	dialer   *openAIWSCaptureDialer
	account  *Account
	proxyErr error
	results  []*OpenAIForwardResult
	turnErrs []error
	// closedWhenSettled records, per turn, whether the upstream connection was already
	// closed when the turn reached settlement (AfterTurn).
	closedWhenSettled []bool
}

func runGoneState(t *testing.T, events []string, clientFailFrom int) *goneStateRun {
	t.Helper()
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.OAuthEnabled = true
	cfg.Gateway.OpenAIWS.APIKeyEnabled = true
	cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
	cfg.Gateway.OpenAIWS.MinIdlePerAccount = 0
	cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
	cfg.Gateway.OpenAIWS.QueueLimitPerConn = 8
	cfg.Gateway.OpenAIWS.DialTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3

	conn := &openAIWSCaptureConn{}
	for _, event := range events {
		conn.events = append(conn.events, []byte(event))
	}
	dialer := &openAIWSCaptureDialer{conn: conn}
	pool := newOpenAIWSConnPool(cfg)
	pool.setClientDialerForTest(dialer)
	svc := &OpenAIGatewayService{
		cfg:              cfg,
		httpUpstream:     &httpUpstreamRecorder{},
		cache:            &stubGatewayCache{},
		openaiWSResolver: NewOpenAIWSProtocolResolver(cfg),
		toolCorrector:    NewCodexToolCorrector(),
		openaiWSPool:     pool,
	}
	account := &Account{
		ID: 9201, Name: "openai-ws-gone-state", Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Status: StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{"api_key": "sk-test"},
		Extra:       map[string]any{"responses_websockets_v2_enabled": true},
	}

	run := &goneStateRun{svc: svc, pool: pool, conn: conn, dialer: dialer, account: account}
	var mu sync.Mutex
	hooks := &OpenAIWSIngressHooks{
		AfterTurn: func(_ int, result *OpenAIForwardResult, turnErr error) {
			mu.Lock()
			defer mu.Unlock()
			run.results = append(run.results, result)
			run.turnErrs = append(run.turnErrs, turnErr)
			conn.mu.Lock()
			run.closedWhenSettled = append(run.closedWhenSettled, conn.closed)
			conn.mu.Unlock()
		},
	}
	ginCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ginCtx.Request = httptest.NewRequest(http.MethodGet, "/openai/v1/responses", nil)
	ginCtx.Request.Header.Set("User-Agent", "unit-test-agent/1.0")

	client := &goneClientWS{failFrom: clientFailFrom}
	run.proxyErr = svc.ProxyResponsesWebSocketFromClient(context.Background(), ginCtx, client, account, "sk-test", []byte(goneStateRequest), hooks)
	return run
}

func (r *goneStateRun) connClosedAndUnread() (closed bool, unread int) {
	r.conn.mu.Lock()
	defer r.conn.mu.Unlock()
	return r.conn.closed, len(r.conn.events)
}

// requireConnectionDropped checks the upstream connection was taken out of the pool.
func (r *goneStateRun) requireConnectionDropped(t *testing.T) {
	t.Helper()
	closed, _ := r.connClosedAndUnread()
	require.True(t, closed, "the upstream connection is closed")
	ap, ok := r.pool.getAccountPool(r.account.ID)
	require.True(t, ok)
	ap.mu.Lock()
	defer ap.mu.Unlock()
	require.Empty(t, ap.conns, "a connection a turn was cut on never goes back to the pool")
	require.Empty(t, ap.pinnedConns)
}

func (r *goneStateRun) responseBoundToAConnection() bool {
	_, bound := r.svc.getOpenAIWSStateStore().GetResponseConn(goneStateID)
	return bound
}

// The client leaves in the middle of a turn: the upstream connection is closed and
// dropped at once, the terminal event it still holds is never read, nothing is left
// pointing at the dead connection, and the turn carries an estimate.
func TestProxyResponsesWebSocketFromClient_ClientGoneMidTurnDropsConnectionAndEstimates(t *testing.T) {
	run := runGoneState(t, []string{goneStateCreated, goneStateDelta1, goneStateDelta2, goneStateDone}, 3)

	require.NoError(t, run.proxyErr, "the client leaving is a normal end of the session")
	require.Len(t, run.results, 1)
	require.NoError(t, run.turnErrs[0])
	result := run.results[0]
	require.NotNil(t, result)
	require.True(t, result.ClientDisconnect)
	require.Equal(t, goneStateID, result.RequestID)
	require.Empty(t, result.UpstreamTerminalEvent, "the terminal event was never read")
	require.Equal(t, estimateTokensForText("Once upon a time a quick brown fox sat down at a keyboard."), result.Usage.OutputTokens,
		"output is estimated from everything the upstream had produced")
	require.Positive(t, result.Usage.InputTokens, "input is estimated from the request")
	require.Equal(t, incompleteCacheReadTokens(result.Usage.InputTokens), result.Usage.CacheReadInputTokens)
	require.True(t, result.Usage.CacheReadEstimated)

	require.Equal(t, []bool{true}, run.closedWhenSettled, "the upstream is closed before the turn is settled, not after")
	run.requireConnectionDropped(t)
	_, unread := run.connClosedAndUnread()
	require.Equal(t, 1, unread, "the terminal event is left unread: nothing is drained")
	require.False(t, run.responseBoundToAConnection(), "no sticky binding to the dead connection")
}

// The write that fails is the terminal event itself: the usage is real, so it is kept
// as reported, and the connection is still dropped.
func TestProxyResponsesWebSocketFromClient_ClientGoneAtTerminalKeepsReportedUsage(t *testing.T) {
	run := runGoneState(t, []string{goneStateCreated, goneStateDelta1, goneStateDone}, 3)

	require.NoError(t, run.proxyErr)
	require.Len(t, run.results, 1)
	result := run.results[0]
	require.NotNil(t, result)
	require.True(t, result.ClientDisconnect)
	require.Equal(t, "response.completed", result.UpstreamTerminalEvent)
	require.Equal(t, 6, result.Usage.InputTokens)
	require.Equal(t, 4, result.Usage.OutputTokens)
	require.Zero(t, result.Usage.CacheReadInputTokens, "reported usage is never re-split")
	require.False(t, result.Usage.CacheReadEstimated)

	run.requireConnectionDropped(t)
	require.False(t, run.responseBoundToAConnection(), "no sticky binding to the dead connection")
}

// Usage the upstream reported before the client left is never replaced by an estimate;
// only the output side it had not reported is estimated.
func TestProxyResponsesWebSocketFromClient_ClientGoneKeepsEarlierReportedInput(t *testing.T) {
	created := `{"type":"response.created","response":{"id":"resp_gone_state","model":"gpt-5.1","usage":{"input_tokens":7,"output_tokens":0}}}`
	run := runGoneState(t, []string{created, goneStateDelta1, goneStateDelta2, goneStateDone}, 3)

	require.NoError(t, run.proxyErr)
	require.Len(t, run.results, 1)
	result := run.results[0]
	require.NotNil(t, result)
	require.True(t, result.ClientDisconnect)
	require.Equal(t, 7, result.Usage.InputTokens, "the input the upstream reported is kept")
	require.Zero(t, result.Usage.CacheReadInputTokens)
	require.False(t, result.Usage.CacheReadEstimated)
	require.Equal(t, estimateTokensForText("Once upon a time a quick brown fox sat down at a keyboard."), result.Usage.OutputTokens)
}

// A client that leaves after a turn finished is a normal wind down: the finished turn
// keeps its real usage, nothing is estimated, and the healthy connection stays pooled.
func TestProxyResponsesWebSocketFromClient_ClientGoneBetweenTurnsKeepsPooledConnection(t *testing.T) {
	run := runGoneState(t, []string{goneStateCreated, goneStateDelta1, goneStateDone}, 0)

	require.NoError(t, run.proxyErr)
	require.Len(t, run.results, 1)
	result := run.results[0]
	require.NotNil(t, result)
	require.False(t, result.ClientDisconnect)
	require.Equal(t, "response.completed", result.UpstreamTerminalEvent)
	require.Equal(t, 6, result.Usage.InputTokens)
	require.Equal(t, 4, result.Usage.OutputTokens)
	require.False(t, result.Usage.CacheReadEstimated)

	closed, _ := run.connClosedAndUnread()
	require.False(t, closed)
	ap, ok := run.pool.getAccountPool(run.account.ID)
	require.True(t, ok)
	ap.mu.Lock()
	require.Len(t, ap.conns, 1, "a connection that finished its turn cleanly goes back to the pool")
	ap.mu.Unlock()
	require.True(t, run.responseBoundToAConnection(), "a completed turn stays resumable on its connection")
}

// The upstream rejected the request and produced nothing; the client was gone before
// it could be told. That is not billable and not the account's fault: no result, a
// cancellation error, and no retry on another connection.
func TestProxyResponsesWebSocketFromClient_ClientGoneAfterUpstreamErrorIsNotBilled(t *testing.T) {
	upstreamError := `{"type":"error","error":{"type":"invalid_request_error","code":"bad_request","message":"nope"}}`
	run := runGoneState(t, []string{goneStateCreated, upstreamError, goneStateDone}, 2)

	require.ErrorIs(t, run.proxyErr, context.Canceled, "reported as the client leaving, not as an upstream failure")
	require.Len(t, run.results, 1)
	require.Nil(t, run.results[0], "nothing to bill")
	require.ErrorIs(t, run.turnErrs[0], context.Canceled)
	require.Equal(t, 1, run.dialer.DialCount(), "a client that left is not retried")
	run.requireConnectionDropped(t)
}

// The same upstream error after real output was produced: that output is billed.
func TestProxyResponsesWebSocketFromClient_ClientGoneAfterOutputThenErrorIsBilled(t *testing.T) {
	upstreamError := `{"type":"error","error":{"type":"server_error","code":"server_error","message":"boom"}}`
	run := runGoneState(t, []string{goneStateCreated, goneStateDelta1, upstreamError}, 3)

	require.NoError(t, run.proxyErr)
	require.Len(t, run.results, 1)
	result := run.results[0]
	require.NotNil(t, result)
	require.True(t, result.ClientDisconnect)
	require.Equal(t, estimateTokensForText("Once upon a time a quick brown fox "), result.Usage.OutputTokens)
	require.Positive(t, result.Usage.InputTokens)
	run.requireConnectionDropped(t)
}
