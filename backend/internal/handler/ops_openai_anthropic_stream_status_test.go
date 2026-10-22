package handler

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// OpenAI-compatible groups have a separate Messages error emitter. Exercise
// that emitter through Ops middleware after a heartbeat has committed HTTP 200.
func TestOpsOpenAIAnthropicStreamStatus_FailoverPreservesConfirmedStatus(t *testing.T) {
	for _, status := range []int{http.StatusServiceUnavailable, 529, http.StatusGatewayTimeout} {
		for _, errType := range []string{"upstream_error", "server_error", "api_error"} {
			t.Run(fmt.Sprintf("%d/%s", status, errType), func(t *testing.T) {
				entry := captureOpsStreamStatusFidelity(t, "/v1/messages", func(c *gin.Context) {
					(&OpenAIGatewayHandler{}).handleAnthropicFailoverExhausted(c, &service.UpstreamFailoverError{
						StatusCode: status,
						ResponseBody: []byte(fmt.Sprintf(
							`{"error":{"type":%q,"message":%q}}`, errType,
							service.GrokCredentialUnavailableClientMessage,
						)),
					}, true)
				})
				require.Equal(t, status, entry.StatusCode, "the Messages compatibility emitter must preserve its confirmed status")
				require.NotNil(t, entry.UpstreamStatusCode)
				require.Equal(t, status, *entry.UpstreamStatusCode)
				require.Equal(t, service.GrokCredentialUnavailableClientMessage, entry.ErrorMessage)
				assertOpsStreamStatusFidelityAttempts(t, entry)
			})
		}
	}
}

func TestOpsOpenAIAnthropicStreamStatus_ExplicitTerminalStatusWins(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, 529, http.StatusGatewayTimeout} {
		t.Run(fmt.Sprintf("%d", status), func(t *testing.T) {
			entry := captureOpsStreamStatusFidelity(t, "/v1/messages", func(c *gin.Context) {
				(&OpenAIGatewayHandler{}).handleAnthropicFailoverExhausted(c, &service.UpstreamFailoverError{
					StatusCode: http.StatusServiceUnavailable,
					ResponseBody: []byte(fmt.Sprintf(
						`{"error":{"type":"upstream_error","message":%q}}`,
						service.GrokCredentialUnavailableClientMessage,
					)),
				}, true)
				// A later explicit terminal status must outrank the emitter's 503
				// marker even when its type and message otherwise match exactly.
				_, err := fmt.Fprintf(c.Writer,
					"event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"upstream_error\",\"message\":%q,\"status_code\":%d}}\n\n",
					service.GrokCredentialUnavailableClientMessage, status,
				)
				require.NoError(t, err)
				c.Writer.Flush()
			})
			require.Equal(t, status, entry.StatusCode, "an explicit SSE terminal status must outrank the earlier 503 marker")
			require.NotNil(t, entry.UpstreamStatusCode)
			require.Equal(t, status, *entry.UpstreamStatusCode)
			assertOpsStreamStatusFidelityAttempts(t, entry)
		})
	}
}

// Stable custom codes must remain attached to the marker as well as the wire
// frame. Otherwise the logger cannot safely match the confirmed status back to
// either a generic SSE error or a synthetic Responses terminal event.
func TestOpsOpenAIAnthropicStreamStatus_CustomCodePreservesConfirmedStatusWithoutChangingSLAFlag(t *testing.T) {
	for _, gateway := range []string{"openai", "compat"} {
		for _, protocol := range []string{"sse", "responses"} {
			for _, status := range []int{http.StatusServiceUnavailable, 529} {
				t.Run(fmt.Sprintf("%s/%s/%d", gateway, protocol, status), func(t *testing.T) {
					path := "/v1/chat/completions"
					if protocol == "responses" {
						path = "/v1/responses"
					} else if gateway == "compat" {
						path = "/v1/messages"
					}
					code := fmt.Sprintf("provider_capacity_%d", status)
					entry := captureOpsStreamStatusFidelity(t, path, func(c *gin.Context) {
						if gateway == "openai" {
							(&OpenAIGatewayHandler{}).handleStreamingAwareErrorWithCode(
								c, status, "upstream_error", code, service.GrokCredentialUnavailableClientMessage, true, false,
							)
						} else {
							(&GatewayHandler{}).handleStreamingAwareErrorWithCode(
								c, status, "upstream_error", code, service.GrokCredentialUnavailableClientMessage, true,
							)
						}
						marker, ok := service.GetOpsStreamError(c)
						require.True(t, ok)
						require.Equal(t, status, marker.IntendedStatus)
						require.Equal(t, "upstream_error", marker.ErrType)
						require.Equal(t, code, marker.Code, "the marker must retain the same custom code as the emitted terminal event")
						require.False(t, marker.CountTowardsSLA, "keeping a custom code must not opt ordinary stream-error markers into SLA accounting")
						require.False(t, marker.RequestScoped)
						require.False(t, marker.NonStream)
					})
					require.Equal(t, status, entry.StatusCode)
					require.NotNil(t, entry.UpstreamStatusCode)
					require.Equal(t, status, *entry.UpstreamStatusCode)
					require.Equal(t, service.GrokCredentialUnavailableClientMessage, entry.ErrorMessage)
					assertOpsStreamStatusFidelityAttempts(t, entry)
				})
			}
		}
	}
}
