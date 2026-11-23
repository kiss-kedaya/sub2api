package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type smallStepUpUserRepo struct{ service.UserRepository }

func (*smallStepUpUserRepo) GetUserAvatar(context.Context, int64) (*service.UserAvatar, error) {
	return nil, nil
}
func (*smallStepUpUserRepo) GetByID(context.Context, int64) (*service.User, error) {
	return &service.User{ID: 1, Role: service.RoleAdmin, TotpEnabled: true}, nil
}

type smallStepUpCache struct{ service.TotpCache }

func (*smallStepUpCache) HasStepUpGrant(context.Context, int64, string, service.Factor) (bool, error) {
	return false, nil
}

// 无授权的桩：门控不会走到剩余时间读取，返回 false 才能保持「不放行」语义。
func (*smallStepUpCache) StepUpGrantRemaining(context.Context, int64, string, service.Factor) (time.Duration, bool, error) {
	return 0, false, nil
}

func TestSmallRetentionPrimaryAdminAndStepUp(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &RequestCaptureHandler{}
	for name, handler := range map[string]gin.HandlerFunc{"detail": h.SmallDetail, "clear": h.SmallClear} {
		for _, tc := range []struct {
			name   string
			id     int64
			method string
			status int
		}{{"observer", 2, "", 403}, {"machine", 1, service.AuditAuthMethodAdminAPIKey, 403}, {"no-stepup-service", 1, "", 503}} {
			t.Run(name+"/"+tc.name, func(t *testing.T) {
				r := gin.New()
				r.GET("/retention", func(c *gin.Context) {
					c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: tc.id})
					c.Set(string(middleware.ContextKeyUserRole), "admin")
					c.Set("auth_method", tc.method)
					handler(c)
				})
				w := httptest.NewRecorder()
				r.ServeHTTP(w, httptest.NewRequest("GET", "/retention", nil))
				require.Equal(t, tc.status, w.Code)
				require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
			})
		}
	}
}
func TestSmallRetentionRequiresRecentTwoFactorGrant(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &smallStepUpUserRepo{}
	h := &RequestCaptureHandler{}
	h.SetStepUpDependencies(service.NewTotpService(repo, nil, &smallStepUpCache{}, nil, nil, nil), service.NewUserService(repo, nil, nil, nil))
	r := gin.New()
	r.GET("/retention", func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1})
		c.Set(string(middleware.ContextKeyUserRole), "admin")
		h.SmallDetail(c)
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/retention", nil))
	require.Equal(t, 403, w.Code)
	require.Contains(t, w.Body.String(), "STEP_UP_REQUIRED")
}

func TestSmallRetentionMetadataRequiresPrimaryAdminAndMissingTablesFailClosed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &RequestCaptureHandler{}
	for _, handler := range []gin.HandlerFunc{h.SmallUpdateConfig} {
		r := gin.New()
		r.PUT("/config", func(c *gin.Context) {
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 2})
			c.Set(string(middleware.ContextKeyUserRole), "admin")
			handler(c)
		})
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("PUT", "/config", nil))
		require.Equal(t, 403, w.Code)
	}
	for _, id := range []int64{2, 1} {
		r := gin.New()
		r.GET("/retention", func(c *gin.Context) {
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: id})
			c.Set(string(middleware.ContextKeyUserRole), "admin")
		}, h.SmallGate, func(c *gin.Context) { c.String(200, "unexpected") })
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/retention", nil))
		if id == 2 {
			require.Equal(t, 403, w.Code)
		} else {
			require.Equal(t, 503, w.Code)
		}
		require.NotContains(t, w.Body.String(), "unexpected")
	}
}
