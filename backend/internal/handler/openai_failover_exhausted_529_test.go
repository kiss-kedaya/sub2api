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

// failover 耗尽时，overloaded 语义还原出的 529 不能被「5xx 一律折 502」再折回去。
func TestOpenAIFailoverExhausted_Keeps529ForOverloadedBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	h := &OpenAIGatewayHandler{}
	h.handleFailoverExhausted(c, &service.UpstreamFailoverError{
		StatusCode:   http.StatusBadGateway,
		ResponseBody: []byte(`{"error":{"type":"overloaded_error","message":"Overloaded"}}`),
	}, false)

	require.Equal(t, 529, rec.Code)
	require.Equal(t, "overloaded_error", gjson.GetBytes(rec.Body.Bytes(), "error.type").String())
}

// 换号耗尽（整组无可用账号）：服务端失败统一按 529（过载）回传，客户端可据此退避重试。
func TestOpenAIFailoverExhausted_NormalizesServerFailuresTo529(t *testing.T) {
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

		require.Equal(t, 529, rec.Code, "换号耗尽的 5xx 必须归一成 529")
		require.Equal(t, "overloaded_error", gjson.GetBytes(rec.Body.Bytes(), "error.type").String())
	}
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
