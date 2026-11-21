package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/adminroutepolicy"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 本文件把「2FA 验证点能否被跳过」的审计清单逐条落成可执行断言。
// 每条断言对应 PR 审计表里的一行；绕过面一旦被改回去，测试直接红。
//
// 审计范围（证据文件:行见 PR）：
//   1. 路由分类表覆盖率（adminroutepolicy：未归类 = fail-closed）
//   2. 组级注册表守卫（routes/admin_route_policy.go）
//   3. handler 级 EnforceStepUp / EnforceStepUpAlways 调用点
//   4. admin API key 机器凭证
//   5. TOTP 未启用用户
//   6. 设置读取失败 / 依赖装配缺失 fail-closed
//   7. 会话绑定（不能拿别的会话的 grant）
//   8. 内部直接调用路径（不经路由的 service 调用，见 PR 审计结论）

// 1. 路由分类表：未归类管理面路由按 fail-closed 要求 step-up；机器白名单
// 之外的写路由全部要求 step-up，不存在「漏归类=放行」。
func TestAuditRoutePolicyFailsClosedForUnclassifiedRoutes(t *testing.T) {
	const unknown = "/api/v1/admin/zz-audit-unclassified"
	require.True(t, adminroutepolicy.IsStepUpRequired("POST", unknown),
		"未归类路由必须 fail-closed 要求 step-up")

	resolved, ok := adminroutepolicy.Resolve("GET", "/api/v1/admin/dashboard/stats")
	require.True(t, ok)
	require.Equal(t, adminroutepolicy.ClassMachineAllowlisted, resolved.Class)
	require.False(t, adminroutepolicy.IsStepUpRequired("GET", "/api/v1/admin/dashboard/stats"),
		"机器白名单只允许无秘密聚合接口")

	for _, route := range adminroutepolicy.MachineAllowlist() {
		require.False(t, adminroutepolicy.IsStepUpRequired(route.Method, route.Path),
			"机器白名单路由不应要求 step-up: %s %s", route.Method, route.Path)
	}

	// 反向断言：删用户/改余额这类高危写操作必须要求 step-up（分类表没有放开）。
	for _, route := range []struct{ method, path string }{
		{http.MethodDelete, "/api/v1/admin/users/:id"},
		{http.MethodPost, "/api/v1/admin/users/:id/balance"},
		{http.MethodGet, "/api/v1/admin/accounts/:id/credential"},
		{http.MethodGet, "/api/v1/admin/backups/:id/download-url"},
		{http.MethodPost, "/api/v1/admin/upstream-monitor/channels/:id/api-keys/:key_id/reveal"},
	} {
		require.True(t, adminroutepolicy.IsStepUpRequired(route.method, route.path),
			"%s %s 必须要求 step-up", route.method, route.path)
	}
}

// 4. admin API key 机器凭证无法完成 TOTP：即使 Redis 里有人手工写了 grant，
// 机器凭证也必须被拒绝（两条认证路径都测）。
func TestAuditAdminAPIKeyCannotBypassStepUp(t *testing.T) {
	for _, authMethod := range []string{
		service.AuditAuthMethodAdminAPIKey,
		service.AuditAuthMethodScopedAdminAPIKey,
	} {
		t.Run(authMethod, func(t *testing.T) {
			rec := serveSkipTestRequest(t, skipTestOptions{
				checker:    stubStepUpGrantChecker{granted: true, remaining: time.Minute},
				authMethod: authMethod,
			})
			require.Equal(t, http.StatusForbidden, rec.Code)
			require.Contains(t, rec.Body.String(), "STEP_UP_ADMIN_API_KEY_FORBIDDEN")
			require.Empty(t, rec.Header().Get(StepUpSkippedHeader))
		})
	}
}

// 5. TOTP 未启用的用户没有可验证的第二因子 → 拒绝，不能因为「有 grant 残留」放行。
func TestAuditUserWithoutTotpCannotBypassStepUp(t *testing.T) {
	rec := serveSkipTestRequest(t, skipTestOptions{
		checker: stubStepUpGrantChecker{granted: true, remaining: time.Minute},
		user:    &service.User{ID: 42, TotpEnabled: false, Role: service.RoleAdmin, Status: service.StatusActive},
	})
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Contains(t, rec.Body.String(), "STEP_UP_TOTP_NOT_ENABLED")
}

// 6a. 设置读取失败 → fail-closed（不能因为存储抖动把门控关掉）。
func TestAuditSettingsReadErrorFailsClosed(t *testing.T) {
	rec := serveSkipTestRequest(t, skipTestOptions{
		checker:  stubStepUpGrantChecker{granted: true},
		settings: stubStepUpSettingReader{err: errors.New("settings down")},
	})
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Contains(t, rec.Body.String(), "STEP_UP_UNAVAILABLE")
}

// 6b. 依赖装配缺失（totp/user service 为 nil）→ fail-closed，而不是「没法检查就放行」。
// 这条专门覆盖 handler 级 EnforceStepUp 调用点：装配漏了也不能成为绕过面。
func TestAuditMissingDependenciesFailClosed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(ContextKeyUser), AuthSubject{UserID: 42})
		c.Set(ContextKeySessionID, "session-a")
	})
	router.PUT("/api/v1/admin/settings", func(c *gin.Context) {
		// 传入 nil 的具体指针（= 门控依赖没装配）。
		if !EnforceStepUpAlways(c, nil, nil) {
			return
		}
		c.Status(http.StatusNoContent)
	})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings", nil))
	require.Equal(t, http.StatusServiceUnavailable, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "STEP_UP_UNAVAILABLE")
}

// 7. grant 绑定会话：别的会话（同一用户）既读不到也借不到授权。
type auditSessionBoundChecker struct {
	boundSession string
	remaining    time.Duration
}

func (c auditSessionBoundChecker) HasStepUpGrant(_ context.Context, _ int64, sessionKey string) (bool, error) {
	return sessionKey == c.boundSession, nil
}

func (c auditSessionBoundChecker) StepUpGrantRemaining(_ context.Context, _ int64, sessionKey string) (time.Duration, bool, error) {
	if sessionKey != c.boundSession {
		return 0, false, nil
	}
	return c.remaining, true, nil
}

func TestAuditGrantIsBoundToSession(t *testing.T) {
	rec := serveSkipTestRequest(t, skipTestOptions{
		checker: auditSessionBoundChecker{boundSession: "session-a", remaining: time.Minute},
		session: "session-b",
	})
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Contains(t, rec.Body.String(), "STEP_UP_REQUIRED")

	ok := serveSkipTestRequest(t, skipTestOptions{
		checker: auditSessionBoundChecker{boundSession: "session-a", remaining: time.Minute},
		session: "session-a",
	})
	require.Equal(t, http.StatusNoContent, ok.Code, ok.Body.String())
}

// 3. EnforceStepUpAlways（不读开关）在无授权时永远拒绝：调用点不能靠「开关读不到」
// 或「用户没授权」偷偷放行。
func TestAuditAlwaysEnforcementIgnoresFeatureSwitch(t *testing.T) {
	rec := serveSkipTestRequest(t, skipTestOptions{
		checker: stubStepUpGrantChecker{granted: false},
		always:  true,
	})
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Contains(t, rec.Body.String(), "STEP_UP_REQUIRED")
}

// 9. 「跳过」只发生在真有有效 grant 时：无 grant 的响应不得带跳过头，防止前端
// 误以为 2FA 已被跳过而掩盖验证失败。
func TestAuditSkipHeadersOnlyWhenGrantActuallyUsed(t *testing.T) {
	rejected := serveSkipTestRequest(t, skipTestOptions{
		checker:  stubStepUpGrantChecker{granted: false},
		settings: stepUpEnabled,
	})
	require.Equal(t, http.StatusForbidden, rejected.Code)
	require.Empty(t, rejected.Header().Get(StepUpSkippedHeader))

	skipped := serveSkipTestRequest(t, skipTestOptions{
		checker: stubStepUpGrantChecker{granted: true, remaining: 30 * time.Second},
	})
	require.Equal(t, "1", skipped.Header().Get(StepUpSkippedHeader))
	require.Equal(t, "30", skipped.Header().Get(StepUpRemainingHeader))
}
