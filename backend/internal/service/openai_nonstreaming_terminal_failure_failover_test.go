package service

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

func newNonStreamingFailoverContext(t *testing.T) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	return c, rec
}

func newNonStreamingFailoverService() *OpenAIGatewayService {
	return &OpenAIGatewayService{cfg: &config.Config{}}
}

func newNonStreamingFailoverAccount() *Account {
	return &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Name: "pool-account", Credentials: map[string]any{"pool_mode": true}}
}

func newNonStreamingSSEResponse() *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{
		"Content-Type": []string{"text/event-stream"}, "X-Request-Id": []string{"rid-nonstreaming-failed"},
	}}
}

func nonStreamingTerminalSSE(eventType, data string) []byte {
	return []byte(strings.Join([]string{"event: " + eventType, "data: " + data, "", "data: [DONE]"}, "\n"))
}

func TestNonStreamingSSEToJSON_CapacityFailedEventFailsOver(t *testing.T) {
	c, rec := newNonStreamingFailoverContext(t)
	body := nonStreamingTerminalSSE("response.failed", `{"type":"response.failed","error":{"message":"Selected model is at capacity. Please try a different model.","type":"invalid_request_error"}}`)
	result, err := newNonStreamingFailoverService().handleSSEToJSON(newNonStreamingSSEResponse(), c, newNonStreamingFailoverAccount(), body, "model", "model")
	var failoverErr *UpstreamFailoverError
	require.Nil(t, result)
	require.ErrorAs(t, err, &failoverErr)
	require.True(t, failoverErr.RetryableOnSameAccount)
	require.Contains(t, string(failoverErr.ResponseBody), "Selected model is at capacity")
	require.False(t, c.Writer.Written())
	require.Empty(t, rec.Body.String())
}

func TestNonStreamingSSEToJSON_NonRetryableFailedEventStillWritesProtocolError(t *testing.T) {
	cases := []struct{ name, data, message string }{
		{"invalid_request", `{"type":"response.failed","error":{"type":"invalid_request_error","code":"invalid_request","message":"unknown parameter foo"}}`, "unknown parameter foo"},
		{"context_window", `{"type":"response.failed","response":{"error":{"code":"upstream_error","message":"input exceeds the context window"}}}`, "input exceeds the context window"},
		{"content_policy", `{"type":"response.failed","error":{"type":"content_policy_violation","message":"blocked by our content policy"}}`, "blocked by our content policy"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, rec := newNonStreamingFailoverContext(t)
			result, err := newNonStreamingFailoverService().handleSSEToJSON(newNonStreamingSSEResponse(), c, newNonStreamingFailoverAccount(), nonStreamingTerminalSSE("response.failed", tc.data), "model", "model")
			var failoverErr *UpstreamFailoverError
			require.Nil(t, result)
			require.Error(t, err)
			require.False(t, errors.As(err, &failoverErr))
			require.Equal(t, http.StatusBadGateway, rec.Code)
			require.Contains(t, rec.Body.String(), tc.message)
		})
	}
}

func TestNonStreamingSSEToJSON_BareErrorUsesConservativeClassifier(t *testing.T) {
	c, rec := newNonStreamingFailoverContext(t)
	nonTransient := `{"type":"error","error":{"message":"upstream rejected request"}}`
	result, err := newNonStreamingFailoverService().handleSSEToJSON(newNonStreamingSSEResponse(), c, newNonStreamingFailoverAccount(), nonStreamingTerminalSSE("error", nonTransient), "model", "model")
	var failoverErr *UpstreamFailoverError
	require.Nil(t, result)
	require.Error(t, err)
	require.False(t, errors.As(err, &failoverErr))
	require.Equal(t, http.StatusBadGateway, rec.Code)

	c, _ = newNonStreamingFailoverContext(t)
	transient := `{"type":"error","error":{"message":"Temporary upstream failure, please retry"}}`
	_, err = newNonStreamingFailoverService().handleSSEToJSON(newNonStreamingSSEResponse(), c, newNonStreamingFailoverAccount(), nonStreamingTerminalSSE("error", transient), "model", "model")
	require.ErrorAs(t, err, &failoverErr)
}

func TestNonStreamingPassthroughSSEToJSON_CapacityFailedEventFailsOver(t *testing.T) {
	c, _ := newNonStreamingFailoverContext(t)
	body := nonStreamingTerminalSSE("response.failed", `{"type":"response.failed","error":{"message":"Selected model is at capacity. Please try a different model.","type":"invalid_request_error"}}`)
	result, err := newNonStreamingFailoverService().handlePassthroughSSEToJSON(newNonStreamingSSEResponse(), c, newNonStreamingFailoverAccount(), body, "model", "model")
	var failoverErr *UpstreamFailoverError
	require.Nil(t, result)
	require.ErrorAs(t, err, &failoverErr)
	require.Contains(t, string(failoverErr.ResponseBody), "Selected model is at capacity")
	require.False(t, c.Writer.Written())
}

func TestNonStreamingSSEToJSON_CommittedResponseKeepsProtocolError(t *testing.T) {
	c, rec := newNonStreamingFailoverContext(t)
	MarkResponseCommitted(c)
	body := nonStreamingTerminalSSE("response.failed", `{"type":"response.failed","error":{"message":"Selected model is at capacity. Please try a different model.","type":"invalid_request_error"}}`)
	result, err := newNonStreamingFailoverService().handleSSEToJSON(newNonStreamingSSEResponse(), c, newNonStreamingFailoverAccount(), body, "model", "model")
	var failoverErr *UpstreamFailoverError
	require.Nil(t, result)
	require.Error(t, err)
	require.False(t, errors.As(err, &failoverErr))
	require.Equal(t, http.StatusBadGateway, rec.Code)
}

func TestNonStreamingTerminalFailureFailover_NilAccountProposesNothing(t *testing.T) {
	c, _ := newNonStreamingFailoverContext(t)
	payload := []byte(`{"type":"response.failed","error":{"message":"Selected model is at capacity"}}`)
	require.Nil(t, newNonStreamingFailoverService().nonStreamingTerminalFailureFailover(c, newNonStreamingSSEResponse(), nil, false, "response.failed", payload, "Selected model is at capacity"))
}
