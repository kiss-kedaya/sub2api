//go:build unit

package handler

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/testutil"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// stormUpstream 是「永远 502 的中转站」：按账号返回固定状态码，并像真实传输层那样把每一次尝试
// 通知给观察者（真实传输层的观察点由 repository 包的测试单独钉住）。
type stormUpstream struct {
	inner *grokCredentialHandlerUpstream
	// delay 模拟上游每次尝试都很慢（用来验证总耗时上限）。
	delay time.Duration

	mu       sync.Mutex
	observer service.UpstreamAttemptObserver
}

func (u *stormUpstream) SetUpstreamAttemptObserver(observer service.UpstreamAttemptObserver) {
	u.mu.Lock()
	u.observer = observer
	u.mu.Unlock()
}

func (u *stormUpstream) Do(req *http.Request, proxyURL string, accountID int64, concurrency int) (*http.Response, error) {
	if u.delay > 0 {
		time.Sleep(u.delay)
	}
	resp, err := u.inner.Do(req, proxyURL, accountID, concurrency)
	u.mu.Lock()
	observer := u.observer
	u.mu.Unlock()
	if observer != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		observer.ObserveUpstreamAttempt(req.Context(), accountID, status, err)
	}
	return resp, err
}

func (u *stormUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, accountID, concurrency)
}

func (u *stormUpstream) totalHits() int { return len(u.inner.accountHits()) }

type stormFixture struct {
	router   *gin.Engine
	upstream *stormUpstream
	repo     *stormRepo
	cleanup  func()
}

// stormRepo 在共用的账号仓库替身上补上「没有可用账号」分类要用的模型可用性诊断查询
// （生产里由真实仓库提供）。
type stormRepo struct {
	*grokCredentialHandlerRepo
}

func (r *stormRepo) ListModelAvailabilityCandidates(_ context.Context, _ *int64, platforms []string, _ bool) ([]service.Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []service.Account
	for _, account := range r.accounts {
		for _, platform := range platforms {
			if account.Platform == platform {
				out = append(out, account)
			}
		}
	}
	return out, nil
}

// newStormFixture 搭一个真实的 OpenAI 网关处理链（选号 -> 转发 -> 失败转移 -> 错误响应），
// 两个池模式账号，上游永远 502。breakerEnabled 控制账号错误率断路器。
func newStormFixture(t *testing.T, breakerEnabled bool) *stormFixture {
	t.Helper()
	return newStormFixtureWith(t, stormOptions{breakerEnabled: breakerEnabled, accounts: 2})
}

type stormOptions struct {
	breakerEnabled bool
	accounts       int
	attemptDelay   time.Duration
	guard          config.GatewayRetryGuardConfig
	// cooldownSeconds 是断路器冷却秒数；默认 30（远长于测试时长，避免慢机器上账号中途恢复）。
	cooldownSeconds int
}

func newStormFixtureWith(t *testing.T, opts stormOptions) *stormFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	breakerEnabled := opts.breakerEnabled
	groupID := int64(901)
	poolCredentials := func(key string) map[string]any {
		return map[string]any{
			"api_key": key, "base_url": "https://relay.invalid",
			"pool_mode": true, "pool_mode_retry_count": 3,
			"pool_mode_retry_status_codes": []any{float64(502)},
		}
	}
	accounts := make([]service.Account, 0, opts.accounts)
	failing := map[int64]int{}
	for i := 1; i <= opts.accounts; i++ {
		accounts = append(accounts, service.Account{
			ID: int64(i), Name: "relay-" + strconv.Itoa(i), Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
			Status: service.StatusActive, Schedulable: true, Concurrency: 50, Priority: i, Credentials: poolCredentials("key-" + strconv.Itoa(i)),
		})
		failing[int64(i)] = http.StatusBadGateway
	}
	repo := &stormRepo{&grokCredentialHandlerRepo{accounts: accounts, missingOnGet: map[int64]bool{}}}
	upstream := &stormUpstream{inner: &grokCredentialHandlerUpstream{failureStatus: failing}, delay: opts.attemptDelay}

	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Gateway.MaxAccountSwitches = 3
	cooldown := opts.cooldownSeconds
	if cooldown <= 0 {
		cooldown = 30
	}
	cfg.Gateway.AccountCircuitBreaker = config.AccountCircuitBreakerConfig{
		Enabled: breakerEnabled, WindowSeconds: 300, ErrorRate: 0.95, MinRequests: 20, CooldownSeconds: cooldown,
		ValveMinAccounts: 3,
	}
	// 这个用例反复打满重试；把退避压到毫秒级，不然光睡眠就要几十秒。
	cfg.Gateway.RetryGuard = config.GatewayRetryGuardConfig{BackoffBaseMs: 1, BackoffMaxMs: 2}
	if opts.guard.MaxAttempts > 0 {
		cfg.Gateway.RetryGuard.MaxAttempts = opts.guard.MaxAttempts
	}
	if opts.guard.MaxElapsedSeconds > 0 {
		cfg.Gateway.RetryGuard.MaxElapsedSeconds = opts.guard.MaxElapsedSeconds
	}

	rateLimit := service.ProvideRateLimitService(repo, nil, cfg, nil, nil, nil, nil, nil, nil, nil, upstream)
	rateLimit.AccountCircuitBreaker().RunPersistSynchronouslyForTest() // nil-safe：断路器关闭时什么都不做
	billingCache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	gateway := service.NewOpenAIGatewayService(
		repo, nil, nil, nil, nil, nil, testutil.StubGatewayCache{}, cfg, nil, nil,
		service.NewBillingService(cfg, nil), rateLimit, billingCache, upstream,
		&service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil,
	)
	cache := &concurrencyCacheMock{
		acquireUserSlotFn:    func(context.Context, int64, int, string) (bool, error) { return true, nil },
		acquireAccountSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil },
	}
	h := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(cache), billingCache, &service.APIKeyService{}, nil, nil, nil, nil, cfg)
	apiKey := &service.APIKey{
		ID: 902, GroupID: &groupID,
		User:  &service.User{ID: 903, Status: service.StatusActive},
		Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI, Status: service.StatusActive, Hydrated: true, AllowImageGeneration: true, CrossProtocolConversionEnabled: true, AllowMessagesDispatch: true},
	}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyAPIKey), apiKey)
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: apiKey.User.ID, Concurrency: 100})
		// 与 API Key 鉴权中间件一致：网关请求 ctx 里带用户 ID（断路器只统计真实网关流量）。
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), ctxkey.UserID, apiKey.User.ID))
		c.Next()
	})
	router.POST("/openai/v1/responses", h.Responses)
	router.POST("/openai/v1/chat/completions", h.ChatCompletions)
	router.POST("/openai/v1/messages", h.Messages)
	router.POST("/openai/v1/embeddings", h.Embeddings)
	router.POST("/openai/v1/alpha/search", h.AlphaSearch)
	router.POST("/openai/v1/images/generations", h.Images)
	t.Cleanup(func() {
		billingCache.Stop()
		ConfigureRetryGuard(nil)
	})
	return &stormFixture{router: router, upstream: upstream, repo: repo, cleanup: billingCache.Stop}
}

func (f *stormFixture) send() *httptest.ResponseRecorder {
	return f.sendTo("/openai/v1/responses", `{"model":"gpt-5","input":"hello","stream":false}`)
}

func (f *stormFixture) sendTo(path, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	f.router.ServeHTTP(rec, req)
	return rec
}

func (f *stormFixture) tempUnschedCalls() int {
	f.repo.mu.Lock()
	defer f.repo.mu.Unlock()
	return len(f.repo.setTempIDs)
}

// 对照组：没有断路器时，客户端每发一个请求，上游就被打 2~3 次，永远不会停。
func TestRetryStormWithoutBreakerKeepsHammeringTheUpstream(t *testing.T) {
	f := newStormFixture(t, false)
	for i := 0; i < 100; i++ {
		rec := f.send()
		require.Equal(t, http.StatusBadGateway, rec.Code, rec.Body.String())
	}
	hits := f.upstream.totalHits()
	require.GreaterOrEqual(t, hits, 200, "without the breaker every client request is amplified into >=2 upstream attempts, forever")
	require.Zero(t, f.tempUnschedCalls())
}

func TestRetryStormWithBreakerIsBoundedPausesAccountsAndProbesAfterCooldown(t *testing.T) {
	f := newStormFixtureWith(t, stormOptions{breakerEnabled: true, accounts: 2, cooldownSeconds: 3})
	const requests = 100

	var statuses []int
	var sawRetryAfter bool
	hitsAfter := make([]int, 0, requests)
	for i := 0; i < requests; i++ {
		rec := f.send()
		statuses = append(statuses, rec.Code)
		if rec.Code == http.StatusServiceUnavailable {
			if ra := rec.Header().Get("Retry-After"); ra != "" {
				seconds, err := strconv.Atoi(ra)
				require.NoError(t, err)
				require.GreaterOrEqual(t, seconds, 5)
				require.LessOrEqual(t, seconds, 30)
				sawRetryAfter = true
			}
		}
		hitsAfter = append(hitsAfter, f.upstream.totalHits())
	}

	// 上游被打的总次数有上界：每个账号最多攒到 min_requests(20) 次失败就被摘掉，
	// 再加上熔断那一刻还在途的少量尝试；而不是 100 个请求 x 2~3 次。
	totalHits := hitsAfter[requests-1]
	require.LessOrEqual(t, totalHits, 2*(20+6), "upstream attempts must be bounded once both accounts are paused")
	require.GreaterOrEqual(t, totalHits, 40, "the breaker needs min_requests samples per account before it pauses it")

	// 熔断后不再被选中：后半段请求不再产生任何上游尝试。
	require.Equal(t, hitsAfter[requests/2], hitsAfter[requests-1], "no upstream attempts after both accounts are paused")
	require.Equal(t, 2, f.tempUnschedCalls(), "one DB write per account per cooldown, not one per request")
	require.Equal(t, http.StatusServiceUnavailable, statuses[requests-1], "paused accounts: the gateway answers 'no available accounts' itself")
	require.True(t, sawRetryAfter, "the gateway's own 503 must tell SDKs to back off with Retry-After")

	for _, id := range []int64{1, 2} {
		acc, err := f.repo.GetByID(context.Background(), id)
		require.NoError(t, err)
		require.NotNil(t, acc.TempUnschedulableUntil)
		require.True(t, acc.Schedulable, "the manual schedulable flag is untouched")
	}

	// 冷却到期（3 秒）后自动恢复：账号重新参与调度，用少量请求去探测上游。
	time.Sleep(3300 * time.Millisecond)
	before := f.upstream.totalHits()
	rec := f.send()
	require.Equal(t, http.StatusBadGateway, rec.Code, "after the cooldown the account is probed again (upstream still down: 502 passthrough)")
	require.Greater(t, f.upstream.totalHits(), before, "recovery probes reach the upstream")
	for i := 0; i < 60; i++ {
		f.send()
	}
	require.LessOrEqual(t, f.upstream.totalHits()-before, 2*(20+6), "the second wave is bounded as well: fresh window needs min_requests new failures before re-pausing")
	require.GreaterOrEqual(t, f.tempUnschedCalls(), 4, "both accounts were paused again after the fresh window filled up")
}

func TestRetryStormWithConcurrentClientsIsBounded(t *testing.T) {
	f := newStormFixture(t, true)
	const workers, perWorker = 8, 25
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				f.send()
			}
		}()
	}
	wg.Wait()
	hits := f.upstream.totalHits()
	// 并发时每个账号熔断前最多多出 workers 个在途请求 x 每个请求的几次尝试。
	require.LessOrEqual(t, hits, 2*(20+workers*3), "200 concurrent client requests must not become 200 x attempts upstream hits")
	require.Equal(t, 2, f.tempUnschedCalls(), "concurrent failures are deduplicated into one write per account")
}

// 一个请求的总尝试次数有硬上限：30 个账号全部 502，没有上限时一个请求会把整个组逐个打一遍
// （再加选号层的兜底重试）；现在最多 10 次，然后把最后一次上游错误交给客户端。
func TestSingleRequestUpstreamAttemptsAreCapped(t *testing.T) {
	f := newStormFixtureWith(t, stormOptions{breakerEnabled: false, accounts: 30})

	rec := f.send()

	require.Equal(t, http.StatusBadGateway, rec.Code, "the last upstream error is returned to the client")
	require.Equal(t, 10, f.upstream.totalHits(), "one client request must make at most max_attempts upstream attempts")
}

func TestSingleRequestAttemptCapIsConfigurable(t *testing.T) {
	f := newStormFixtureWith(t, stormOptions{breakerEnabled: false, accounts: 30, guard: config.GatewayRetryGuardConfig{MaxAttempts: 4}})
	f.send()
	require.Equal(t, 4, f.upstream.totalHits())
}

// 总耗时上限：每次尝试 400ms，上限 1 秒 -> 第 3 次失败时已经过了 1.2 秒，不再发起新的尝试。
func TestSingleRequestElapsedBudgetStopsRetries(t *testing.T) {
	f := newStormFixtureWith(t, stormOptions{
		breakerEnabled: false, accounts: 30, attemptDelay: 400 * time.Millisecond,
		guard: config.GatewayRetryGuardConfig{MaxElapsedSeconds: 1},
	})
	start := time.Now()

	rec := f.send()

	require.Equal(t, http.StatusBadGateway, rec.Code)
	hits := f.upstream.totalHits()
	// 400ms 一次、预算 1 秒：理论上第 3 次失败后停（慢机器上可能第 2 次就已过线）；没有时限的话会一直打到 10 次。
	require.GreaterOrEqual(t, hits, 2)
	require.LessOrEqual(t, hits, 4, "attempts stop once the request has spent max_elapsed_seconds in the failover loop")
	require.Less(t, time.Since(start), 6*time.Second)
}

// 每一个 OpenAI 系入口都有自己的失败转移循环：它们必须全部受同一个重试硬上限约束。
func TestEveryOpenAIFailoverLoopHonorsTheAttemptCap(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
		body string
	}{
		{"responses", "/openai/v1/responses", `{"model":"gpt-5","input":"hello","stream":false}`},
		{"chat completions", "/openai/v1/chat/completions", `{"model":"gpt-5","messages":[{"role":"user","content":"hello"}],"stream":false}`},
		{"messages", "/openai/v1/messages", `{"model":"gpt-5","max_tokens":16,"messages":[{"role":"user","content":"hello"}],"stream":false}`},
		{"embeddings", "/openai/v1/embeddings", `{"model":"text-embedding-3-small","input":"hello"}`},
		{"images", "/openai/v1/images/generations", `{"model":"gpt-image-1","prompt":"a cat"}`},
		{"alpha search", "/openai/v1/alpha/search", `{"model":"gpt-5","query":"hello"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newStormFixtureWith(t, stormOptions{breakerEnabled: false, accounts: 30})
			rec := f.sendTo(tc.path, tc.body)
			hits := f.upstream.totalHits()
			t.Logf("%s -> status %d, upstream hits %d", tc.name, rec.Code, hits)
			require.Greater(t, hits, 0, "the request must actually reach the failing upstream: %s", rec.Body.String())
			require.LessOrEqual(t, hits, 10, "one client request must make at most max_attempts upstream attempts")
		})
	}
}
