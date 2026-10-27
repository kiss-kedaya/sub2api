package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExcelGrantHandlersDenyObserverAdministrators(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &ExcelGrantHandler{}
	for _, fn := range []gin.HandlerFunc{h.List, h.Save, h.Authorize, h.Callback} {
		r := gin.New()
		r.POST("/test", func(c *gin.Context) {
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 8})
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
