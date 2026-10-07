package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestScheduledTestAuthorizationPreservesConfiguredOwnerScope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, email        string
		ownerID, accountID int64
		allowed            bool
	}{
		{"owner renamed legacy", "renamed@example.test", 7, 13, true},
		{"owner other upload", "renamed@example.test", 7, 12, true},
		{"ordinary own upload", "other@example.test", 0, 11, true},
		{"legacy email cannot elevate", "admin@sub2api.local", 0, 13, false},
		{"ordinary other upload", "other@example.test", 0, 12, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("GET", "/plans", nil)
			c.Request = c.Request.WithContext(service.WithAccountOwnerScope(c.Request.Context(), 7, tc.ownerID))
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 7})
			c.Set(middleware.ContextKeyAuthEmail, tc.email)
			h := NewScheduledTestHandler(nil, newOwnerScopeGuardHandler().adminService)
			require.Equal(t, tc.allowed, h.authorizeAccount(c, tc.accountID))
			if !tc.allowed {
				require.Equal(t, http.StatusNotFound, w.Code)
			}
			_, all, scoped := service.AccountOwnerScopeDetail(c.Request.Context())
			require.True(t, scoped)
			require.Equal(t, tc.ownerID == 7, all)
		})
	}
}
