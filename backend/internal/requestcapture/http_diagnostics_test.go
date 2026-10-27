package requestcapture

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCaptureTransportDiagnosticsPreserveClassAndStage(t *testing.T) {
	for _, phase := range []string{"connect", "tls", "request_write", "response_headers"} {
		t.Run(phase, func(t *testing.T) {
			m, _ := testManager(t)
			target := task(t, m, "account", 60, false)
			s := m.Begin(Meta{UserID: 1})
			require.NotNil(t, s)
			req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://example.test/responses", strings.NewReader("{}"))
			require.NoError(t, err)
			_, finish := s.ObserveHTTPRequest(req, 60)
			tr := httptrace.ContextClientTrace(req.Context())
			tr.GetConn("example.test:443")
			switch phase {
			case "tls":
				tr.TLSHandshakeStart()
			case "request_write":
				tr.GotConn(httptrace.GotConnInfo{})
			case "response_headers":
				tr.WroteRequest(httptrace.WroteRequestInfo{})
			}
			failure := &url.Error{Op: "Post", URL: "https://user:SECRET_PROXY@example.test/?token=SECRET_TOKEN", Err: fmt.Errorf("SECRET_DETAIL: %w", syscall.ECONNRESET)}
			finish(nil, failure)
			s.Finish(502)
			drain(t, m)
			rows, err := m.Records(context.Background(), target.ID, "", false, 10, 0)
			require.NoError(t, err)
			require.Len(t, rows, 1)
			require.Equal(t, "connection_reset", rows[0].Attempts[0].Error)
			require.Equal(t, phase, rows[0].Attempts[0].ErrorStage)
			require.NotContains(t, fmt.Sprintf("%+v", rows[0]), "SECRET_")
		})
	}
}

func TestCaptureClientOutcomeSeparatesFailureAndRecoveredRetry(t *testing.T) {
	for _, outcome := range []string{"failed", "completed"} {
		t.Run(outcome, func(t *testing.T) {
			m, _ := testManager(t)
			target := task(t, m, "account", 60, false)
			s := m.Begin(Meta{UserID: 1})
			require.NotNil(t, s)
			n := s.BeginAttempt(60)
			s.AttemptResponse(n, 0, nil, errors.New("previous failure"))
			st := s.NewStream("client_response", 0, 0, "text/event-stream", nil)
			payload := completedCaptureEvent
			if outcome == "failed" {
				payload = "data: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"code\":\"basispoints_stream_incomplete\"}}}\n\n"
			}
			_, err := st.Write([]byte(payload))
			require.NoError(t, err)
			require.NoError(t, st.Close())
			s.Finish(200)
			drain(t, m)
			rows, err := m.Records(context.Background(), target.ID, "", false, 10, 0)
			require.NoError(t, err)
			require.Len(t, rows, 1)
			require.True(t, rows[0].IsError)
			require.Equal(t, 200, rows[0].Status)
			require.Equal(t, outcome, rows[0].ClientOutcome)
			if outcome == "failed" {
				require.Equal(t, "basispoints_stream_incomplete", rows[0].ErrorCode)
			}
		})
	}
}
