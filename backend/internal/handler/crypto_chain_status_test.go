package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func chainStatusContext() (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/crypto/chains/status", nil)
	return c, w
}

func TestAdminChainStatusWithoutAServiceIsUnavailableAndNotCacheable(t *testing.T) {
	c, w := chainStatusContext()
	(&CryptoHandler{}).AdminChainStatus(c)
	require.Equal(t, http.StatusServiceUnavailable, w.Code, w.Body.String())
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	require.Equal(t, "no-cache", w.Header().Get("Pragma"))
}

func TestAdminChainStatusReturnsTheReportEnvelope(t *testing.T) {
	c, w := chainStatusContext()
	(&CryptoHandler{service: &cryptopay.Service{}}).AdminChainStatus(c)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))

	var body struct {
		Data struct {
			GeneratedAt string `json:"generated_at"`
			Summary     map[string]int
			Chains      []any `json:"chains"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	_, err := time.Parse(time.RFC3339, body.Data.GeneratedAt)
	require.NoError(t, err)
	require.NotNil(t, body.Data.Chains, "an empty list, not null")
	require.Empty(t, body.Data.Chains)
}
