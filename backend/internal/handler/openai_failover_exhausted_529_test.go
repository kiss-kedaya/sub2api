package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// 只有上游实际返回 529 时，客户端才收到 529。
func TestOpenAIFailoverExhausted_KeepsUpstream529(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	h := &OpenAIGatewayHandler{}
	h.handleFailoverExhausted(c, &service.UpstreamFailoverError{
		StatusCode:   529,
		ResponseBody: []byte(`{"error":{"type":"overloaded_error","message":"Overloaded"}}`),
	}, false)

	require.Equal(t, 529, rec.Code)
	require.Equal(t, "overloaded_error", gjson.GetBytes(rec.Body.Bytes(), "error.type").String())
}

// 换号耗尽不能把上游 5xx 改写成 529，也不能吞掉上游错误消息。
func TestOpenAIFailoverExhausted_PreservesServerFailureStatusAndMessage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, code := range []int{
		http.StatusBadGateway,
		http.StatusInternalServerError,
		http.StatusGatewayTimeout,
		520,
	} {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

		h := &OpenAIGatewayHandler{}
		h.handleFailoverExhausted(c, &service.UpstreamFailoverError{
			StatusCode:   code,
			ResponseBody: []byte(`{"error":{"message":"upstream says boom"}}`),
		}, false)

		require.Equal(t, code, rec.Code, "上游状态码必须原样返回")
		require.Equal(t, "upstream says boom", gjson.GetBytes(rec.Body.Bytes(), "error.message").String())
	}
}

func TestOpenAIFailoverExhausted_DoesNotRewrite500WithOverloadedBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	h := &OpenAIGatewayHandler{}
	h.handleFailoverExhausted(c, &service.UpstreamFailoverError{
		StatusCode:   http.StatusInternalServerError,
		ResponseBody: []byte(`{"error":{"type":"overloaded_error","message":"Overloaded"}}`),
	}, false)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.Equal(t, "overloaded_error", gjson.GetBytes(rec.Body.Bytes(), "error.type").String())
	require.Equal(t, "Overloaded", gjson.GetBytes(rec.Body.Bytes(), "error.message").String())
}

// 换号耗尽时，确定性 4xx 不被归一：状态码与错误类型原样保留。
func TestOpenAIFailoverExhausted_KeepsDeterministic4xx(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	h := &OpenAIGatewayHandler{}
	h.handleFailoverExhausted(c, &service.UpstreamFailoverError{
		StatusCode:   http.StatusTooManyRequests,
		ResponseBody: []byte(`{"error":{"type":"rate_limit_error","message":"rate limited"}}`),
	}, false)

	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	require.Equal(t, "rate_limit_error", gjson.GetBytes(rec.Body.Bytes(), "error.type").String())
}
