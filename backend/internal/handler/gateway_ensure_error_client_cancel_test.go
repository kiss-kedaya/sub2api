package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 客户端已断开（ctx 取消）时不再补写上游 502，按 499 归因，避免把链路取消
// 记成 provider 侧错误。
func TestGatewayEnsureForwardErrorResponse_SkipsCanceledClient(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	ctx, cancel := context.WithCancel(context.Background())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil).WithContext(ctx)
	cancel()

	h := &GatewayHandler{}
	require.False(t, h.ensureForwardErrorResponse(c, true))
	require.Equal(t, statusClientClosedRequest, c.Writer.Status())
	require.Empty(t, recorder.Body.String())
}
