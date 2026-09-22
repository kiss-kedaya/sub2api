//go:build unit

package admin

import (
	"bytes"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAccountTypeBindingAllowsCloudflare(t *testing.T) {
	gin.SetMode(gin.TestMode)

	createBody := `{"name":"cf","platform":"openai","type":"cloudflare","credentials":{"api_key":"token","account_id":"df9b7a01eff429b0ecaeeea0366cde12"}}`
	createCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	createCtx.Request = httptest.NewRequest("POST", "/", bytes.NewBufferString(createBody))
	createCtx.Request.Header.Set("Content-Type", "application/json")
	var createReq CreateAccountRequest
	require.NoError(t, createCtx.ShouldBindJSON(&createReq))
	require.Equal(t, "cloudflare", createReq.Type)

	updateCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	updateCtx.Request = httptest.NewRequest("PUT", "/", bytes.NewBufferString(`{"type":"cloudflare"}`))
	updateCtx.Request.Header.Set("Content-Type", "application/json")
	var updateReq UpdateAccountRequest
	require.NoError(t, updateCtx.ShouldBindJSON(&updateReq))
	require.Equal(t, "cloudflare", updateReq.Type)
}
