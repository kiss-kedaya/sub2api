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

func TestCapturedPayloadAccessCannotBypassPrimarySessionAndStepUp(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &RequestCaptureHandler{}
	for name, handler := range map[string]gin.HandlerFunc{"content": h.Content, "export": h.Export, "preflight": h.AuthorizeSensitiveRead} {
		for _, tc := range []struct {
			name   string
			id     int64
			method string
			want   int
		}{
			{"observer", 8, "", http.StatusForbidden},
			{"primary-machine-key", 1, service.AuditAuthMethodAdminAPIKey, http.StatusForbidden},
			{"missing-stepup-dependency", 1, "", http.StatusServiceUnavailable},
		} {
			t.Run(name+"/"+tc.name, func(t *testing.T) {
				r := gin.New()
				r.GET("/sensitive", func(c *gin.Context) {
					c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: tc.id})
					c.Set(string(middleware.ContextKeyUserRole), "admin")
					c.Set("auth_method", tc.method)
					handler(c)
				})
				w := httptest.NewRecorder()
				r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/sensitive", nil))
				require.Equal(t, tc.want, w.Code)
				require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
				require.NotContains(t, w.Body.String(), "authorized")
			})
		}
	}
}

func TestCaptureConfigurationRejectsObserversBeforeReadingOrWritingSettings(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &RequestCaptureHandler{}
	for _, handler := range []gin.HandlerFunc{h.GetConfig, h.UpdateConfig, h.Gate} {
		r := gin.New()
		r.GET("/config", func(c *gin.Context) {
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 2})
			c.Set(string(middleware.ContextKeyUserRole), "admin")
			handler(c)
		})
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/config", nil))
		require.Equal(t, http.StatusForbidden, w.Code)
	}
}
