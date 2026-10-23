package routes

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/enttest"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

const callbackTestSigningSecret = "callback-test-signing-secret"

func newPaymentCallbackSecurityRouter(t *testing.T, rpm int) (*gin.Engine, *dbent.Client) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := sql.Open("sqlite", "file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	_, err = db.Exec("PRAGMA foreign_keys = ON")
	require.NoError(t, err)
	client := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(entsql.OpenDB(dialect.SQLite, db))))
	t.Cleanup(func() { _ = client.Close(); _ = db.Close() })
	key := []byte(strings.Repeat("k", 32))
	providerConfig, err := json.Marshal(map[string]string{
		"pid": "test-pid", "secretKey": callbackTestSigningSecret,
		"apiBase": "https://payments.example.com", "notifyUrl": "https://merchant.example.com/callback",
		"returnUrl": "https://merchant.example.com/purchase", "token": "USDT", "network": "bsc", "currency": "USD",
	})
	require.NoError(t, err)
	//nolint:staticcheck // Verify callbacks against historical encrypted provider configurations.
	ciphertext, err := payment.Encrypt(string(providerConfig), key)
	require.NoError(t, err)
	_, err = client.PaymentProviderInstance.Create().SetProviderKey(payment.TypeEpusdt).
		SetName("test-epusdt").SetConfig(ciphertext).SetSupportedTypes(payment.TypeEpusdt).SetEnabled(true).Save(context.Background())
	require.NoError(t, err)
	registry := payment.NewRegistry()
	payments := service.NewPaymentService(client, registry, payment.NewDefaultLoadBalancer(client, key), nil, nil, nil, nil, nil, nil)
	webhook := handler.NewPaymentWebhookHandler(payments, registry)
	settings := service.NewSettingService(&cryptoRouteSettingsRepo{values: map[string]string{
		"panel_rate_limit_settings": fmt.Sprintf(`{"enabled":true,"public_ip_rpm":%d}`, rpm),
	}}, &config.Config{})
	server := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	router := gin.New()
	require.NoError(t, router.SetTrustedProxies(nil))
	noop := func(c *gin.Context) { c.Next() }
	RegisterPaymentRoutes(router.Group("/api/v1"), &handler.PaymentHandler{}, webhook, &adminhandler.PaymentHandler{},
		middleware.JWTAuthMiddleware(noop), middleware.AdminAuthMiddleware(noop), middleware.AuditLogMiddleware(noop),
		settings, middleware.NewPanelRateLimiter(rdb, settings))
	return router, client
}

func paymentCallbackBody(t *testing.T, valid bool) string {
	t.Helper()
	// Real EPUSDT signing uses HMAC-SHA256 over sorted nonempty fields.
	// Use an unknown order to exercise signed-callback ACK
	// without creating or mutating any user/payment balances.
	canonical := "amount=1&order_id=test-unknown-order&pid=test-pid&status=2&trade_id=test-trade"
	mac := hmac.New(sha256.New, []byte(callbackTestSigningSecret))
	_, err := mac.Write([]byte(canonical))
	require.NoError(t, err)
	signature := hex.EncodeToString(mac.Sum(nil))
	if !valid {
		signature = strings.Repeat("0", 64)
	}
	body, err := json.Marshal(map[string]any{
		"amount": 1, "order_id": "test-unknown-order", "pid": "test-pid", "status": 2, "trade_id": "test-trade", "signature": signature,
	})
	require.NoError(t, err)
	return string(body)
}

func performPaymentCallback(router *gin.Engine, path, body, ip string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = ip + ":12345"
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func TestPaymentCallbackAliasSharesPublicIPRateLimitWithCanonicalWebhook(t *testing.T) {
	router, client := newPaymentCallbackSecurityRouter(t, 2)
	body := paymentCallbackBody(t, false)
	const ip = "198.51.100.21"
	first := performPaymentCallback(router, "/api/v1/payment/webhook/epusdt", body, ip)
	require.Equal(t, http.StatusBadRequest, first.Code)
	second := performPaymentCallback(router, "/api/v1/payments/callback", body, ip)
	require.Equal(t, http.StatusBadRequest, second.Code)
	for _, path := range []string{"/api/v1/payments/callback", "/api/v1/payment/webhook/epusdt"} {
		limited := performPaymentCallback(router, path, body, ip)
		require.Equal(t, http.StatusTooManyRequests, limited.Code, path)
		require.Contains(t, limited.Body.String(), "RATE_LIMITED")
		require.NotEmpty(t, limited.Header().Get("Retry-After"))
	}
	otherIP := performPaymentCallback(router, "/api/v1/payments/callback", body, "198.51.100.22")
	require.Equal(t, http.StatusBadRequest, otherIP.Code)
	count, err := client.PaymentOrder.Query().Count(context.Background())
	require.NoError(t, err)
	require.Zero(t, count)
}

func TestPaymentCallbackAliasStillVerifiesRealProviderSignature(t *testing.T) {
	for _, path := range []string{"/api/v1/payments/callback", "/api/v1/payment/webhook/epusdt"} {
		t.Run(strings.ReplaceAll(path, "/", "_"), func(t *testing.T) {
			router, client := newPaymentCallbackSecurityRouter(t, 10)
			bad := performPaymentCallback(router, path, paymentCallbackBody(t, false), "198.51.100.30")
			require.Equal(t, http.StatusBadRequest, bad.Code)
			require.Equal(t, "verify failed", bad.Body.String())
			good := performPaymentCallback(router, path, paymentCallbackBody(t, true), "198.51.100.30")
			require.Equal(t, http.StatusOK, good.Code)
			require.Equal(t, "success", good.Body.String())
			count, err := client.PaymentOrder.Query().Count(context.Background())
			require.NoError(t, err)
			require.Zero(t, count)
		})
	}
}
