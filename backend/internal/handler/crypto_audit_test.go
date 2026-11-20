package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/wallet"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func cryptoAuditTestContext(t *testing.T) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/crypto/wallet/export-mnemonics", nil)
	request.Header.Set("User-Agent", "unit-agent/1.0")
	request.RemoteAddr = "203.0.113.7:44321"
	request = request.WithContext(context.WithValue(request.Context(), ctxkey.RequestID, "req-unit-1"))
	c.Request = request
	return c
}

func TestCryptoAuditContextCarriesOperatorAndRequestMetadata(t *testing.T) {
	c := cryptoAuditTestContext(t)
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 77})
	c.Set(middleware.ContextKeyAuthEmail, "ops-admin@example.com")
	c.Set(cryptoAuthMethodContextKey, "jwt")

	actor := wallet.AuditActorFromContext(cryptoAuditContext(c))
	require.NotNil(t, actor.UserID)
	require.Equal(t, int64(77), *actor.UserID)
	require.Equal(t, "ops-admin@example.com", actor.Email)
	require.Equal(t, "203.0.113.7", actor.ClientIP)
	require.Equal(t, "unit-agent/1.0", actor.UserAgent)
	require.Equal(t, "req-unit-1", actor.RequestID)
	require.Equal(t, "jwt", actor.AuthMethod)
}

// A request whose identity cannot be resolved must still produce a usable
// context: the wallet writes NULLs and the export is never blocked by missing
// audit metadata.
func TestCryptoAuditContextWithoutIdentityIsEmpty(t *testing.T) {
	c := cryptoAuditTestContext(t)

	ctx := cryptoAuditContext(c)
	require.NotNil(t, ctx)
	actor := wallet.AuditActorFromContext(ctx)
	require.Nil(t, actor.UserID)
	require.Empty(t, actor.Email)
	require.Empty(t, actor.AuthMethod)
}
