package requesttiming

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/util/transportdiag"
	"github.com/stretchr/testify/require"
)

func TestPhysicalHTTPAttemptsSnapshotActualRoute(t *testing.T) {
	c := New(time.Now(), 0)
	ctx := With(context.Background(), c)
	for _, mode := range []string{"native", "http_bridge"} {
		route := transportdiag.Route{Downstream: "websocket", Mode: mode}
		req, err := http.NewRequestWithContext(transportdiag.WithRoute(ctx, route), "POST", "https://example.test/v1/chat/completions?token=SECRET", nil)
		require.NoError(t, err)
		req, logical := StartAttempt(req, 1, 0)
		_, physical := StartTransport(req)
		resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(""))}
		physical.Response(resp, nil)
		require.NoError(t, resp.Body.Close())
		logical.Response(nil, nil)
	}
	c.Finish(200, false)
	c.WhenFinished(func(s Snapshot) {
		require.Len(t, s.Attempts, 4)
		for i, a := range s.Attempts {
			require.Equal(t, []string{"native", "http_bridge"}[i/2], a.Route.Mode)
			require.Equal(t, "http", a.Route.Upstream)
			require.Equal(t, "chat_completions", a.Route.Endpoint)
			if a.Kind == "http" {
				require.Equal(t, "sse", a.Route.ResponseFormat)
			}
		}
	})
}

func TestRouteObservationsAreBoundedAndDoNotRetainRawErrors(t *testing.T) {
	c := New(time.Now(), 0)
	ctx := With(context.Background(), c)
	for i := 0; i < 33; i++ {
		finish := StartRoute(ctx, 1, "websocket_handshake", transportdiag.Route{Upstream: "websocket"})
		finish(426, io.ErrUnexpectedEOF)
		finish(200, nil) // Finishing twice cannot overwrite the original outcome.
	}
	c.Finish(502, false)
	c.WhenFinished(func(s Snapshot) {
		require.True(t, s.Truncated)
		require.Len(t, s.Routes, 32)
		for _, r := range s.Routes {
			require.Equal(t, 426, r.Status)
			require.Equal(t, "transport_error", r.Error)
			require.NotNil(t, r.EndMS)
		}
	})
}

func TestConfirmedPluginDispatchDoesNotClaimPhysicalHTTP(t *testing.T) {
	c := New(time.Now(), 0)
	ctx := With(context.Background(), c)
	req, err := http.NewRequestWithContext(ctx, "POST", "https://example.test/responses", nil)
	require.NoError(t, err)
	req, trace := StartAttempt(req, 1, 0)
	SetRoute(req.Context(), transportdiag.Route{Mode: "plugin", Upstream: "plugin", Endpoint: "responses"})
	trace.Response(nil, nil)
	c.Finish(502, false)
	c.WhenFinished(func(s Snapshot) {
		require.Equal(t, "plugin", s.Attempts[0].Route.Upstream)
		require.Equal(t, "plugin", s.Attempts[0].Route.Mode)
	})
}
