package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAccountOpsAdminReferenceChangesRequireStepUp(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, body := range []string{`{"use_admin_email":true}`, `{"use_admin_bark":true}`, `{"use_admin_email":false}`, `{"use_admin_bark":false}`} {
		t.Run(body, func(t *testing.T) {
			repo := &globalTotpActorRepo{actor: &service.User{ID: 1, Role: service.RoleAdmin, Status: service.StatusActive, TotpEnabled: true}}
			cache := &globalTotpGrantCache{grants: make(map[globalTotpGrantKey]bool)}
			h := &AccountOpsHandler{}
			h.SetStepUpDependencies(service.NewTotpService(repo, nil, cache, nil, nil, nil), service.NewUserService(repo, nil, nil, nil))
			r := gin.New()
			r.PUT("/notification-settings", func(c *gin.Context) {
				c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1})
				c.Set(string(middleware.ContextKeyUserRole), "admin")
				c.Set(middleware.ContextKeySessionID, "account-ops-session")
				h.SaveNotificationSettings(c)
			})
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPut, "/notification-settings", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(w, req)
			require.Equal(t, http.StatusForbidden, w.Code)
			require.Contains(t, w.Body.String(), "STEP_UP_REQUIRED")
			require.Equal(t, 1, cache.checks)
		})
	}
}

func TestAccountOpsAdminChannelTestsRequireStepUp(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &AccountOpsHandler{}
	r := gin.New()
	r.POST("/admin-defaults/:channel/test", func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1})
		c.Set(string(middleware.ContextKeyUserRole), "admin")
		c.Set("auth_method", service.AuditAuthMethodAdminAPIKey)
	}, h.RequireStepUp, h.TestAdminChannel)
	for _, channel := range []string{"email", "bark"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/admin-defaults/"+channel+"/test", nil))
		require.Equal(t, http.StatusForbidden, w.Code)
		require.Contains(t, w.Body.String(), "STEP_UP_ADMIN_API_KEY_FORBIDDEN")
	}
}

// All accessors reject observers before dereferencing the service. This also
// protects the handlers if a future route accidentally omits the admin group.
func TestAccountOpsHandlersDenyObserverAdministrators(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &AccountOpsHandler{}
	for _, fn := range []gin.HandlerFunc{h.GetConfig, h.SaveConfig, h.List, h.BalanceAccounts, h.ThresholdAccounts, h.Groups, h.SaveGroups, h.TestWebhook, h.TestAdminChannel, h.SaveNotificationSettings, h.SaveWebhook, h.DeleteWebhook, h.SaveRule, h.SaveRulesBatch, h.DeleteRule} {
		r := gin.New()
		r.POST("/test", func(c *gin.Context) {
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 9})
			c.Set(string(middleware.ContextKeyUserRole), "admin")
			fn(c)
		})
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(`{}`)))
		if w.Code != http.StatusForbidden {
			t.Fatalf("observer status %d", w.Code)
		}
	}
}
