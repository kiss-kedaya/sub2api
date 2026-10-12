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

// 上游 5xx 状态码原样透出，不再被折叠成 502。
func TestOpenAIFailoverExhausted_PreservesUpstream5xxStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, code := range []int{
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

		require.Equal(t, code, rec.Code, "上游状态码必须原样透出")
	}
}
