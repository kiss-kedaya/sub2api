package routes

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestDepositNetworkStatusRoute(t *testing.T) {
	t.Setenv("CRYPTO_INSTANCE_ENABLED", "false")
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	cfg := &config.Config{Totp: config.TotpConfig{EncryptionKey: strings.Repeat("a", 64), EncryptionKeyConfigured: true}}
	router := gin.New()
	jwt := middleware.JWTAuthMiddleware(func(c *gin.Context) {
		if c.GetHeader("Authorization") != "Bearer test" {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 7})
		c.Next()
	})
	gate := middleware.KeyRotationGateMiddleware(func(c *gin.Context) {
		if c.GetHeader("X-Test-Key-Rotation") == "required" {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		c.Next()
	})
	// The empty service has no RPC pool, address allocator, or shared store.
	RegisterCryptoRoutes(router.Group("/api/v1"), handler.NewCryptoHandler(nil, nil, nil, nil, cfg), handler.NewCryptoDepositHandler(&cryptopay.Service{}), jwt, gate, nil, nil)

	request := func(token, rotation string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/crypto/deposit-network-status", nil)
		r.Header.Set("Authorization", token)
		r.Header.Set("X-Test-Key-Rotation", rotation)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	ok := request("Bearer test", "")
	require.Equal(t, http.StatusOK, ok.Code, ok.Body.String())
	require.Contains(t, ok.Header().Get("Cache-Control"), "no-store")
	var body struct {
		Data struct {
			Networks []map[string]any `json:"networks"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(ok.Body.Bytes(), &body))
	require.NotEmpty(t, body.Data.Networks)
	for _, network := range body.Data.Networks {
		chain, _ := network["chain"].(string)
		require.True(t, cryptopay.IsEVMChain(chain))
		require.Equal(t, chain, network["chain"])
		require.Equal(t, "unknown", network["status"])
		// Without a database or a shared measurement the optional ETA fields
		// stay null and the switch stays at its zero value: an honest blank,
		// never a fabricated number.
		require.Nil(t, network["eta_seconds"])
		require.Nil(t, network["backlog_seconds"])
		require.Nil(t, network["required_confirmations"])
		require.Equal(t, false, network["instant_credit"])
	}
	require.Equal(t, http.StatusUnauthorized, request("", "").Code)
	require.Equal(t, http.StatusForbidden, request("Bearer test", "required").Code)
}
