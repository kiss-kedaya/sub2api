package middleware

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/stretchr/testify/require"
)

// windowRecordingAllower 按 key 计数并记录每个 key 用过的 limit/window，
// 用来钉住「每分钟 3 次 + 每小时 20 次」两档的 key、上限与窗口。
type windowRecordingAllower struct {
	mu      sync.Mutex
	counts  map[string]int64
	limits  map[string]int
	windows map[string]time.Duration
	err     error
}

func (a *windowRecordingAllower) Allow(_ context.Context, key string, limit int, window time.Duration) (middleware.AllowResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.err != nil {
		return middleware.AllowResult{}, a.err
	}
	if a.counts == nil {
		a.counts, a.limits, a.windows = map[string]int64{}, map[string]int{}, map[string]time.Duration{}
	}
	a.counts[key]++
	a.limits[key], a.windows[key] = limit, window
	result := middleware.AllowResult{Allowed: a.counts[key] <= int64(limit), Count: a.counts[key]}
	if !result.Allowed {
		result.RetryAfter = window
	}
	return result, nil
}

// 每个管理员每分钟 3 次：第 4 次 429，且别的管理员不受影响。
func TestAdmin2FAResetLimitsEachAdminToThreePerMinute(t *testing.T) {
	allower := &windowRecordingAllower{}
	p := &PanelRateLimiter{limiter: allower}
	adminA := newPanelTestRouter(p.Admin2FAReset(), &panelTestIdentity{userID: 1, role: service.RoleAdmin})
	adminB := newPanelTestRouter(p.Admin2FAReset(), &panelTestIdentity{userID: 2, role: service.RoleAdmin})

	for i := 0; i < admin2FAResetPerMinute; i++ {
		require.Equal(t, http.StatusOK, performPanelRequest(adminA, "127.0.0.1:1000").Code, "request %d", i+1)
	}
	rec := performPanelRequest(adminA, "127.0.0.1:1000")
	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	require.Contains(t, rec.Body.String(), "RATE_LIMITED")
	require.NotEmpty(t, rec.Header().Get("Retry-After"))
	require.Equal(t, http.StatusOK, performPanelRequest(adminB, "127.0.0.1:1000").Code)

	allower.mu.Lock()
	defer allower.mu.Unlock()
	require.Equal(t, 3, allower.limits["panel:admin_2fa_reset:user:1"])
	require.Equal(t, time.Minute, allower.windows["panel:admin_2fa_reset:user:1"])
}

// 每小时 20 次：分钟档不触发时（每次都换一个新的分钟窗口），第 21 次仍被小时档拦住。
func TestAdmin2FAResetLimitsEachAdminToTwentyPerHour(t *testing.T) {
	allower := &windowRecordingAllower{}
	p := &PanelRateLimiter{limiter: allower}
	router := newPanelTestRouter(p.Admin2FAReset(), &panelTestIdentity{userID: 1, role: service.RoleAdmin})

	for i := 0; i < admin2FAResetPerHour; i++ {
		allower.mu.Lock()
		delete(allower.counts, "panel:admin_2fa_reset:user:1") // 分钟窗口翻页
		allower.mu.Unlock()
		require.Equal(t, http.StatusOK, performPanelRequest(router, "127.0.0.1:1000").Code, "request %d", i+1)
	}
	allower.mu.Lock()
	delete(allower.counts, "panel:admin_2fa_reset:user:1")
	allower.mu.Unlock()
	rec := performPanelRequest(router, "127.0.0.1:1000")
	require.Equal(t, http.StatusTooManyRequests, rec.Code, "the hourly tier must stop the 21st request")

	allower.mu.Lock()
	defer allower.mu.Unlock()
	require.Equal(t, 20, allower.limits["panel:admin_2fa_reset_hourly:user:1"])
	require.Equal(t, time.Hour, allower.windows["panel:admin_2fa_reset_hourly:user:1"])
}

// 被分钟档拦住的请求不消耗小时额度。
func TestAdmin2FAResetMinuteRejectionDoesNotSpendTheHourlyBudget(t *testing.T) {
	allower := &windowRecordingAllower{}
	p := &PanelRateLimiter{limiter: allower}
	router := newPanelTestRouter(p.Admin2FAReset(), &panelTestIdentity{userID: 1, role: service.RoleAdmin})

	for i := 0; i < 10; i++ {
		performPanelRequest(router, "127.0.0.1:1000")
	}
	allower.mu.Lock()
	defer allower.mu.Unlock()
	require.EqualValues(t, 3, allower.counts["panel:admin_2fa_reset_hourly:user:1"])
}

// 它是固定护栏：不受面板限流总开关/管理员豁免影响；Redis 出错、没有限流器、没有认证主体都放行。
func TestAdmin2FAResetIsAFixedGuardrailThatFailsOpen(t *testing.T) {
	// 面板限流总开关关闭 + 管理员豁免：仍然限流。
	p := &PanelRateLimiter{
		limiter:        &windowRecordingAllower{},
		settingService: newPanelRateLimitTestService(t, `{"enabled":false,"user_rpm":1000,"heavy_rpm":1000,"exempt_admin":true,"public_ip_rpm":0}`),
	}
	router := newPanelTestRouter(p.Admin2FAReset(), &panelTestIdentity{userID: 1, role: service.RoleAdmin})
	for i := 0; i < admin2FAResetPerMinute; i++ {
		require.Equal(t, http.StatusOK, performPanelRequest(router, "127.0.0.1:1000").Code)
	}
	require.Equal(t, http.StatusTooManyRequests, performPanelRequest(router, "127.0.0.1:1000").Code)

	// Redis 出错：fail-open。
	failing := &PanelRateLimiter{limiter: &windowRecordingAllower{err: errors.New("redis down")}}
	failOpen := newPanelTestRouter(failing.Admin2FAReset(), &panelTestIdentity{userID: 1, role: service.RoleAdmin})
	for i := 0; i < 5; i++ {
		require.Equal(t, http.StatusOK, performPanelRequest(failOpen, "127.0.0.1:1000").Code)
	}

	// nil 限流器（测试装配注入 nil）与没有认证主体：直接放行。
	var nilLimiter *PanelRateLimiter
	nilRouter := newPanelTestRouter(nilLimiter.Admin2FAReset(), &panelTestIdentity{userID: 1, role: service.RoleAdmin})
	for i := 0; i < 5; i++ {
		require.Equal(t, http.StatusOK, performPanelRequest(nilRouter, "127.0.0.1:1000").Code)
	}
	anonymous := newPanelTestRouter((&PanelRateLimiter{limiter: &windowRecordingAllower{}}).Admin2FAReset(), nil)
	for i := 0; i < 5; i++ {
		require.Equal(t, http.StatusOK, performPanelRequest(anonymous, "127.0.0.1:1000").Code)
	}
}

// fixedUserTier 抽出公共判定函数后，既有的固定档（API Key 封存读取等）行为不变：
// 每用户每分钟 20 次，key 命名不变，第 21 次 429。
func TestFixedUserTierBehaviourIsUnchangedByTheSharedAllowHelper(t *testing.T) {
	allower := &windowRecordingAllower{}
	p := &PanelRateLimiter{limiter: allower}
	router := newPanelTestRouter(p.SecretSealRead(), &panelTestIdentity{userID: 7, role: service.RoleUser})

	for i := 0; i < apiKeySecretReadRPM; i++ {
		require.Equal(t, http.StatusOK, performPanelRequest(router, "127.0.0.1:1000").Code, "request %d", i+1)
	}
	require.Equal(t, http.StatusTooManyRequests, performPanelRequest(router, "127.0.0.1:1000").Code)

	allower.mu.Lock()
	defer allower.mu.Unlock()
	require.Equal(t, apiKeySecretReadRPM, allower.limits["panel:api_key_secret:user:7"])
	require.Equal(t, time.Minute, allower.windows["panel:api_key_secret:user:7"])
}
