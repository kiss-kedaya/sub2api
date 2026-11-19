package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestDelegatedPaymentRoutesGuardRejectsMisroutedPayments(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, tc := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/crypto"},
		{http.MethodGet, "/api/v1/crypto/"},
		{http.MethodGet, "/api/v1/crypto/deposit-info"},
		{http.MethodGet, "/api/v1/admin/crypto"},
		{http.MethodPut, "/api/v1/admin/crypto/config"},
		{http.MethodGet, "/api/v1/payment"},
		{http.MethodPost, "/api/v1/payment/orders"},
		{http.MethodPost, "/api/v1/payment/public/orders/resolve"},
		{http.MethodGet, "/api/v1/payment/webhook/easypay?trade_no=example"},
		{http.MethodPost, "/api/v1/payment/webhook/epusdt"},
		{http.MethodGet, "/api/v1/admin/payment"},
		{http.MethodDelete, "/api/v1/admin/payment/plans/42"},
		{http.MethodPut, "/api/v1/admin/settings"},
		{http.MethodPost, "/api/v1/payments/callback"},
		{http.MethodPatch, "/api/v1/payments/callback"},
		{http.MethodOptions, "/api/v1/payments/callback"},
		{http.MethodHead, "/api/v1/payments/callback"},
		{http.MethodGet, "/api/v1/auth/oauth/wechat/payment"},
		{http.MethodGet, "/api/v1/auth/oauth/wechat/payment/start"},
		{http.MethodGet, "/api/v1/auth/oauth/wechat/payment/callback"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			router := gin.New()
			router.Use(servermiddleware.DelegatedPaymentRoutesGuard())
			router.Use(servermiddleware.CORS(config.CORSConfig{AllowedOrigins: []string{"*"}}))
			handlerCalled := false
			paymentHandler := func(c *gin.Context) {
				handlerCalled = true
				c.Status(http.StatusNoContent)
			}
			router.POST("/api/v1/payments/callback", paymentHandler)
			router.PUT("/api/v1/admin/settings", paymentHandler)
			request := httptest.NewRequest(tc.method, tc.path, nil)
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)

			require.Equal(t, http.StatusServiceUnavailable, recorder.Code, recorder.Body.String())
			require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
			require.Equal(t, "application/json; charset=utf-8", recorder.Header().Get("Content-Type"))
			require.Empty(t, recorder.Header().Get("Location"))
			if tc.method != http.MethodHead {
				require.JSONEq(t, `{"code":503,"message":"payments are unavailable on this gateway","reason":"PAYMENTS_DELEGATED"}`, recorder.Body.String())
			}
			require.False(t, handlerCalled)
		})
	}
}

func TestDelegatedPaymentRoutesGuardPassesOtherRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, tc := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/health"},
		{http.MethodGet, "/v1/models"},
		{http.MethodPost, "/v1/responses"},
		{http.MethodGet, "/v1beta/models"},
		{http.MethodGet, "/api/v1/user/profile"},
		{http.MethodGet, "/api/v1/settings/public"},
		{http.MethodGet, "/api/v1/admin/settings"},
		{http.MethodPut, "/api/v1/admin/settings/panel-rate-limit"},
		{http.MethodGet, "/api/v1/auth/oauth/wechat/start"},
		{http.MethodGet, "/api/v1/auth/oauth/wechat/callback"},
		{http.MethodGet, "/api/v1/payment-settings"},
		{http.MethodGet, "/api/v1/admin/cryptography"},
		{http.MethodGet, "/api/v1/payments/callback-status"},
		{http.MethodGet, "/api/v1/auth/oauth/wechat/payment-status"},
		{http.MethodGet, "/purchase"},
		{http.MethodGet, "/admin/crypto"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			router := gin.New()
			router.Use(servermiddleware.DelegatedPaymentRoutesGuard())
			router.Handle(tc.method, tc.path, func(c *gin.Context) { c.Status(http.StatusNoContent) })
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(tc.method, tc.path, nil))

			require.Equal(t, http.StatusNoContent, recorder.Code, recorder.Body.String())
			require.Empty(t, recorder.Header().Get("Cache-Control"))
		})
	}
}
