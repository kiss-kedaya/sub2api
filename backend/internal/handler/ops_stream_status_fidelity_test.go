package handler

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Exercise the emitted terminal frame and the actual Ops middleware together.
// A handler-only assertion can pass while the logger later guesses another
// status from a generic upstream_error/server_error code.
func TestOpsStreamStatusFidelity_FailoverHandlersPreserveConfirmedStatus(t *testing.T) {
	handlers := []struct {
		name string
		path string
		run  func(*gin.Context, *service.UpstreamFailoverError)
	}{
		{
			name: "openai_responses",
			path: "/v1/responses",
			run: func(c *gin.Context, failure *service.UpstreamFailoverError) {
				(&OpenAIGatewayHandler{}).handleFailoverExhausted(c, failure, true)
			},
		},
		{
			name: "compat_responses",
			path: "/v1/responses",
			run: func(c *gin.Context, failure *service.UpstreamFailoverError) {
				(&GatewayHandler{}).handleResponsesFailoverExhausted(c, failure, true)
			},
		},
		{
			name: "messages",
			path: "/v1/messages",
			run: func(c *gin.Context, failure *service.UpstreamFailoverError) {
				(&GatewayHandler{}).handleFailoverExhausted(c, failure, service.PlatformGrok, true)
			},
		},
	}
	for _, handler := range handlers {
		for _, status := range []int{500, 502, 503, 504, 520, 529} {
			for _, errType := range []string{"upstream_error", "server_error"} {
				t.Run(fmt.Sprintf("%s/%d/%s", handler.name, status, errType), func(t *testing.T) {
					entry := captureOpsStreamStatusFidelity(t, handler.path, func(c *gin.Context) {
						failure := &service.UpstreamFailoverError{
							StatusCode: status,
							ResponseBody: []byte(fmt.Sprintf(
								`{"error":{"type":%q,"message":%q}}`, errType,
								service.GrokCredentialUnavailableClientMessage,
							)),
						}
						handler.run(c, failure)
					})
					require.Equal(t, status, entry.StatusCode, "the emitted terminal frame must retain the handler's confirmed status")
					require.NotNil(t, entry.UpstreamStatusCode)
					require.Equal(t, status, *entry.UpstreamStatusCode)
					require.Equal(t, service.GrokCredentialUnavailableClientMessage, entry.ErrorMessage)
					assertOpsStreamStatusFidelityAttempts(t, entry)
				})
			}
		}
	}
}

func TestOpsStreamStatusFidelity_ExplicitTerminalStatusWins(t *testing.T) {
	for _, status := range []int{400, 429, 500, 503, 529} {
		t.Run(fmt.Sprintf("%d", status), func(t *testing.T) {
			entry := captureOpsStreamStatusFidelity(t, "/v1/responses", func(c *gin.Context) {
				service.MarkOpsStreamError(c, "upstream_error", "terminal failure", http.StatusBadGateway)
				_, err := fmt.Fprintf(c.Writer,
					"event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"upstream_error\",\"message\":\"terminal failure\",\"status_code\":%d}}}\n\n",
					status,
				)
				require.NoError(t, err)
				c.Writer.Flush()
			})
			require.Equal(t, status, entry.StatusCode, "an explicit terminal status outranks a conflicting earlier marker")
			require.NotNil(t, entry.UpstreamStatusCode)
			require.Equal(t, status, *entry.UpstreamStatusCode)
			assertOpsStreamStatusFidelityAttempts(t, entry)
		})
	}
}

func TestOpsStreamStatusFidelity_UnknownTerminalDoesNotInheritAttempts(t *testing.T) {
	entry := captureOpsStreamStatusFidelity(t, "/v1/responses", func(c *gin.Context) {
		_, err := c.Writer.WriteString("event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"unknown_provider_code\",\"message\":\"terminal failure\"}}}\n\n")
		require.NoError(t, err)
		c.Writer.Flush()
	})
	require.Equal(t, http.StatusBadGateway, entry.StatusCode)
	require.NotNil(t, entry.UpstreamStatusCode)
	require.Equal(t, http.StatusBadGateway, *entry.UpstreamStatusCode)
	assertOpsStreamStatusFidelityAttempts(t, entry)
}

func TestOpsStreamStatusFidelity_StaleMarkerDoesNotOverrideTerminal(t *testing.T) {
	for _, test := range []struct {
		name            string
		markerType      string
		markerCode      string
		markerMessage   string
		terminalCode    string
		terminalMessage string
	}{
		{
			name:       "different_message",
			markerType: "upstream_error", markerMessage: "earlier account failure",
			terminalCode: "upstream_error", terminalMessage: "terminal failure",
		},
		{
			name:       "different_code",
			markerType: "upstream_error", markerCode: "earlier_provider_code", markerMessage: "terminal failure",
			terminalCode: "new_provider_code", terminalMessage: "terminal failure",
		},
		{
			name:       "different_error_type",
			markerType: "permission_error", markerMessage: "terminal failure",
			terminalCode: "unknown_provider_code", terminalMessage: "terminal failure",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			entry := captureOpsStreamStatusFidelity(t, "/v1/responses", func(c *gin.Context) {
				service.MarkOpsStreamErrorValue(c, service.OpsStreamError{
					ErrType: test.markerType, Code: test.markerCode, Message: test.markerMessage,
					IntendedStatus: http.StatusServiceUnavailable,
				})
				_, err := fmt.Fprintf(c.Writer,
					"event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":%q,\"message\":%q}}}\n\n",
					test.terminalCode, test.terminalMessage,
				)
				require.NoError(t, err)
				c.Writer.Flush()
			})
			require.Equal(t, http.StatusBadGateway, entry.StatusCode, "a different terminal error must not inherit an older marker")
			require.NotNil(t, entry.UpstreamStatusCode)
			require.Equal(t, http.StatusBadGateway, *entry.UpstreamStatusCode)
			require.Equal(t, test.terminalMessage, entry.ErrorMessage)
			assertOpsStreamStatusFidelityAttempts(t, entry)
		})
	}
}

func TestOpsStreamStatusFidelity_ConflictingTerminalTypeWinsOverMatchingCodeAndMessage(t *testing.T) {
	entry := captureOpsStreamStatusFidelity(t, "/v1/responses", func(c *gin.Context) {
		service.MarkOpsStreamErrorValue(c, service.OpsStreamError{
			ErrType: "upstream_error", Code: "upstream_error", Message: "terminal failure",
			IntendedStatus: http.StatusServiceUnavailable,
		})
		_, err := c.Writer.WriteString("event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"error\":{\"type\":\"permission_error\",\"code\":\"upstream_error\",\"message\":\"terminal failure\"}}}\n\n")
		require.NoError(t, err)
		c.Writer.Flush()
	})
	require.Equal(t, http.StatusForbidden, entry.StatusCode, "a conflicting terminal type must not inherit a marker just because its code and message match")
	require.NotNil(t, entry.UpstreamStatusCode)
	require.Equal(t, http.StatusForbidden, *entry.UpstreamStatusCode)
	require.Equal(t, "terminal failure", entry.ErrorMessage)
	assertOpsStreamStatusFidelityAttempts(t, entry)
}

func captureOpsStreamStatusFidelity(t *testing.T, path string, terminal func(*gin.Context)) *service.OpsInsertErrorLogInput {
	t.Helper()
	setupOpsErrorLogTestQueue(t, 4)
	gin.SetMode(gin.TestMode)
	ops := service.NewOpsService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	router := gin.New()
	router.Use(OpsErrorLoggerMiddleware(ops))
	router.POST(path, func(c *gin.Context) {
		setOpsRequestContext(c, "grok-4.7", true)
		// Earlier HTTP failures must remain intact and must not be mistaken for
		// the confirmed status of the terminal event emitted below.
		c.Set(service.OpsUpstreamErrorsKey, []*service.OpsUpstreamErrorEvent{
			{Platform: service.PlatformGrok, AccountID: 30778, Kind: "failover", UpstreamStatusCode: 503, Message: service.GrokCredentialUnavailableClientMessage},
			{Platform: service.PlatformGrok, AccountID: 30767, Kind: "failover", UpstreamStatusCode: 503, Message: service.GrokCredentialUnavailableClientMessage},
		})
		service.SetOpsUpstreamError(c, http.StatusServiceUnavailable, service.GrokCredentialUnavailableClientMessage, "")
		c.Header("Content-Type", "text/event-stream")
		written, err := c.Writer.WriteString(": ping\n\n")
		require.NoError(t, err)
		recordGatewayStreamHeartbeat(c, written)
		c.Writer.Flush()
		terminal(c)
	})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, path, nil))
	require.Equal(t, http.StatusOK, recorder.Code, "flushed HTTP headers must remain 200")
	require.Equal(t, int64(1), OpsErrorLogQueueLength(), "record the terminal failure exactly once")
	job := <-opsErrorLogQueue
	require.NotNil(t, job.entry)
	return job.entry
}

func assertOpsStreamStatusFidelityAttempts(t *testing.T, entry *service.OpsInsertErrorLogInput) {
	t.Helper()
	require.Nil(t, entry.UpstreamErrors, "raw attempts must be released before async queueing")
	require.NotNil(t, entry.UpstreamErrorsJSON)
	attempts, err := service.ParseOpsUpstreamErrors(*entry.UpstreamErrorsJSON)
	require.NoError(t, err)
	require.Len(t, attempts, 2)
	for _, attempt := range attempts {
		require.NotNil(t, attempt)
		require.Equal(t, http.StatusServiceUnavailable, attempt.UpstreamStatusCode, "terminal attribution must not rewrite attempt evidence")
	}
}
