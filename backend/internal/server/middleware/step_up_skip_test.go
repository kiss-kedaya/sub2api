package middleware

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

var errStepUpRedisDown = errors.New("redis down")

type skipTestOptions struct {
	checker    stepUpGrantChecker
	settings   stepUpSettingReader
	authMethod string
	noSubject  bool
	user       *service.User
	// session 指定 step-up 会话键（默认 session-a），用于跨会话断言。
	session string
	// always 走「不读功能开关」的强制门控（settings == nil 语义）。
	always bool
	// handlerLevel 走 handler 内的 EnforceStepUp（而不是组级中间件），
	// 验证 handler 级调用点同样回传跳过头。
	handlerLevel bool
}

// serveSkipTestRequest 在真实的 /api/v1/admin/settings 路由上执行一次门控。
func serveSkipTestRequest(t *testing.T, opts skipTestOptions) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	if opts.settings == nil && !opts.always {
		opts.settings = stepUpEnabled
	}
	if opts.session == "" {
		opts.session = "session-a"
	}
	if opts.user == nil {
		opts.user = &service.User{ID: 42, TotpEnabled: true, Role: service.RoleAdmin, Status: service.StatusActive}
	}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		if opts.authMethod != "" {
			c.Set("auth_method", opts.authMethod)
		}
		if !opts.noSubject {
			c.Set(string(ContextKeyUser), AuthSubject{UserID: 42})
			c.Set(ContextKeySessionID, opts.session)
		}
	})
	reader := stubStepUpUserReader{user: opts.user}
	router.PUT("/api/v1/admin/settings", func(c *gin.Context) {
		if opts.handlerLevel {
			// handler 内部的条件门控（组级守卫之外的第二道）：真实代码见
			// admin/user_handler.go 里的 EnforceStepUp / EnforceStepUpAlways。
			if !enforceStepUp(c, opts.checker, reader, opts.settings) {
				return
			}
		}
		if !enforceStepUp(c, opts.checker, reader, opts.settings) {
			return
		}
		c.Status(http.StatusNoContent)
	})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings", nil))
	return rec
}

// 有有效 step-up 授权时敏感请求跳过了验证码，必须回传「跳过 + 剩余秒数」，
// 不允许静默跳过。
func TestStepUpSkipExposesRemainingWindow(t *testing.T) {
	rec := serveSkipTestRequest(t, skipTestOptions{
		checker: stubStepUpGrantChecker{granted: true, remaining: 4*time.Minute + 32*time.Second},
	})

	require.Equal(t, http.StatusNoContent, rec.Code)
	require.Equal(t, "1", rec.Header().Get(StepUpSkippedHeader))
	require.Equal(t, "272", rec.Header().Get(StepUpRemainingHeader))
}

// 5 分钟窗口边界：4:59 仍在窗口内（可用，且剩余秒数正确）。
func TestStepUpSkipStillValidAtFourFiftyNine(t *testing.T) {
	rec := serveSkipTestRequest(t, skipTestOptions{
		checker: stubStepUpGrantChecker{granted: true, remaining: 4*time.Minute + 59*time.Second},
	})

	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	require.Equal(t, "1", rec.Header().Get(StepUpSkippedHeader))
	require.Equal(t, "299", rec.Header().Get(StepUpRemainingHeader))
}

// 5 分钟窗口边界：5:01 已失效（必须重新验证，且不得回传跳过头）。
func TestStepUpRejectsAfterFiveMinuteWindow(t *testing.T) {
	rec := serveSkipTestRequest(t, skipTestOptions{
		checker: stubStepUpGrantChecker{granted: false},
	})

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Contains(t, rec.Body.String(), "STEP_UP_REQUIRED")
	require.Empty(t, rec.Header().Get(StepUpSkippedHeader))
	require.Empty(t, rec.Header().Get(StepUpRemainingHeader))
}

// 剩余时间读不到（授权在两次读取之间过期）→ 按未验证处理。
func TestStepUpRejectsWhenGrantExpiresBetweenChecks(t *testing.T) {
	rec := serveSkipTestRequest(t, skipTestOptions{
		checker: stubStepUpGrantChecker{granted: true, expireAfterCheck: true},
	})

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Contains(t, rec.Body.String(), "STEP_UP_REQUIRED")
	require.Empty(t, rec.Header().Get(StepUpSkippedHeader))
}

// 剩余时间读取故障 → fail-closed，绝不放行也不谎报剩余时间。
func TestStepUpFailsClosedWhenRemainingUnreadable(t *testing.T) {
	rec := serveSkipTestRequest(t, skipTestOptions{
		checker: stubStepUpGrantChecker{granted: true, remainingErr: errStepUpRedisDown},
	})

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Contains(t, rec.Body.String(), "STEP_UP_UNAVAILABLE")
	require.Empty(t, rec.Header().Get(StepUpSkippedHeader))
}

// 剩余时间向下取整：不能把 4:32.9 说成 4:33。
func TestStepUpRemainingSecondsFloor(t *testing.T) {
	rec := serveSkipTestRequest(t, skipTestOptions{
		checker: stubStepUpGrantChecker{granted: true, remaining: 4*time.Minute + 32*time.Second + 900*time.Millisecond},
	})

	require.Equal(t, "272", rec.Header().Get(StepUpRemainingHeader))
}

// 功能开关关闭时本来就不需要 2FA，不得回传「跳过 2FA」误导前端。
func TestStepUpSkipHeaderAbsentWhenFeatureDisabled(t *testing.T) {
	rec := serveSkipTestRequest(t, skipTestOptions{
		checker:  stubStepUpGrantChecker{granted: true},
		settings: stubStepUpSettingReader{enabled: false},
	})

	require.Equal(t, http.StatusNoContent, rec.Code)
	require.Empty(t, rec.Header().Get(StepUpSkippedHeader))
	require.Empty(t, rec.Header().Get(StepUpRemainingHeader))
}

// handler 级 EnforceStepUp（条件门控）同样必须回传跳过头，否则「handler 里额外
// 要求的二次验证」会静默通过，形成绕过面。
func TestHandlerLevelEnforceStepUpExposesSkipHeaders(t *testing.T) {
	rec := serveSkipTestRequest(t, skipTestOptions{
		checker:      stubStepUpGrantChecker{granted: true, remaining: 90 * time.Second},
		handlerLevel: true,
	})

	require.Equal(t, http.StatusNoContent, rec.Code)
	require.Equal(t, "1", rec.Header().Get(StepUpSkippedHeader))
	require.Equal(t, "90", rec.Header().Get(StepUpRemainingHeader))
}

// 机器凭据（旧版全权 key / 受限 adm_sc_ key）永远无法完成 step-up：
// 即使 grant 存在也必须拒绝，且不得回传「跳过」头。
func TestMachineCredentialsCannotSkipStepUp(t *testing.T) {
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

// 未启用 TOTP 的用户无法完成 step-up → 拒绝，不允许靠「配置缺省」跳过。
func TestTotpDisabledUserCannotSkipStepUp(t *testing.T) {
	rec := serveSkipTestRequest(t, skipTestOptions{
		checker: stubStepUpGrantChecker{granted: true, remaining: time.Minute},
		user:    &service.User{ID: 42, TotpEnabled: false, Role: service.RoleAdmin, Status: service.StatusActive},
	})

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Contains(t, rec.Body.String(), "STEP_UP_TOTP_NOT_ENABLED")
	require.Empty(t, rec.Header().Get(StepUpSkippedHeader))
}

// 未认证请求不得进入授权判断。
func TestUnauthenticatedRequestRejectedBeforeGrantCheck(t *testing.T) {
	rec := serveSkipTestRequest(t, skipTestOptions{
		checker:   stubStepUpGrantChecker{granted: true},
		noSubject: true,
	})

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Contains(t, rec.Body.String(), "UNAUTHORIZED")
}
