package requestcapture

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/util/transportdiag"
	"github.com/stretchr/testify/require"
)

func TestRouteHandshakeFallbackPreservesBothAttempts(t *testing.T) {
	m, _ := testManager(t)
	target := task(t, m, "account", 60, false)
	s := m.Begin(Meta{UserID: 1, Protocol: "http"})
	s.ClientRequest([]byte(`{"stream":true}`), "application/json", nil)
	ws := transportdiag.Route{Downstream: "http", Upstream: "websocket", Mode: "ctx_pool", Reason: "oauth_http_sse_acceleration", Endpoint: "responses"}
	n := s.BeginHandshake(60, ws)
	s.AttemptResponse(n, 426, nil, errors.New("SECRET_HANDSHAKE_DETAIL"))
	httpRoute := transportdiag.Route{Downstream: "http", Mode: "native", FallbackReason: "oauth_ws_sse_handshake_fallback"}
	ctx := transportdiag.WithRoute(context.Background(), httpRoute)
	req, err := http.NewRequestWithContext(ctx, "POST", "https://example.test/v1/responses?token=SECRET_QUERY", strings.NewReader("{}"))
	require.NoError(t, err)
	n, finish := s.ObserveHTTPRequest(req, 60)
	require.Equal(t, 2, n)
	_, err = io.Copy(io.Discard, req.Body)
	require.NoError(t, err)
	resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.created\"}\n\ndata: {\"type\":\"response.completed\"}\n\n"))}
	finish(resp, nil)
	_, err = io.Copy(io.Discard, resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	s.Finish(200)
	drain(t, m)
	rows, err := m.Records(context.Background(), target.ID, "", false, 10, 0)
	require.NoError(t, err)
	require.Len(t, rows, 1) // Recovered upstream failures are retained by existing policy.
	a := rows[0].Attempts
	require.Len(t, a, 2)
	require.Equal(t, ws, a[0].Route)
	require.Equal(t, "websocket_handshake", a[0].Phase)
	require.Equal(t, "websocket_handshake", a[0].ErrorStage)
	require.NotNil(t, a[0].HandshakeMS)
	require.Nil(t, a[0].FirstEventMS)
	require.Equal(t, "http", a[1].Route.Upstream)
	require.Equal(t, "sse", a[1].Route.ResponseFormat)
	require.Equal(t, "responses", a[1].Route.Endpoint)
	require.Equal(t, httpRoute.FallbackReason, a[1].Route.FallbackReason)
	require.NotNil(t, a[1].FirstEventMS)
	raw, err := json.Marshal(a)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "SECRET")
}

func TestRouteWebSocketTurnsAndAccountsStayIndependent(t *testing.T) {
	m, _ := testManager(t)
	target := task(t, m, "user", 1, false)
	s := m.Begin(Meta{UserID: 1, Protocol: "websocket"})
	for turn, mode := range []string{"passthrough", "ctx_pool"} {
		s.ClientFrame([]byte(`{"type":"response.create"}`))
		route := transportdiag.Route{Downstream: "websocket", Mode: mode, Endpoint: "responses"}
		n := s.WSRequestWithRoute(int64(turn+1), []byte(`{"type":"response.create"}`), route)
		require.Equal(t, 1, n)
		s.AttemptResponse(n, 101, nil, nil)
		s.Frame("upstream_response", n, []byte(`{"type":"response.created"}`))
		s.Frame("upstream_response", n, []byte(`{"type":"response.failed","response":{"status":"failed"}}`))
		s.Frame("client_response", 0, []byte(`{"type":"response.failed","response":{"status":"failed"}}`))
	}
	s.Finish(101)
	drain(t, m)
	rows, err := m.Records(context.Background(), target.ID, "", false, 10, 0)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	for _, r := range rows {
		require.Len(t, r.Attempts, 1)
		a := r.Attempts[0]
		require.Equal(t, int64(r.Turn), a.AccountID)
		require.Equal(t, []string{"passthrough", "ctx_pool"}[r.Turn-1], a.Route.Mode)
		require.Equal(t, "websocket", a.Route.Upstream)
		require.Empty(t, a.Route.FallbackReason)
		require.NotNil(t, a.FirstEventMS)
	}
}

func TestRouteFirstEventRequiresCompleteValidEvent(t *testing.T) {
	m, _ := testManager(t)
	task(t, m, "user", 1, false)
	s := m.Begin(Meta{UserID: 1, Protocol: "http"})
	n := s.BeginRoutedAttempt(1, transportdiag.Route{Upstream: "http"})
	st := s.NewStream("upstream_response", n, 0, "text/event-stream", nil)
	for _, data := range []string{": heartbeat\n\n", "data: invalid\n\n", "data: {\"type\":\"response.created\"}"} {
		_, err := st.Write([]byte(data))
		require.NoError(t, err)
		s.mu.Lock()
		first := s.attempts[0].FirstEventMS
		s.mu.Unlock()
		require.Nil(t, first)
	}
	_, err := st.Write([]byte("\n\n"))
	require.NoError(t, err)
	s.mu.Lock()
	first := s.attempts[0].FirstEventMS
	s.mu.Unlock()
	require.NotNil(t, first)
	_, err = st.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n"))
	require.NoError(t, err)
	s.mu.Lock()
	require.Equal(t, first, s.attempts[0].FirstEventMS)
	s.mu.Unlock()
	require.NoError(t, st.Close())
	s.Finish(502)
	drain(t, m)
}
