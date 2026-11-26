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

// 多因子（任选其一）：剩余时间异常长的授权无法证明自己新鲜，这个因子不算数；
// 另一个因子有新鲜授权时仍然放行，所有因子的授权都异常（或其余因子没有授权）才拒绝。
func TestEnforceStepUpWithinAnOverlongGrantNeverSatisfiesButAFreshOtherFactorStillDoes(t *testing.T) {
	grants := factorAgedGrants{
		service.FactorTOTP:    service.StepUpGrantTTL - 10*time.Second,
		service.FactorPasskey: 14 * time.Minute,
	}
	passed, _ := enforceFreshForUser1(t, grants, freshTestWindow)
	require.True(t, passed, "TOTP 授权新鲜，Passkey 授权异常长：只靠 TOTP 放行")

	grants = factorAgedGrants{
		service.FactorTOTP:    24 * time.Hour,
		service.FactorPasskey: service.StepUpGrantTTL - 10*time.Second,
	}
	passed, _ = enforceFreshForUser1(t, grants, freshTestWindow)
	require.True(t, passed, "反过来：只靠 Passkey 放行")

	// 唯一的授权异常长 → 拒绝（异常授权永远不能自己满足 step-up）。
	for _, grants := range []factorAgedGrants{
		{service.FactorPasskey: 14 * time.Minute},
		{service.FactorTOTP: 14 * time.Minute, service.FactorPasskey: 24 * time.Hour},
	} {
		passed, rec := enforceFreshForUser1(t, grants, freshTestWindow)
		require.False(t, passed, "grants=%v", grants)
		require.Equal(t, http.StatusForbidden, rec.Code)
		require.Contains(t, rec.Body.String(), "STEP_UP_REQUIRED")
	}
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

// 任选其一：任一已绑定因子有新鲜授权就放行；陈旧授权（窗口内但超过 maxAge）不算数，
// 所有因子的授权都陈旧才拒绝，并且提示「需要新鲜验证」让前端当场重新验证。
func TestEnforceStepUpWithinAcceptsAnyBoundFactorWithAFreshGrant(t *testing.T) {
	// 一个刚验证、另一个已经陈旧：靠刚验证的那个放行（两个方向都测）。
	for _, grants := range []factorAgedGrants{
		{service.FactorTOTP: service.StepUpGrantTTL - 10*time.Second, service.FactorPasskey: service.StepUpGrantTTL - 3*time.Minute},
		{service.FactorTOTP: service.StepUpGrantTTL - 3*time.Minute, service.FactorPasskey: service.StepUpGrantTTL - 10*time.Second},
	} {
		passed, _ := enforceFreshForUser1(t, grants, freshTestWindow)
		require.True(t, passed, "grants=%v", grants)
	}

	// 两个因子都刚验证 → 放行。
	passed, _ := enforceFreshForUser1(t, factorAgedGrants{
		service.FactorTOTP:    service.StepUpGrantTTL - 10*time.Second,
		service.FactorPasskey: service.StepUpGrantTTL - 20*time.Second,
	}, freshTestWindow)
	require.True(t, passed)

	// 两个因子都陈旧（窗口内、但都超过 60 秒）→ 拒绝，提示需要新鲜验证。
	passed, rec := enforceFreshForUser1(t, factorAgedGrants{
		service.FactorTOTP:    service.StepUpGrantTTL - 2*time.Minute,
		service.FactorPasskey: service.StepUpGrantTTL - 3*time.Minute,
	}, freshTestWindow)
	require.False(t, passed)
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Contains(t, rec.Body.String(), "STEP_UP_REQUIRED")
	require.Contains(t, rec.Body.String(), "fresh")

	// 只有一个因子有授权且陈旧（另一个根本没有）→ 同样拒绝。
	passed, _ = enforceFreshForUser1(t, factorAgedGrants{service.FactorPasskey: service.StepUpGrantTTL - 3*time.Minute}, freshTestWindow)
	require.False(t, passed)
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
