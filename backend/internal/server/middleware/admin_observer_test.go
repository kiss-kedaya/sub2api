package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestObserverAdministrationIsAnExactReadOnlyAllowlist(t *testing.T) {
	gin.SetMode(gin.TestMode)
	allowed := []string{"/api/v1/admin/usage", "/api/v1/admin/usage/stats", "/api/v1/admin/dashboard/stats", "/api/v1/admin/dashboard/realtime", "/api/v1/admin/dashboard/models", "/api/v1/admin/dashboard/groups", "/api/v1/admin/dashboard/trend", "/api/v1/admin/dashboard/snapshot-v2"}
	denied := []string{"/api/v1/admin/users", "/api/v1/admin/users/:id", "/api/v1/admin/users/:id/balance", "/api/v1/admin/redeem-codes", "/api/v1/admin/redeem-codes/generate", "/api/v1/admin/accounts", "/api/v1/admin/accounts/export", "/api/v1/admin/accounts/:id/credentials", "/api/v1/admin/proxies", "/api/v1/admin/payment/orders/:id/refund", "/api/v1/admin/settings", "/api/v1/admin/settings/admin-api-key", "/api/v1/admin/crypto/config", "/api/v1/admin/backup/download/:id", "/api/v1/admin/usage/search-users", "/api/v1/admin/usage/search-api-keys", "/api/v1/admin/usage/cleanup-tasks", "/api/v1/admin/ops/error-logs", "/api/v1/admin/ops/ws/realtime", "/api/v1/admin/dashboard/users-trend", "/api/v1/admin/dashboard/users-ranking", "/api/v1/admin/dashboard/user-breakdown", "/api/v1/admin/future-sensitive-endpoint"}
	for _, route := range append(allowed, denied...) {
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete} {
			for _, id := range []int64{1, 2} {
				router := gin.New()
				called := false
				router.Handle(method, route, func(c *gin.Context) {
					c.Set(string(ContextKeyUser), AuthSubject{UserID: id})
					c.Set(string(ContextKeyUserRole), "admin")
					if !enforceAdminObserverAccess(c) {
						return
					}
					called = true
					c.Status(http.StatusOK)
				})
				path := strings.ReplaceAll(route, ":id", "2")
				request := httptest.NewRequest(method, path+"?user_id=1&role=admin", nil)
				request.Header.Set("X-Admin-User-ID", "1")
				w := httptest.NewRecorder()
				router.ServeHTTP(w, request)
				canRead := false
				for _, item := range allowed {
					if route == item {
						canRead = true
					}
				}
				want := id == 1 || (method == http.MethodGet && canRead)
				if called != want || (want && w.Code != http.StatusOK) || (!want && w.Code != http.StatusForbidden) {
					t.Fatalf("id=%d %s %s reached=%v status=%d body=%s", id, method, route, called, w.Code, w.Body.String())
				}
			}
		}
	}
}

func TestObserverCannotUpgradeAnAllowedReadToWebSocket(t *testing.T) {
	router := gin.New()
	router.GET("/api/v1/admin/usage", func(c *gin.Context) {
		c.Set(string(ContextKeyUser), AuthSubject{UserID: 2})
		if enforceAdminObserverAccess(c) {
			t.Error("WebSocket observer bypass")
		}
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/usage", nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status=%d", w.Code)
	}
}
