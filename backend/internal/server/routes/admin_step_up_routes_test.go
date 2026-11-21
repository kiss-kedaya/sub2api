//go:build unit

package routes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const stepUpProbeHeader = "X-Test-Step-Up-Grant"

// stepUpRouteSettingsRepo 是只读的设置仓储桩，满足 step-up / 合规守卫的最小读取。
type stepUpRouteSettingsRepo struct {
	values map[string]string
}

func (r *stepUpRouteSettingsRepo) Get(_ context.Context, key string) (*service.Setting, error) {
	value, ok := r.values[key]
	if !ok {
		return nil, service.ErrSettingNotFound
	}
	return &service.Setting{Key: key, Value: value}, nil
}

func (r *stepUpRouteSettingsRepo) GetValue(_ context.Context, key string) (string, error) {
	if value, ok := r.values[key]; ok {
		return value, nil
	}
	return "", service.ErrSettingNotFound
}

func (r *stepUpRouteSettingsRepo) Set(context.Context, string, string) error { return nil }
func (r *stepUpRouteSettingsRepo) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	result := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := r.values[key]; ok {
			result[key] = value
		}
	}
	return result, nil
}
func (r *stepUpRouteSettingsRepo) SetMultiple(context.Context, map[string]string) error { return nil }
func (r *stepUpRouteSettingsRepo) GetAll(context.Context) (map[string]string, error) {
	return r.values, nil
}
func (r *stepUpRouteSettingsRepo) Delete(context.Context, string) error { return nil }

func stepUpRouteSettings(values map[string]string) *service.SettingService {
	return service.NewSettingService(&stepUpRouteSettingsRepo{values: values}, nil)
}

// stepUpProbe 作为 stepUpAuth 的替身：带 grant 头时 204 并中止（证明中间件已执行），
// 否则 403 STEP_UP_REQUIRED（证明请求在进入业务处理器前被拦住）。
func stepUpProbe() middleware.StepUpAuthMiddleware {
	return middleware.StepUpAuthMiddleware(func(c *gin.Context) {
		if c.GetHeader(stepUpProbeHeader) == "1" {
			c.Status(http.StatusNoContent)
			c.Abort()
			return
		}
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"reason": "STEP_UP_REQUIRED"})
	})
}

func adminRoutesTestAuth(authMode string) middleware.AdminAuthMiddleware {
	return middleware.AdminAuthMiddleware(func(c *gin.Context) {
		c.Set("auth_method", authMode)
		// 观察员身份（非 1 号管理员）跳过行政合规确认，测试只关注 step-up 装配。
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 9})
		c.Next()
	})
}

func buildAdminRouteProbeRouter(t *testing.T, stepUp middleware.StepUpAuthMiddleware, authMode string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery())
	RegisterAdminRoutes(
		r.Group("/api/v1"),
		&handler.Handlers{Admin: &handler.AdminHandlers{}},
		adminRoutesTestAuth(authMode),
		middleware.AuditLogMiddleware(func(c *gin.Context) { c.Next() }),
		stepUp,
		stepUpRouteSettings(map[string]string{}),
		nil,
	)
	return r
}

func buildPaymentProbeRouter(t *testing.T, stepUp middleware.StepUpAuthMiddleware, authMode string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery())
	RegisterPaymentRoutes(
		r.Group("/api/v1"),
		&handler.PaymentHandler{},
		&handler.PaymentWebhookHandler{},
		&adminhandler.PaymentHandler{},
		middleware.JWTAuthMiddleware(func(c *gin.Context) { c.Next() }),
		adminRoutesTestAuth(authMode),
		middleware.AuditLogMiddleware(func(c *gin.Context) { c.Next() }),
		stepUp,
		nil,
		stepUpRouteSettings(map[string]string{}),
		nil,
	)
	return r
}

type stepUpRouteCase struct {
	method string
	path   string
	router string
}

func sensitiveStepUpRoutes() []stepUpRouteCase {
	return []stepUpRouteCase{
		// 系统设置（含支付/邮件/注册等敏感配置）
		{http.MethodPut, "/api/v1/admin/settings", "admin"},
		// 删号、改余额
		{http.MethodDelete, "/api/v1/admin/users/1", "admin"},
		{http.MethodPost, "/api/v1/admin/users/1/balance", "admin"},
		// 给账号绑定第三方登录身份（会新增一条登录路径）
		{http.MethodPost, "/api/v1/admin/users/1/auth-identities", "admin"},
		// 上游失败 dump 的正文含客户端请求体与上游响应体
		{http.MethodGet, "/api/v1/admin/debug-dumps/test.txt", "admin"},
		// 支付渠道配置
		{http.MethodPut, "/api/v1/admin/payment/config", "payment"},
		{http.MethodPost, "/api/v1/admin/payment/providers", "payment"},
		{http.MethodPut, "/api/v1/admin/payment/providers/1", "payment"},
		{http.MethodDelete, "/api/v1/admin/payment/providers/1", "payment"},
		// 退款与退款确认
		{http.MethodPost, "/api/v1/admin/payment/orders/1/refund", "payment"},
		{http.MethodPost, "/api/v1/admin/payment/orders/1/refund/query", "payment"},
	}
}

// 每个敏感路由都必须挂上 step-up：无授权被拦，有授权才进入业务层。
func TestSensitiveAdminRoutesAreStepUpGated(t *testing.T) {
	adminRouter := buildAdminRouteProbeRouter(t, stepUpProbe(), service.AuditAuthMethodJWT)
	paymentRouter := buildPaymentProbeRouter(t, stepUpProbe(), service.AuditAuthMethodJWT)
	for _, tc := range sensitiveStepUpRoutes() {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			router := adminRouter
			if tc.router == "payment" {
				router = paymentRouter
			}
			// 未完成 step-up：403。
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
			require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
			require.Contains(t, w.Body.String(), "STEP_UP_REQUIRED")

			// 完成 step-up：放行（probe 返回 204，证明中间件确实执行过）。
			w = httptest.NewRecorder()
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.Header.Set(stepUpProbeHeader, "1")
			router.ServeHTTP(w, req)
			require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
		})
	}
}

// 机器凭据（管理员 API Key）与其它 step-up 路由语义一致：即使有 grant 也 403。
func TestGatedAdminRoutesRejectAdminAPIKeyMachineCredential(t *testing.T) {
	settings := stepUpRouteSettings(map[string]string{service.SettingKeyStepUpEnabled: "true"})
	realStepUp := middleware.NewStepUpAuthMiddleware(nil, nil, settings)
	adminRouter := buildAdminRouteProbeRouter(t, realStepUp, service.AuditAuthMethodAdminAPIKey)
	paymentRouter := buildPaymentProbeRouter(t, realStepUp, service.AuditAuthMethodAdminAPIKey)
	for _, tc := range sensitiveStepUpRoutes() {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			router := adminRouter
			if tc.router == "payment" {
				router = paymentRouter
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
			require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
			require.Contains(t, w.Body.String(), "STEP_UP_ADMIN_API_KEY_FORBIDDEN")
		})
	}
}

// step_up 开关关闭时保持门控引入前的既有行为（不误伤现有部署）。
func TestSensitiveAdminRoutesStepUpDisabledKeepsLegacyBehavior(t *testing.T) {
	settings := stepUpRouteSettings(map[string]string{service.SettingKeyStepUpEnabled: "false"})
	realStepUp := middleware.NewStepUpAuthMiddleware(nil, nil, settings)
	// 用真实 step-up + 一个 204 的业务桩，验证关闭时 API Key 也能通过。
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("auth_method", service.AuditAuthMethodAdminAPIKey)
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 9})
		c.Next()
	})
	r.PUT("/api/v1/admin/settings", gin.HandlerFunc(realStepUp), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings", nil))
	require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
}
