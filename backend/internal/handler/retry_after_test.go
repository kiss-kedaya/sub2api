package handler

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func retryAfterTestContext(path string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, path, nil)
	return c, rec
}

func requireRetryAfterInRange(t *testing.T, rec *httptest.ResponseRecorder, lo, hi int) {
	t.Helper()
	value := rec.Header().Get("Retry-After")
	require.NotEmpty(t, value, "gateway-generated error must carry Retry-After")
	seconds, err := strconv.Atoi(value)
	require.NoError(t, err)
	require.GreaterOrEqual(t, seconds, lo)
	require.LessOrEqual(t, seconds, hi)
}

// withRetryAfterDefaults 用默认配置（Retry-After 开启，5–15 秒）跑测试，并清掉断路器恢复时间提示。
func withRetryAfterDefaults(t *testing.T) {
	t.Helper()
	ConfigureRetryGuard(nil)
	circuitRecoveryHint.Store(nil)
	t.Cleanup(func() {
		ConfigureRetryGuard(nil)
		circuitRecoveryHint.Store(nil)
	})
}

func setCircuitRecoveryIn(t *testing.T, remaining time.Duration, ok bool) {
	t.Helper()
	fn := func() (time.Duration, bool) { return remaining, ok }
	circuitRecoveryHint.Store(&fn)
	t.Cleanup(func() { circuitRecoveryHint.Store(nil) })
}

// ---- 分类 ---------------------------------------------------------------------

func TestClassifyGatewayRetryAfterTable(t *testing.T) {
	const queueMsg = "Too many pending requests, please retry later"
	cases := []struct {
		name    string
		status  int
		errType string
		code    string
		message string
		want    retryAfterKind
	}{
		// 容量类：没有可用账号
		{"no available accounts 503", 503, "api_error", "", "No available accounts for this request. Please retry later.", retryAfterNoCapacity},
		{"scheduling error 503", 503, "scheduling_error", "", "No available accounts", retryAfterNoCapacity},
		{"billing service unavailable 503", 503, "billing_service_error", "", "Billing service temporarily unavailable. Please retry later.", retryAfterNoCapacity},
		{"all accounts rate limited 429 (message)", 429, "rate_limit_error", "", "All available accounts are currently rate-limited. Please retry later.", retryAfterNoCapacity},
		{"all accounts rate limited 429 (prefix)", 429, "rate_limit_error", "", "No available accounts: all available accounts are currently rate-limited. Please retry later.", retryAfterNoCapacity},
		// 用户 / Key 自己的并发与排队上限
		{"queue full", 429, "rate_limit_error", gatewayQueueFullCode, queueMsg, retryAfterUserLimit},
		{"concurrency limit", 429, "rate_limit_error", gatewayConcurrencyLimitCode, "Concurrency limit exceeded for user", retryAfterUserLimit},
		{"api key queue full", 429, "rate_limit_error", apiKeyQueueFullCode, "API key wait queue is full", retryAfterUserLimit},
		{"api key queue timeout", 429, "rate_limit_error", apiKeyQueueTimeoutCode, "Timed out waiting for API key concurrency slot", retryAfterUserLimit},
		{"google style queue full (message only)", 429, "", "", queueMsg, retryAfterUserLimit},
		{"websocket connection cap", 429, "rate_limit_error", "", "Too many open WebSocket connections, please retry later", retryAfterUserLimit},
		// 额度 / 余额 / Key 限流：等多久都不会好
		{"billing rate limit", 429, "rate_limit_exceeded", "", "api key 5h limit reached", retryAfterNone},
		{"key quota exhausted", 429, "API_KEY_QUOTA_EXHAUSTED", "", "api key 额度已用完", retryAfterNone},
		{"insufficient balance", 429, "insufficient_quota", "", "Insufficient account balance", retryAfterNone},
		{"billing code in the code slot (responses sink)", 429, "", "rate_limit_exceeded", "limit reached", retryAfterNone},
		{"quota code in the code slot", 429, "", "insufficient_quota", "no balance", retryAfterNone},
		{"billing code beats a user-limit looking message", 429, "rate_limit_exceeded", "", queueMsg, retryAfterNone},
		// 没认出来的 429 与其它状态码：不带
		{"unknown 429", 429, "", "", "something else", retryAfterNone},
		{"upstream_error 429", 429, "upstream_error", "", "upstream rate limit exceeded", retryAfterNone},
		{"502", 502, "upstream_error", "", "Upstream request failed", retryAfterNone},
		{"500", 500, "api_error", "", "Internal server error", retryAfterNone},
		{"504", 504, "upstream_error", "", "timeout", retryAfterNone},
		{"529", 529, "overloaded_error", "", "overloaded", retryAfterNone},
		{"400", 400, "invalid_request_error", "", "bad", retryAfterNone},
		{"401", 401, "authentication_error", "", "bad key", retryAfterNone},
		{"403", 403, "permission_error", "", "no", retryAfterNone},
		{"404", 404, "not_found_error", "", "no", retryAfterNone},
		{"200", 200, "", "", "", retryAfterNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, classifyGatewayRetryAfter(tc.status, tc.errType, tc.code, tc.message))
		})
	}
}

// ---- 秒数 ---------------------------------------------------------------------

func TestGatewayRetryAfterSecondsPerKind(t *testing.T) {
	withRetryAfterDefaults(t)

	collect := func(kind retryAfterKind, n int) map[int]struct{} {
		seen := map[int]struct{}{}
		for i := 0; i < n; i++ {
			seen[gatewayRetryAfterSeconds(kind)] = struct{}{}
		}
		return seen
	}
	inRange := func(seen map[int]struct{}, lo, hi int) {
		t.Helper()
		for s := range seen {
			require.GreaterOrEqual(t, s, lo)
			require.LessOrEqual(t, s, hi)
		}
	}

	userLimit := collect(retryAfterUserLimit, 400)
	inRange(userLimit, 1, 3)
	require.Len(t, userLimit, 3, "1, 2 and 3 are all used: the hint is jittered")

	budget := collect(retryAfterUpstreamBudget, 400)
	inRange(budget, 5, 10)
	require.Greater(t, len(budget), 3)

	noCapacity := collect(retryAfterNoCapacity, 600)
	inRange(noCapacity, 5, 15)
	require.Greater(t, len(noCapacity), 7, "without breaker information the hint is spread over 5-15s")

	require.Zero(t, gatewayRetryAfterSeconds(retryAfterNone))
}

func TestGatewayRetryAfterForNoCapacityFollowsTheBreakerCooldown(t *testing.T) {
	withRetryAfterDefaults(t)

	t.Run("remaining cooldown plus 1-5s of jitter", func(t *testing.T) {
		setCircuitRecoveryIn(t, 20*time.Second, true)
		seen := map[int]struct{}{}
		for i := 0; i < 400; i++ {
			s := gatewayRetryAfterSeconds(retryAfterNoCapacity)
			require.GreaterOrEqual(t, s, 21)
			require.LessOrEqual(t, s, 25)
			seen[s] = struct{}{}
		}
		require.Len(t, seen, 5)
	})
	t.Run("a fractional remainder is rounded up so the client never comes back early", func(t *testing.T) {
		setCircuitRecoveryIn(t, 20*time.Second+100*time.Millisecond, true)
		for i := 0; i < 200; i++ {
			require.GreaterOrEqual(t, gatewayRetryAfterSeconds(retryAfterNoCapacity), 22)
		}
	})
	t.Run("capped at 30s", func(t *testing.T) {
		setCircuitRecoveryIn(t, 5*time.Minute, true)
		for i := 0; i < 100; i++ {
			require.Equal(t, 30, gatewayRetryAfterSeconds(retryAfterNoCapacity))
		}
	})
	t.Run("no paused accounts: the configured 5-15s range", func(t *testing.T) {
		setCircuitRecoveryIn(t, 0, false)
		for i := 0; i < 200; i++ {
			s := gatewayRetryAfterSeconds(retryAfterNoCapacity)
			require.GreaterOrEqual(t, s, 5)
			require.LessOrEqual(t, s, 15)
		}
	})
	t.Run("the other kinds ignore the breaker", func(t *testing.T) {
		setCircuitRecoveryIn(t, 20*time.Second, true)
		for i := 0; i < 100; i++ {
			require.LessOrEqual(t, gatewayRetryAfterSeconds(retryAfterUserLimit), 3)
			require.LessOrEqual(t, gatewayRetryAfterSeconds(retryAfterUpstreamBudget), 10)
		}
	})
	t.Run("the configured range is honored when there is no cooldown information", func(t *testing.T) {
		cfg := &config.Config{}
		cfg.Gateway.RetryGuard = config.GatewayRetryGuardConfig{RetryAfterEnabled: true, RetryAfterMinSeconds: 7, RetryAfterMaxSeconds: 7}
		ConfigureRetryGuard(cfg)
		for i := 0; i < 20; i++ {
			require.Equal(t, 7, gatewayRetryAfterSeconds(retryAfterNoCapacity))
		}
	})
}

func TestRegisterCircuitRecoveryHint(t *testing.T) {
	withRetryAfterDefaults(t)
	_, ok := circuitRecoveryIn()
	require.False(t, ok, "nothing registered")

	registerCircuitRecoveryHint(struct{}{}) // 不满足接口：忽略
	_, ok = circuitRecoveryIn()
	require.False(t, ok)

	registerCircuitRecoveryHint(&recoveryHinterStub{in: 9 * time.Second})
	d, ok := circuitRecoveryIn()
	require.True(t, ok)
	require.Equal(t, 9*time.Second, d)

	// 真实服务：未配置断路器时没有恢复时间；nil 服务也安全。
	registerCircuitRecoveryHint((*service.GatewayService)(nil))
	_, ok = circuitRecoveryIn()
	require.False(t, ok)
	registerCircuitRecoveryHint(&service.OpenAIGatewayService{})
	_, ok = circuitRecoveryIn()
	require.False(t, ok)
}

type recoveryHinterStub struct{ in time.Duration }

func (s *recoveryHinterStub) CircuitRecoveryIn() (time.Duration, bool) { return s.in, true }

// ---- 配置 ---------------------------------------------------------------------

func TestRetryAfterConfiguration(t *testing.T) {
	withRetryAfterDefaults(t) // 清掉其它用例（handler 构造时）注册的断路器恢复时间提示
	sink := func() *httptest.ResponseRecorder {
		c, rec := retryAfterTestContext("/v1/messages")
		(&GatewayHandler{}).handleStreamingAwareError(c, http.StatusServiceUnavailable, "api_error", "No available accounts", false)
		return rec
	}
	configure := func(rg config.GatewayRetryGuardConfig) {
		cfg := &config.Config{}
		cfg.Gateway.RetryGuard = rg
		ConfigureRetryGuard(cfg)
	}

	t.Run("enabled by default (nil config and a bare retry_guard section)", func(t *testing.T) {
		ConfigureRetryGuard(nil)
		requireRetryAfterInRange(t, sink(), 5, 15)
		ConfigureRetryGuard(&config.Config{})
		requireRetryAfterInRange(t, sink(), 5, 15)
	})
	t.Run("retry_after_enabled=false: no Retry-After at all, status and body untouched", func(t *testing.T) {
		configure(config.GatewayRetryGuardConfig{MaxAttempts: 30, MaxElapsedSeconds: 120, RetryAfterEnabled: false, RetryAfterMinSeconds: 5, RetryAfterMaxSeconds: 15})
		rec := sink()
		require.Empty(t, rec.Header().Get("Retry-After"))
		require.Equal(t, http.StatusServiceUnavailable, rec.Code)
		require.Equal(t, "api_error", gjson.GetBytes(rec.Body.Bytes(), "error.type").String())
	})
	t.Run("an explicit 0 range does not fall back to the default 5-15s", func(t *testing.T) {
		configure(config.GatewayRetryGuardConfig{MaxAttempts: 30, RetryAfterEnabled: true, RetryAfterMinSeconds: 0, RetryAfterMaxSeconds: 0})
		require.Empty(t, sink().Header().Get("Retry-After"))
	})
	t.Run("a negative range is invalid and falls back to the default", func(t *testing.T) {
		configure(config.GatewayRetryGuardConfig{MaxAttempts: 30, RetryAfterEnabled: true, RetryAfterMinSeconds: -1, RetryAfterMaxSeconds: -1})
		requireRetryAfterInRange(t, sink(), 5, 15)
	})
	t.Run("only a max configured: the minimum is 1s", func(t *testing.T) {
		configure(config.GatewayRetryGuardConfig{MaxAttempts: 30, RetryAfterEnabled: true, RetryAfterMinSeconds: 0, RetryAfterMaxSeconds: 4})
		for i := 0; i < 100; i++ {
			requireRetryAfterInRange(t, sink(), 1, 4)
		}
	})
	t.Run("a reversed range collapses to the minimum", func(t *testing.T) {
		configure(config.GatewayRetryGuardConfig{MaxAttempts: 30, RetryAfterEnabled: true, RetryAfterMinSeconds: 20, RetryAfterMaxSeconds: 5})
		requireRetryAfterInRange(t, sink(), 20, 20)
	})
	t.Run("the budget Retry-After honors the switch too", func(t *testing.T) {
		configure(config.GatewayRetryGuardConfig{MaxAttempts: 30, RetryAfterEnabled: false, RetryAfterMinSeconds: 5, RetryAfterMaxSeconds: 15})
		c, rec := retryAfterTestContext("/v1/messages")
		(&GatewayHandler{}).handleFailoverExhausted(c, &service.UpstreamFailoverError{StatusCode: 502, BudgetExhausted: true}, service.PlatformAnthropic, false)
		require.Empty(t, rec.Header().Get("Retry-After"))
	})
}

// ---- 基本行为 -------------------------------------------------------------------

func TestSetGatewayRetryAfterOnlyOnGatewayGenerated503And429(t *testing.T) {
	withRetryAfterDefaults(t)

	c, rec := retryAfterTestContext("/v1/messages")
	setGatewayRetryAfter(c, http.StatusServiceUnavailable, "api_error", "", "No available accounts")
	c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false})
	requireRetryAfterInRange(t, rec, 5, 15)
	require.Equal(t, http.StatusServiceUnavailable, rec.Code, "status code must not change")
	require.JSONEq(t, `{"ok":false}`, rec.Body.String(), "body must not change")

	c, rec = retryAfterTestContext("/v1/messages")
	setGatewayRetryAfter(c, http.StatusTooManyRequests, "rate_limit_error", gatewayQueueFullCode, "Too many pending requests")
	c.JSON(http.StatusTooManyRequests, gin.H{"ok": false})
	requireRetryAfterInRange(t, rec, 1, 3)

	for _, status := range []int{200, 400, 401, 403, 404, 500, 502, 504, 529} {
		c, rec := retryAfterTestContext("/v1/messages")
		setGatewayRetryAfter(c, status, "api_error", "", "No available accounts")
		c.JSON(status, gin.H{})
		require.Empty(t, rec.Header().Get("Retry-After"), "status %d must not get Retry-After", status)
	}
}

func TestSetGatewayRetryAfterKeepsExistingAndSkipsPassthroughAndCommitted(t *testing.T) {
	withRetryAfterDefaults(t)
	t.Run("an existing Retry-After (billing errors, upstream hint) is never overridden", func(t *testing.T) {
		c, rec := retryAfterTestContext("/v1/messages")
		c.Header("Retry-After", "3")
		setGatewayRetryAfter(c, http.StatusServiceUnavailable, "api_error", "", "No available accounts")
		c.JSON(http.StatusServiceUnavailable, gin.H{})
		require.Equal(t, "3", rec.Header().Get("Retry-After"))
	})
	t.Run("upstream passthrough errors are not the gateway's own decision", func(t *testing.T) {
		c, rec := retryAfterTestContext("/v1/messages")
		markUpstreamPassthroughError(c, nil)
		setGatewayRetryAfter(c, http.StatusServiceUnavailable, "api_error", "", "No available accounts")
		c.JSON(http.StatusServiceUnavailable, gin.H{})
		require.Empty(t, rec.Header().Get("Retry-After"))
	})
	t.Run("headers already sent (stream in progress) cannot be changed", func(t *testing.T) {
		c, rec := retryAfterTestContext("/v1/messages")
		_, err := c.Writer.Write([]byte("data: x\n\n"))
		require.NoError(t, err)
		setGatewayRetryAfter(c, http.StatusServiceUnavailable, "api_error", "", "No available accounts")
		require.Empty(t, rec.Header().Get("Retry-After"))
	})
	t.Run("nil context is safe", func(t *testing.T) {
		require.NotPanics(t, func() {
			setGatewayRetryAfter(nil, 503, "", "", "")
			markUpstreamPassthroughError(nil, nil)
			setRetryAfterHeader(nil, retryAfterNoCapacity)
		})
	})
}

// ---- 各 handler 家族的错误出口 ----------------------------------------------------

func TestRetryAfterOnMessagesFamilySinks(t *testing.T) {
	withRetryAfterDefaults(t)
	h := &GatewayHandler{}

	t.Run("no available accounts (503)", func(t *testing.T) {
		c, rec := retryAfterTestContext("/v1/messages")
		h.handleStreamingAwareError(c, http.StatusServiceUnavailable, "api_error", "No available accounts for this request", false)
		require.Equal(t, http.StatusServiceUnavailable, rec.Code)
		require.Equal(t, "api_error", gjson.GetBytes(rec.Body.Bytes(), "error.type").String(), "error body must be unchanged")
		requireRetryAfterInRange(t, rec, 5, 15)
	})
	t.Run("too many pending requests (429)", func(t *testing.T) {
		c, rec := retryAfterTestContext("/v1/messages")
		h.handleStreamingAwareErrorWithCode(c, http.StatusTooManyRequests, "rate_limit_error", gatewayQueueFullCode, "Too many pending requests, please retry later", false)
		require.Equal(t, http.StatusTooManyRequests, rec.Code)
		require.Equal(t, "Too many pending requests, please retry later", gjson.GetBytes(rec.Body.Bytes(), "error.message").String())
		requireRetryAfterInRange(t, rec, 1, 3)
	})
	t.Run("billing, quota and key limits (429) carry no Retry-After", func(t *testing.T) {
		for _, errType := range []string{"rate_limit_exceeded", "API_KEY_QUOTA_EXHAUSTED", "insufficient_quota"} {
			c, rec := retryAfterTestContext("/v1/messages")
			h.handleStreamingAwareError(c, http.StatusTooManyRequests, errType, "limit reached", false)
			require.Equal(t, http.StatusTooManyRequests, rec.Code, errType)
			require.Empty(t, rec.Header().Get("Retry-After"), errType)
		}
	})
	t.Run("a billing error that carries its own reset time keeps it", func(t *testing.T) {
		c, rec := retryAfterTestContext("/v1/messages")
		c.Header("Retry-After", "42") // billingErrorDetails 的 RPM / 额度窗口重置秒数
		h.handleStreamingAwareError(c, http.StatusTooManyRequests, "rate_limit_exceeded", "limit reached", false)
		require.Equal(t, "42", rec.Header().Get("Retry-After"))
	})
	t.Run("chat completions and responses variants", func(t *testing.T) {
		for name, tc := range map[string]struct {
			write  func(*gin.Context)
			lo, hi int
		}{
			"chat 503": {func(c *gin.Context) { h.chatCompletionsErrorResponse(c, 503, "server_error", "No available accounts") }, 5, 15},
			"chat queue full": {func(c *gin.Context) {
				h.chatCompletionsErrorResponseWithCode(c, 429, "rate_limit_error", "gateway_queue_full", "Too many pending requests")
			}, 1, 3},
			"responses 503": {func(c *gin.Context) { h.responsesErrorResponse(c, 503, "server_error", "No available accounts") }, 5, 15},
			"responses typed": {func(c *gin.Context) {
				h.responsesErrorResponseWithTypeAndCode(c, 429, "rate_limit_error", "gateway_queue_full", "Too many pending requests")
			}, 1, 3},
			"responses billing": {nil, 0, 0},
		} {
			c, rec := retryAfterTestContext("/v1/chat/completions")
			if tc.write == nil {
				// responsesErrorResponse 的第三个参数是错误码：计费类 429 不带 Retry-After
				h.responsesErrorResponse(c, 429, "insufficient_quota", "no balance")
				require.Empty(t, rec.Header().Get("Retry-After"), name)
				continue
			}
			tc.write(c)
			requireRetryAfterInRange(t, rec, tc.lo, tc.hi)
		}
	})
	t.Run("a 502 is not touched", func(t *testing.T) {
		c, rec := retryAfterTestContext("/v1/messages")
		h.handleStreamingAwareError(c, http.StatusBadGateway, "upstream_error", "Upstream request failed", false)
		require.Empty(t, rec.Header().Get("Retry-After"))
	})
	t.Run("a passthrough of the upstream's own 503 is left alone, its Retry-After is preserved", func(t *testing.T) {
		c, rec := retryAfterTestContext("/v1/messages")
		h.handleFailoverExhausted(c, &service.UpstreamFailoverError{
			StatusCode:      http.StatusServiceUnavailable,
			ResponseBody:    []byte(`{"type":"error","error":{"type":"overloaded_error","message":"upstream says no"}}`),
			ResponseHeaders: http.Header{"Retry-After": []string{"7"}},
		}, service.PlatformAnthropic, false)
		require.Equal(t, http.StatusServiceUnavailable, rec.Code)
		require.Equal(t, "7", rec.Header().Get("Retry-After"))

		c2, rec2 := retryAfterTestContext("/v1/messages")
		h.handleFailoverExhausted(c2, &service.UpstreamFailoverError{
			StatusCode:   http.StatusServiceUnavailable,
			ResponseBody: []byte(`{"type":"error","error":{"type":"overloaded_error","message":"upstream says no"}}`),
		}, service.PlatformAnthropic, false)
		require.Equal(t, http.StatusServiceUnavailable, rec2.Code)
		require.Empty(t, rec2.Header().Get("Retry-After"), "the gateway must not invent a back-off for a plain upstream passthrough")
	})
}

func TestRetryAfterWhenTheRetryBudgetIsExhausted(t *testing.T) {
	withRetryAfterDefaults(t)
	gatewayHandler := &GatewayHandler{}
	openAIHandler := &OpenAIGatewayHandler{}
	body := []byte(`{"error":{"message":"upstream is down"}}`)
	mk := func(status int, budget bool, headers http.Header) *service.UpstreamFailoverError {
		return &service.UpstreamFailoverError{StatusCode: status, ResponseBody: body, ResponseHeaders: headers, BudgetExhausted: budget}
	}
	exits := map[string]func(c *gin.Context, err *service.UpstreamFailoverError){
		"messages": func(c *gin.Context, err *service.UpstreamFailoverError) {
			gatewayHandler.handleFailoverExhausted(c, err, service.PlatformAnthropic, false)
		},
		"chat completions": func(c *gin.Context, err *service.UpstreamFailoverError) {
			gatewayHandler.handleCCFailoverExhausted(c, err, false)
		},
		"responses": func(c *gin.Context, err *service.UpstreamFailoverError) {
			gatewayHandler.handleResponsesFailoverExhausted(c, err, false)
		},
		"gemini native": func(c *gin.Context, err *service.UpstreamFailoverError) {
			gatewayHandler.handleGeminiFailoverExhausted(c, err)
		},
		"openai responses": func(c *gin.Context, err *service.UpstreamFailoverError) {
			openAIHandler.handleFailoverExhausted(c, err, false)
		},
		"openai anthropic path": func(c *gin.Context, err *service.UpstreamFailoverError) {
			openAIHandler.handleAnthropicFailoverExhausted(c, err, false)
		},
	}
	for name, exit := range exits {
		t.Run(name+": 5xx after the budget ran out gets 5-10s", func(t *testing.T) {
			for _, status := range []int{500, 502, 503, 504} {
				c, rec := retryAfterTestContext("/v1/messages")
				exit(c, mk(status, true, nil))
				requireRetryAfterInRange(t, rec, 5, 10)
			}
		})
		t.Run(name+": the upstream's own Retry-After wins", func(t *testing.T) {
			if name == "gemini native" {
				t.Skip("the Gemini native passthrough never forwarded the upstream Retry-After")
			}
			c, rec := retryAfterTestContext("/v1/messages")
			exit(c, mk(503, true, http.Header{"Retry-After": []string{"7"}}))
			require.Equal(t, "7", rec.Header().Get("Retry-After"))
		})
		t.Run(name+": without budget exhaustion nothing is invented", func(t *testing.T) {
			c, rec := retryAfterTestContext("/v1/messages")
			exit(c, mk(502, false, nil))
			require.Empty(t, rec.Header().Get("Retry-After"))
		})
		t.Run(name+": a passthrough 429 or 4xx is not touched even if the budget ran out", func(t *testing.T) {
			for _, status := range []int{429, 400} {
				c, rec := retryAfterTestContext("/v1/messages")
				exit(c, mk(status, true, nil))
				require.Empty(t, rec.Header().Get("Retry-After"), "status %d", status)
			}
		})
	}

	t.Run("the status code and body of the passthrough stay as the upstream sent them", func(t *testing.T) {
		c, rec := retryAfterTestContext("/v1/messages")
		openAIHandler.handleFailoverExhausted(c, mk(503, true, nil), false)
		require.Equal(t, http.StatusServiceUnavailable, rec.Code)
		require.Contains(t, rec.Body.String(), "upstream is down")
		requireRetryAfterInRange(t, rec, 5, 10)
	})
	t.Run("the simple variants have no failover error and add nothing", func(t *testing.T) {
		c, rec := retryAfterTestContext("/v1/messages")
		gatewayHandler.handleFailoverExhaustedSimple(c, 502, false)
		require.Empty(t, rec.Header().Get("Retry-After"))
		c2, rec2 := retryAfterTestContext("/v1/responses")
		openAIHandler.handleFailoverExhaustedSimple(c2, 502, false)
		require.Empty(t, rec2.Header().Get("Retry-After"))
	})
}

func TestRetryAfterOnGeminiNativeSink(t *testing.T) {
	withRetryAfterDefaults(t)
	c, rec := retryAfterTestContext("/v1beta/models/gemini-2.5-pro:generateContent")
	googleError(c, http.StatusServiceUnavailable, "No available accounts")
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Equal(t, int64(http.StatusServiceUnavailable), gjson.GetBytes(rec.Body.Bytes(), "error.code").Int())
	requireRetryAfterInRange(t, rec, 5, 15)

	c1, rec1 := retryAfterTestContext("/v1beta/models/gemini-2.5-pro:generateContent")
	googleError(c1, http.StatusTooManyRequests, "Too many pending requests, please retry later")
	requireRetryAfterInRange(t, rec1, 1, 3)

	c2, rec2 := retryAfterTestContext("/v1beta/models/gemini-2.5-pro:generateContent")
	googleError(c2, http.StatusBadGateway, "Upstream request failed")
	require.Empty(t, rec2.Header().Get("Retry-After"))

	c3, rec3 := retryAfterTestContext("/v1beta/models/gemini-2.5-pro:generateContent")
	googleError(c3, http.StatusTooManyRequests, "api key quota exhausted")
	require.Empty(t, rec3.Header().Get("Retry-After"), "an unrecognised 429 is not given a made-up back-off")
}

func TestRetryAfterOnOpenAIFamilySinks(t *testing.T) {
	withRetryAfterDefaults(t)
	h := &OpenAIGatewayHandler{}

	for name, tc := range map[string]struct {
		write  func(*gin.Context)
		lo, hi int
	}{
		"errorResponse 503":          {func(c *gin.Context) { h.errorResponse(c, 503, "api_error", "No available accounts for this request") }, 5, 15},
		"errorResponse 429 pending":  {func(c *gin.Context) { h.errorResponse(c, 429, "rate_limit_error", "Too many pending requests") }, 1, 3},
		"anthropicErrorResponse 503": {func(c *gin.Context) { h.anthropicErrorResponse(c, 503, "api_error", "No available accounts") }, 5, 15},
		"streamingAware 503":         {func(c *gin.Context) { h.handleStreamingAwareError(c, 503, "api_error", "No available accounts", false) }, 5, 15},
		"streamingAware 429 with code": {func(c *gin.Context) {
			h.handleStreamingAwareErrorWithCode(c, 429, "rate_limit_error", "gateway_queue_full", "Too many pending requests", false, false)
		}, 1, 3},
		"streamingAware 429 concurrency": {func(c *gin.Context) {
			h.handleStreamingAwareErrorWithCode(c, 429, "rate_limit_error", gatewayConcurrencyLimitCode, "Concurrency limit exceeded for account, please retry later", false, false)
		}, 1, 3},
	} {
		t.Run(name, func(t *testing.T) {
			c, rec := retryAfterTestContext("/v1/responses")
			tc.write(c)
			require.Contains(t, []int{429, 503}, rec.Code, "status code is unchanged")
			requireRetryAfterInRange(t, rec, tc.lo, tc.hi)
		})
	}

	t.Run("billing and quota 429 carry no Retry-After", func(t *testing.T) {
		for _, errType := range []string{"rate_limit_exceeded", "API_KEY_QUOTA_EXHAUSTED", "insufficient_quota"} {
			c, rec := retryAfterTestContext("/v1/responses")
			h.errorResponse(c, 429, errType, "limit reached")
			require.Empty(t, rec.Header().Get("Retry-After"), errType)
			c2, rec2 := retryAfterTestContext("/v1/responses")
			h.handleStreamingAwareErrorWithCode(c2, 429, errType, "some_code", "limit reached", false, false)
			require.Empty(t, rec2.Header().Get("Retry-After"), errType)
		}
	})
	t.Run("502 and 400 untouched", func(t *testing.T) {
		for _, status := range []int{502, 400} {
			c, rec := retryAfterTestContext("/v1/responses")
			h.errorResponse(c, status, "upstream_error", "x")
			require.Empty(t, rec.Header().Get("Retry-After"))
		}
	})
	t.Run("upstream passthrough 503 keeps its own headers only", func(t *testing.T) {
		c, rec := retryAfterTestContext("/v1/responses")
		h.handleFailoverExhausted(c, &service.UpstreamFailoverError{
			StatusCode:   http.StatusServiceUnavailable,
			ResponseBody: []byte(`{"error":{"message":"upstream says no"}}`),
		}, false)
		require.Equal(t, http.StatusServiceUnavailable, rec.Code)
		require.Empty(t, rec.Header().Get("Retry-After"))
	})
	t.Run("handler dependencies missing (503)", func(t *testing.T) {
		c, rec := retryAfterTestContext("/v1/responses")
		require.False(t, h.ensureResponsesDependencies(c, nil))
		require.Equal(t, http.StatusServiceUnavailable, rec.Code)
		requireRetryAfterInRange(t, rec, 5, 15)
	})
}

func TestRetryAfterOnTheWebSearchSink(t *testing.T) {
	withRetryAfterDefaults(t)
	setCircuitRecoveryIn(t, 12*time.Second, true)
	c, rec := retryAfterTestContext("/v1/messages")
	setGatewayRetryAfter(c, http.StatusServiceUnavailable, "scheduling_error", "", "No available accounts")
	c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"type": "scheduling_error", "message": "No available accounts"}})
	requireRetryAfterInRange(t, rec, 13, 17)
	require.Equal(t, "scheduling_error", gjson.GetBytes(rec.Body.Bytes(), "error.type").String())
}
