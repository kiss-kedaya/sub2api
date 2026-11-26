//go:build unit

package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// breakerSchedRepo 在 mockAccountRepoForPlatform 之上实现 SetTempUnschedulable：
// 语义与真实 SQL 一致（until 只会变大，不会缩短；只写 temp_unschedulable_*，不碰 schedulable/status）。
type breakerSchedRepo struct {
	mockAccountRepoForPlatform
	mu    sync.Mutex
	calls []breakerSchedCall
}

type breakerSchedCall struct {
	id     int64
	until  time.Time
	reason string
}

func (r *breakerSchedRepo) SetTempUnschedulable(_ context.Context, id int64, until time.Time, reason string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, breakerSchedCall{id: id, until: until, reason: reason})
	acc := r.accountsByID[id]
	if acc == nil {
		return nil
	}
	if acc.TempUnschedulableUntil == nil || acc.TempUnschedulableUntil.Before(until) {
		u := until
		acc.TempUnschedulableUntil = &u
		acc.TempUnschedulableReason = reason
	}
	return nil
}

func (r *breakerSchedRepo) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func newBreakerSchedFixture(t *testing.T, cooldown time.Duration) (*breakerSchedRepo, *RateLimitService, *AccountCircuitBreaker, *GatewayService, *mockGatewayCacheForPlatform) {
	t.Helper()
	repo := &breakerSchedRepo{mockAccountRepoForPlatform: mockAccountRepoForPlatform{
		accounts: []Account{
			{ID: 1, Platform: PlatformAnthropic, Priority: 1, Status: StatusActive, Schedulable: true},
			{ID: 2, Platform: PlatformAnthropic, Priority: 2, Status: StatusActive, Schedulable: true},
			{ID: 3, Platform: PlatformAnthropic, Priority: 3, Status: StatusActive, Schedulable: false}, // 手动停用
		},
		accountsByID: map[int64]*Account{},
	}}
	for i := range repo.accounts {
		repo.accountsByID[repo.accounts[i].ID] = &repo.accounts[i]
	}
	rl := NewRateLimitService(repo, nil, testConfig(), nil, nil)
	settings := DefaultAccountCircuitBreakerSettings()
	settings.Cooldown = cooldown
	breaker := NewAccountCircuitBreaker(settings, rl.persistAccountCircuitTrip)
	breaker.async = func(f func()) { f() }
	breaker.log = &circuitLogRecorder{}
	rl.SetAccountCircuitBreaker(breaker)
	cache := &mockGatewayCacheForPlatform{sessionBindings: map[string]int64{}}
	svc := &GatewayService{accountRepo: repo, cache: cache, cfg: testConfig(), rateLimitService: rl}
	return repo, rl, breaker, svc, cache
}

func tripAccount(breaker *AccountCircuitBreaker, id int64) {
	ctx := circuitCtx(PlatformAnthropic, "acct")
	for i := 0; i < 20; i++ {
		breaker.ObserveUpstreamAttempt(ctx, id, 502, nil)
	}
}

func TestCircuitBreakerTripIsHonoredBySchedulerStickySessionsAndRecoversAutomatically(t *testing.T) {
	repo, _, breaker, svc, cache := newBreakerSchedFixture(t, 400*time.Millisecond)
	ctx := context.Background()
	const model = "claude-3-5-sonnet-20241022"

	// 熔断前：优先级最高的账号 1 被选中，粘连会话也命中它。
	cache.sessionBindings["session-123"] = 1
	acc, err := svc.selectAccountForModelWithPlatform(ctx, nil, "session-123", model, nil, PlatformAnthropic)
	require.NoError(t, err)
	require.Equal(t, int64(1), acc.ID)

	// 账号 1 的上游全 502：窗口里攒够 20 个样本且失败率 100% -> 熔断，写成临时不可调度。
	tripAccount(breaker, 1)
	require.Equal(t, 1, repo.callCount())
	require.Equal(t, int64(1), repo.calls[0].id)
	require.True(t, IsAccountCircuitBreakerReason(repo.calls[0].reason))
	require.Contains(t, repo.accountsByID[1].TempUnschedulableReason, "circuit_breaker:")
	require.True(t, repo.accountsByID[1].Schedulable, "the breaker only writes temp_unschedulable_*; the manual schedulable flag is untouched")
	require.Equal(t, StatusActive, repo.accountsByID[1].Status)
	require.True(t, svc.AccountCircuitOpen(1))
	require.False(t, svc.AccountCircuitOpen(2))

	// 调度选号认它：账号 1 不再被选中；粘连会话被清掉，改选账号 2。
	acc, err = svc.selectAccountForModelWithPlatform(ctx, nil, "", model, nil, PlatformAnthropic)
	require.NoError(t, err)
	require.Equal(t, int64(2), acc.ID)
	acc, err = svc.selectAccountForModelWithPlatform(ctx, nil, "session-123", model, nil, PlatformAnthropic)
	require.NoError(t, err)
	require.Equal(t, int64(2), acc.ID, "a session bound to the tripped account must move to a healthy one")
	require.Greater(t, cache.deletedSessions["session-123"], 0, "the stale sticky binding is cleared")

	// 手动停用的账号 3 不会被当作后备（熔断不会把它「救活」）。
	acc, err = svc.selectAccountForModelWithPlatform(ctx, nil, "", model, map[int64]struct{}{2: {}}, PlatformAnthropic)
	require.Error(t, err, "only the tripped (1), excluded (2) and manually disabled (3) accounts remain: nothing to select")
	require.Nil(t, acc)

	// 冷却到期自动恢复：不需要任何人工操作，账号 1 重新按优先级被选中。
	time.Sleep(450 * time.Millisecond)
	require.False(t, svc.AccountCircuitOpen(1))
	acc, err = svc.selectAccountForModelWithPlatform(ctx, nil, "", model, nil, PlatformAnthropic)
	require.NoError(t, err)
	require.Equal(t, int64(1), acc.ID, "cooldown elapsed: the account is schedulable again")

	// 恢复后需要重新积累新样本才可能再次熔断：19 个失败不够。
	ctxObs := circuitCtx(PlatformAnthropic, "acct")
	for i := 0; i < 19; i++ {
		breaker.ObserveUpstreamAttempt(ctxObs, 1, 502, nil)
	}
	require.Equal(t, 1, repo.callCount(), "no re-trip from stale data")
	breaker.ObserveUpstreamAttempt(ctxObs, 1, 502, nil)
	require.Equal(t, 2, repo.callCount(), "re-trips after a fresh window of failing attempts")
}

func TestCircuitBreakerNeverShortensALongerExistingCooldown(t *testing.T) {
	repo, _, breaker, _, _ := newBreakerSchedFixture(t, 60*time.Second)
	manual := time.Now().Add(2 * time.Hour)
	repo.accountsByID[2].TempUnschedulableUntil = &manual
	repo.accountsByID[2].TempUnschedulableReason = "operator pause"

	tripAccount(breaker, 2)

	require.Equal(t, 1, repo.callCount(), "the breaker still asks for its 60s pause")
	require.Equal(t, manual, *repo.accountsByID[2].TempUnschedulableUntil, "the existing longer cooldown must win (max, never min)")
	require.Equal(t, "operator pause", repo.accountsByID[2].TempUnschedulableReason)
}

func TestCircuitBreakerResetOnManualClear(t *testing.T) {
	repo, rl, breaker, svc, _ := newBreakerSchedFixture(t, time.Minute)
	tripAccount(breaker, 1)
	require.True(t, svc.AccountCircuitOpen(1))

	// 管理员在后台点「重置临时不可调度」：库里清掉的同时，断路器内存状态也必须清掉，
	// 否则重试循环会继续把一个已经可调度的账号当作「被熔断」。
	require.NoError(t, rl.ClearTempUnschedulable(context.Background(), 1))
	require.False(t, svc.AccountCircuitOpen(1))
	require.Equal(t, 1, repo.callCount())
}

func TestPersistAccountCircuitTripWithoutRepositoryFails(t *testing.T) {
	rl := &RateLimitService{}
	err := rl.persistAccountCircuitTrip(context.Background(), AccountCircuitTrip{AccountID: 1})
	require.ErrorIs(t, err, errCircuitBreakerNoRepo)
}

func TestRateLimitServiceWithoutBreakerBehavesAsBefore(t *testing.T) {
	rl := NewRateLimitService(&mockAccountRepoForPlatform{}, nil, testConfig(), nil, nil)
	require.False(t, rl.AccountCircuitOpen(1))
	require.Nil(t, rl.AccountCircuitBreaker())
	var nilRL *RateLimitService
	require.False(t, nilRL.AccountCircuitOpen(1))
	var nilGateway *GatewayService
	require.False(t, nilGateway.AccountCircuitOpen(1))
	var nilOpenAI *OpenAIGatewayService
	require.False(t, nilOpenAI.AccountCircuitOpen(1))
	require.False(t, (&GatewayService{}).AccountCircuitOpen(1))
	require.NotPanics(t, func() { rl.notifyAccountSchedulingBlockCleared(1) })
}

func TestAttachAccountCircuitBreakerFollowsConfig(t *testing.T) {
	observable := &observableUpstreamStub{}

	// 默认（裸 Config）：未启用，不挂观察者，不创建断路器。
	rl := NewRateLimitService(&mockAccountRepoForPlatform{}, nil, testConfig(), nil, nil)
	attachAccountCircuitBreaker(rl, testConfig(), observable)
	require.Nil(t, rl.AccountCircuitBreaker())
	require.Nil(t, observable.observer)

	// 启用：创建断路器，并注册成上游传输层的观察者。
	cfg := testConfig()
	cfg.Gateway.AccountCircuitBreaker.Enabled = true
	attachAccountCircuitBreaker(rl, cfg, observable)
	require.NotNil(t, rl.AccountCircuitBreaker())
	require.Same(t, rl.AccountCircuitBreaker(), observable.observer)

	// 不支持观察者的 HTTPUpstream 实现（测试替身等）：不报错，断路器照常创建。
	rl2 := NewRateLimitService(&mockAccountRepoForPlatform{}, nil, cfg, nil, nil)
	attachAccountCircuitBreaker(rl2, cfg, nil)
	require.NotNil(t, rl2.AccountCircuitBreaker())
}

type observableUpstreamStub struct {
	HTTPUpstream
	observer UpstreamAttemptObserver
}

func (o *observableUpstreamStub) SetUpstreamAttemptObserver(observer UpstreamAttemptObserver) {
	o.observer = observer
}
