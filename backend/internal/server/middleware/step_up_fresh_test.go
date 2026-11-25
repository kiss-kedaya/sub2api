package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/stretchr/testify/require"
)

// 「新鲜」二次验证：只有显式传入 maxAge 的高危处理器才要求授权是刚签发的；
// 其它路由沿用共享的 5 分钟窗口（maxAge <= 0），语义不变。

const freshTestWindow = 60 * time.Second

// enforceFreshForUser1 以用户 1（已启用 TOTP）的身份跑一次 enforceStepUpWithin。
func enforceFreshForUser1(t *testing.T, grants stepUpGrantChecker, maxAge time.Duration) (bool, *httptest.ResponseRecorder) {
	t.Helper()
	c, rec := newStepUpTestContext(t)
	c.Set(string(ContextKeyUser), AuthSubject{UserID: 1})
	passed := enforceStepUpWithin(c, grants, stubStepUpUserReader{user: &service.User{ID: 1, TotpEnabled: true}}, stepUpEnabled, maxAge)
	return passed, rec
}

func TestEnforceStepUpWithinRejectsAGrantOlderThanMaxAge(t *testing.T) {
	// 授权剩余 3.5 分钟 = 90 秒前签发：在共享窗口内，但不够新鲜。
	passed, rec := enforceFreshForUser1(t, stubStepUpGrantChecker{granted: true, remaining: 3*time.Minute + 30*time.Second}, freshTestWindow)
	require.False(t, passed)
	require.Equal(t, http.StatusForbidden, rec.Code)
	// 与「没有授权」同一个机器码：前端现有的验证码弹窗直接接管，输入当前验证码后重试。
	require.Contains(t, rec.Body.String(), "STEP_UP_REQUIRED")
	require.Contains(t, rec.Body.String(), "fresh")
}

func TestEnforceStepUpWithinAcceptsAGrantIssuedWithinMaxAge(t *testing.T) {
	// 刚签发（剩余接近整个窗口）与 30 秒前签发都算新鲜。
	for _, remaining := range []time.Duration{service.StepUpGrantTTL - time.Second, service.StepUpGrantTTL - 30*time.Second} {
		passed, _ := enforceFreshForUser1(t, stubStepUpGrantChecker{granted: true, remaining: remaining}, freshTestWindow)
		require.True(t, passed, "remaining=%s", remaining)
	}
	// 边界：恰好 60 秒前签发仍算新鲜，多一毫秒就不算。
	passed, _ := enforceFreshForUser1(t, stubStepUpGrantChecker{granted: true, remaining: service.StepUpGrantTTL - freshTestWindow}, freshTestWindow)
	require.True(t, passed)
	passed, _ = enforceFreshForUser1(t, stubStepUpGrantChecker{granted: true, remaining: service.StepUpGrantTTL - freshTestWindow - time.Millisecond}, freshTestWindow)
	require.False(t, passed)
}

// 授权的年龄 = 窗口总长 - 剩余时间。剩余时间比窗口总长还长（遗留键、改小 TTL 常量之前留下的键）时年龄是负数，
// 不是按 StepUpGrantTTL 签发的授权，无法证明它「新鲜」：fail-closed，不能因为负数不大于 maxAge 就放行（审计 AUD-6）。
func TestEnforceStepUpWithinRejectsAGrantWhoseRemainingTTLExceedsTheWindow(t *testing.T) {
	for _, remaining := range []time.Duration{
		service.StepUpGrantTTL + time.Millisecond,
		service.StepUpGrantTTL + time.Minute,
		14 * time.Minute,
		24 * time.Hour,
	} {
		passed, rec := enforceFreshForUser1(t, stubStepUpGrantChecker{granted: true, remaining: remaining}, freshTestWindow)
		require.False(t, passed, "remaining=%s", remaining)
		require.Equal(t, http.StatusForbidden, rec.Code, "remaining=%s", remaining)
		require.Contains(t, rec.Body.String(), "STEP_UP_REQUIRED", "remaining=%s", remaining)
	}

	// 边界：剩余时间恰好等于窗口总长（年龄 0，刚签发）仍然放行。
	passed, _ := enforceFreshForUser1(t, stubStepUpGrantChecker{granted: true, remaining: service.StepUpGrantTTL}, freshTestWindow)
	require.True(t, passed)

	// 只影响要求新鲜度的路由：不传 maxAge 的既有路由语义不变。
	passed, _ = enforceFreshForUser1(t, stubStepUpGrantChecker{granted: true, remaining: 14 * time.Minute}, 0)
	require.True(t, passed)
}

// 多因子：任何一个因子的剩余时间异常长，同样整体拒绝。
func TestEnforceStepUpWithinRejectsAnyFactorWithAnOverlongRemainingTTL(t *testing.T) {
	grants := factorAgedGrants{
		service.FactorTOTP:    service.StepUpGrantTTL - 10*time.Second,
		service.FactorPasskey: 14 * time.Minute,
	}
	passed, rec := enforceFreshForUser1(t, grants, freshTestWindow)
	require.False(t, passed)
	require.Contains(t, rec.Body.String(), "STEP_UP_REQUIRED")
}

// 路由级门控放行时已经写了「已跳过验证码」的响应头。新鲜度检查随后拒绝这个请求，
// 没有跳过任何验证：这些响应头必须撤掉，否则前端会提示「刚刚免验证执行了敏感操作」。
func TestEnforceStepUpWithinRejectionDropsTheSkipHeadersAnEarlierGateWrote(t *testing.T) {
	c, rec := newStepUpTestContext(t)
	c.Set(string(ContextKeyUser), AuthSubject{UserID: 1})
	c.Header(StepUpSkippedHeader, "1")
	c.Header(StepUpRemainingHeader, "210")
	c.Header(StepUpFactorsHeader, "totp")

	require.False(t, enforceStepUpWithin(c, stubStepUpGrantChecker{granted: true, remaining: 3*time.Minute + 30*time.Second},
		stubStepUpUserReader{user: &service.User{ID: 1, TotpEnabled: true}}, stepUpEnabled, freshTestWindow))

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Empty(t, rec.Header().Get(StepUpSkippedHeader))
	require.Empty(t, rec.Header().Get(StepUpRemainingHeader))
	require.Empty(t, rec.Header().Get(StepUpFactorsHeader))

	// 放行时（授权新鲜）照常带着已跳过的响应头，行为不变。
	c, rec = newStepUpTestContext(t)
	c.Set(string(ContextKeyUser), AuthSubject{UserID: 1})
	require.True(t, enforceStepUpWithin(c, stubStepUpGrantChecker{granted: true, remaining: service.StepUpGrantTTL - 10*time.Second},
		stubStepUpUserReader{user: &service.User{ID: 1, TotpEnabled: true}}, stepUpEnabled, freshTestWindow))
	require.Equal(t, "1", rec.Header().Get(StepUpSkippedHeader))
}

// 不传 maxAge（所有既有路由）：窗口内最老的授权也照常放行——别的路由的窗口语义不变。
func TestEnforceStepUpKeepsTheSharedFiveMinuteWindowForEveryOtherRoute(t *testing.T) {
	for _, remaining := range []time.Duration{time.Second, 30 * time.Second, 3 * time.Minute} {
		c, _ := newStepUpTestContext(t)
		c.Set(string(ContextKeyUser), AuthSubject{UserID: 1})
		require.True(t, enforceStepUp(c, stubStepUpGrantChecker{granted: true, remaining: remaining},
			stubStepUpUserReader{user: &service.User{ID: 1, TotpEnabled: true}}, stepUpEnabled), "remaining=%s", remaining)
		require.False(t, c.IsAborted())
	}
	// maxAge <= 0 等价于不要求新鲜度。
	for _, maxAge := range []time.Duration{0, -time.Minute} {
		passed, _ := enforceFreshForUser1(t, stubStepUpGrantChecker{granted: true, remaining: time.Second}, maxAge)
		require.True(t, passed, "maxAge=%s", maxAge)
	}
}

// 新鲜度只是在原有门控上再加一道条件：没授权、机器凭据、功能开关关闭等分支原样。
func TestEnforceStepUpWithinKeepsEveryExistingGateBranch(t *testing.T) {
	// 没有授权。
	passed, rec := enforceFreshForUser1(t, stubStepUpGrantChecker{granted: false}, freshTestWindow)
	require.False(t, passed)
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Contains(t, rec.Body.String(), "STEP_UP_REQUIRED")

	// 机器凭据一律拒绝。
	c, rec := newStepUpTestContext(t)
	c.Set("auth_method", service.AuditAuthMethodAdminAPIKey)
	require.False(t, enforceStepUpWithin(c, stubStepUpGrantChecker{granted: true},
		stubStepUpUserReader{user: &service.User{ID: 1, TotpEnabled: true}}, stepUpEnabled, freshTestWindow))
	require.Contains(t, rec.Body.String(), "STEP_UP_ADMIN_API_KEY_FORBIDDEN")

	// 全站开关关闭：与门控引入前一致，直接放行（新鲜度同样不再要求）。
	c, _ = newStepUpTestContext(t)
	require.True(t, enforceStepUpWithin(c, stubStepUpGrantChecker{granted: true, remaining: time.Second},
		stubStepUpUserReader{user: &service.User{ID: 1, TotpEnabled: true}}, stubStepUpSettingReader{enabled: false}, freshTestWindow))
}

// 每个已绑定因子的授权都必须新鲜：一个因子刚验证、另一个已经陈旧同样不放行。
func TestEnforceStepUpWithinRequiresEveryBoundFactorToBeFresh(t *testing.T) {
	grants := factorAgedGrants{
		service.FactorTOTP:    service.StepUpGrantTTL - 10*time.Second,
		service.FactorPasskey: service.StepUpGrantTTL - 3*time.Minute,
	}
	passed, rec := enforceFreshForUser1(t, grants, freshTestWindow)
	require.False(t, passed)
	require.Contains(t, rec.Body.String(), "STEP_UP_REQUIRED")

	// 两个因子都刚验证 → 放行。
	grants[service.FactorPasskey] = service.StepUpGrantTTL - 20*time.Second
	passed, _ = enforceFreshForUser1(t, grants, freshTestWindow)
	require.True(t, passed)
}

// factorAgedGrants 按因子给出剩余有效期，并声明用户绑定了 TOTP 与 Passkey 两个因子。
type factorAgedGrants map[service.Factor]time.Duration

func (g factorAgedGrants) HasStepUpGrant(_ context.Context, _ int64, _ string, factor service.Factor) (bool, error) {
	_, ok := g[factor]
	return ok, nil
}

func (g factorAgedGrants) StepUpGrantRemaining(_ context.Context, _ int64, _ string, factor service.Factor) (time.Duration, bool, error) {
	remaining, ok := g[factor]
	return remaining, ok, nil
}

func (g factorAgedGrants) RequiredFactors(context.Context, int64) ([]service.Factor, error) {
	return []service.Factor{service.FactorTOTP, service.FactorPasskey}, nil
}
