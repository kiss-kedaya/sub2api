package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

// All accessors reject observers before dereferencing the service. This also
// protects the handlers if a future route accidentally omits the admin group.
func TestAccountOpsHandlersDenyObserverAdministrators(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &AccountOpsHandler{}
	for _, fn := range []gin.HandlerFunc{h.GetConfig, h.SaveConfig, h.List, h.BalanceAccounts, h.ThresholdAccounts, h.Groups, h.SaveGroups, h.TestWebhook, h.SaveNotificationSettings, h.SaveWebhook, h.DeleteWebhook, h.SaveRule, h.SaveRulesBatch, h.DeleteRule} {
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
