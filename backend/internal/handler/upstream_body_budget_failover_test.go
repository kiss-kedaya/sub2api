//go:build unit

package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const bodyBudgetUpstreamBody = `{"error":{"message":"Request body buffering capacity is exhausted; retry later","type":"invalid_request_error","code":"model_not_found"}}`

func bodyBudgetFailoverError() *service.UpstreamFailoverError {
	return &service.UpstreamFailoverError{
		StatusCode:      http.StatusServiceUnavailable,
		ResponseBody:    []byte(bodyBudgetUpstreamBody),
		ResponseHeaders: http.Header{"Retry-After": []string{"1"}},
	}
}

func requireBodyBudgetClientFailure(t *testing.T, recorder *httptest.ResponseRecorder) {
	t.Helper()
	// Upstream 503 semantics survive failover exhaustion.
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	// The supplier's explicit retry boundary is relayed instead of dropped.
	require.Equal(t, "1", recorder.Header().Get("Retry-After"))
	require.Contains(t, recorder.Body.String(), service.UpstreamRequestBodyBudgetClientMessage)

	lower := strings.ToLower(recorder.Body.String())
	for _, leak := range []string{
		"buffering",
		"memory budget",
		"gateway_request_body_memory_budget_bytes",
		"model_not_found",
		"invalid_request_error",
	} {
		require.NotContains(t, lower, leak)
	}
}

func TestOpenAIRequestBodyBudgetFailoverExhaustedKeeps503AndRetryAfter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	(&OpenAIGatewayHandler{}).handleFailoverExhausted(c, bodyBudgetFailoverError(), false)

	requireBodyBudgetClientFailure(t, recorder)
}

func TestAnthropicRequestBodyBudgetFailoverExhaustedKeeps503AndRetryAfter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	(&OpenAIGatewayHandler{}).handleAnthropicFailoverExhausted(c, bodyBudgetFailoverError(), false)

	requireBodyBudgetClientFailure(t, recorder)
}

func TestGatewayRequestBodyBudgetFailoverExhaustedKeeps503AndRetryAfter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	(&GatewayHandler{}).handleFailoverExhausted(c, bodyBudgetFailoverError(), service.PlatformAnthropic, false)

	requireBodyBudgetClientFailure(t, recorder)
}

func TestOrdinary503FailoverExhaustedUnaffectedByBodyBudgetFix(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	(&OpenAIGatewayHandler{}).handleFailoverExhausted(c, &service.UpstreamFailoverError{
		StatusCode:   http.StatusServiceUnavailable,
		ResponseBody: []byte(`{"error":{"message":"upstream temporarily unavailable"}}`),
	}, false)

	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.Contains(t, recorder.Body.String(), "upstream temporarily unavailable")
	require.Empty(t, recorder.Header().Get("Retry-After"))
	require.NotContains(t, recorder.Body.String(), service.UpstreamRequestBodyBudgetClientMessage)
}
