package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAutomationHandlersDenyObserverBeforeAnyDependency(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &OpsAutomationHandler{}
	for _, fn := range []gin.HandlerFunc{h.Get, h.Save, h.Plans, h.SavePlan, h.RunPlan, h.Ownership} {
		r := gin.New()
		r.POST("/test", func(c *gin.Context) {
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 9})
			c.Set(string(middleware.ContextKeyUserRole), "admin")
			fn(c)
		})
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/test", nil))
		require.Equal(t, http.StatusForbidden, w.Code)
	}
}
