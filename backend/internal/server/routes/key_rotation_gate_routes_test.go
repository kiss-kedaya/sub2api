package routes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type keyRotationGateRouteLister struct {
	keys []service.APIKey
}

func (l *keyRotationGateRouteLister) ListActiveKeysByUser(context.Context, int64) ([]service.APIKey, error) {
	return l.keys, nil
}

type keyRotationGateAuditRepo struct {
	service.AuditLogRepository
}

func (r *keyRotationGateAuditRepo) BatchInsert(_ context.Context, logs []*service.AuditLog) (int64, error) {
	return int64(len(logs)), nil
}

func (r *keyRotationGateAuditRepo) Insert(context.Context, *service.AuditLog) error { return nil }

// newKeyRotationGateRouteEngine builds the real route registrations for the
// redeem / payment / crypto surfaces with the key-rotation gate wired in.
func newKeyRotationGateRouteEngine(t *testing.T) (*gin.Engine, *service.KeyRotationGateService) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	settings := service.NewSettingService(&cryptoRouteSettingsRepo{values: map[string]string{}}, nil)
	cutoff := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	cfg := &config.Config{
		Security: config.SecurityConfig{
			KeyRotationGateEnabled: true,
			KeyRotationCutoff:      cutoff.Format(time.RFC3339),
		},
		Totp: config.TotpConfig{
			EncryptionKey:           strings.Repeat("a", 64),
			EncryptionKeyConfigured: true,
		},
	}
	gateService := service.NewKeyRotationGateService(&keyRotationGateRouteLister{keys: []service.APIKey{
		{ID: 1, CreatedAt: cutoff.Add(-24 * time.Hour)},
	}}, cfg, nil)
	auditService := service.NewAuditLogService(&keyRotationGateAuditRepo{}, nil)
	gateMiddleware := middleware.NewKeyRotationGateMiddleware(gateService, auditService)

	jwt := middleware.JWTAuthMiddleware(func(c *gin.Context) {
		c.Set("auth_method", service.AuditAuthMethodJWT)
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 91})
		c.Next()
	})
	noopAudit := middleware.AuditLogMiddleware(func(c *gin.Context) { c.Next() })

	h := &handler.Handlers{
		Redeem:      &handler.RedeemHandler{},
		KeyRotation: handler.NewKeyRotationHandler(gateService, nil, auditService),
	}
	cryptoHandler := handler.NewCryptoHandler(nil, settings, nil, nil, cfg)
	depositHandler := handler.NewCryptoDepositHandler(nil)

	router := gin.New()
	router.Use(gin.Recovery())
	v1 := router.Group("/api/v1")
	RegisterUserRoutes(v1, h, jwt, noopAudit, gateMiddleware, settings, nil)
	RegisterPaymentRoutes(v1, &handler.PaymentHandler{}, &handler.PaymentWebhookHandler{}, &adminhandler.PaymentHandler{}, jwt, nil, noopAudit, nil, gateMiddleware, settings, nil)
	RegisterCryptoRoutes(v1, cryptoHandler, depositHandler, jwt, gateMiddleware, settings, nil)
	return router, gateService
}

func keyRotationGateBody(t *testing.T, recorder *httptest.ResponseRecorder) response.Response {
	t.Helper()
	var body response.Response
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	return body
}

func TestKeyRotationGateBlocksEveryRechargeSurface(t *testing.T) {
	router, _ := newKeyRotationGateRouteEngine(t)

	for _, tc := range []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/v1/redeem"},
		{http.MethodGet, "/api/v1/redeem/history"},
		{http.MethodPost, "/api/v1/payment/orders"},
		{http.MethodPost, "/api/v1/payment/orders/verify"},
		{http.MethodGet, "/api/v1/payment/orders/my"},
		{http.MethodGet, "/api/v1/payment/orders/1"},
		{http.MethodPost, "/api/v1/payment/orders/1/cancel"},
		{http.MethodPost, "/api/v1/payment/orders/1/refund-request"},
		{http.MethodGet, "/api/v1/crypto/deposit-addresses"},
		{http.MethodGet, "/api/v1/crypto/deposit-info"},
		{http.MethodGet, "/api/v1/crypto/deposit-history"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(tc.method, tc.path, nil))

			require.Equal(t, http.StatusForbidden, recorder.Code, recorder.Body.String())
			body := keyRotationGateBody(t, recorder)
			require.Equal(t, service.KeyRotationRequiredCode, body.Reason)
			require.Equal(t, service.KeyRotationRequiredMessage, body.Message)
			require.Equal(t, service.KeyRotationRequiredCode, body.Metadata["error_code"])
			require.Equal(t, service.KeyRotationRemediationPath, body.Metadata["remediation_path"])
		})
	}
}

func TestKeyRotationGateStatusEndpointIsNotGated(t *testing.T) {
	router, _ := newKeyRotationGateRouteEngine(t)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/user/key-rotation-gate", nil))

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var body struct {
		Code int `json:"code"`
		Data struct {
			Enabled         bool   `json:"enabled"`
			Required        bool   `json:"required"`
			Cutoff          string `json:"cutoff"`
			ActiveKeyCount  int    `json:"active_key_count"`
			PendingKeyCount int    `json:"pending_key_count"`
			ErrorCode       string `json:"error_code"`
			RemediationPath string `json:"remediation_path"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	require.Equal(t, 0, body.Code)
	require.True(t, body.Data.Enabled)
	require.True(t, body.Data.Required)
	require.Equal(t, service.KeyRotationRequiredCode, body.Data.ErrorCode)
	require.Equal(t, service.KeyRotationRemediationPath, body.Data.RemediationPath)
	require.Equal(t, 1, body.Data.ActiveKeyCount)
	require.Equal(t, 1, body.Data.PendingKeyCount)
	require.NotEmpty(t, body.Data.Cutoff)
}

func TestKeyRotationGateLeavesNonRechargeEndpointsAlone(t *testing.T) {
	router, _ := newKeyRotationGateRouteEngine(t)

	// /payment/config sits on the authenticated payment group but outside
	// /payment/orders, so the gate must not answer 403 for it. The zero-value
	// handler panics on its nil service; Recovery turns that into a 500, which
	// still proves the request reached the handler instead of the gate.
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/payment/config", nil))
	require.NotEqual(t, http.StatusForbidden, recorder.Code)
	require.NotContains(t, recorder.Body.String(), service.KeyRotationRequiredCode)

	// Key management is never gated: the gate middleware is not mounted on it.
	for _, path := range []string{"/api/v1/keys", "/api/v1/keys/concurrency"} {
		recorder = httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		require.NotEqual(t, http.StatusForbidden, recorder.Code, path)
		require.NotContains(t, recorder.Body.String(), service.KeyRotationRequiredCode, path)
	}
}
