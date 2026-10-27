package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

func TestChannelOperationsHandlersDenyObserverAdministrators(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &ChannelHandler{}
	for _, fn := range []gin.HandlerFunc{h.OperationsCatalog, h.MutateOperations, h.OperationsSnapshot} {
		r := gin.New()
		r.GET("/test", func(c *gin.Context) {
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 9})
			c.Set(string(middleware.ContextKeyUserRole), "admin")
			fn(c)
		})
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/test", nil))
		if w.Code != http.StatusForbidden {
			t.Fatalf("observer status %d", w.Code)
		}
	}
}
