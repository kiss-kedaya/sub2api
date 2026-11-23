package handler

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// 审计 A-P1-06：密码因子没有「按用户」的失败预算，持会话者可以对 secret-seal 的 PUT
// （以及创建 / 轮换 / 二次查看）在线猜登录密码。这里用真实的账号预算实现
// （middleware.TwoFactorRateLimiter + miniredis，60 秒内最多 5 次，成功清零）跑 HTTP 全链路。

type passwordBudgetEnv struct {
	*handlerVerificationEngine
	redis *miniredis.Miniredis
}

func newPasswordBudgetEnv(t *testing.T) *passwordBudgetEnv {
	t.Helper()
	engine := newHandlerVerificationEngine(t, handlerVerificationUser(t))
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	// 与生产装配（router.configureAccountVerificationBudgets）同一个构造函数：独立的密码桶。
	engine.handler.SetPasswordAttemptLimiter(middleware2.NewAPIKeyPasswordRateLimiter(client))
	// 创建走幂等协调器（生产装配必有）：校验失败的错误要经它回到客户端，Retry-After 也在那一路写出。
	previous := service.DefaultIdempotencyCoordinator()
	service.SetDefaultIdempotencyCoordinator(service.NewIdempotencyCoordinator(newUserMemoryIdempotencyRepoStub(), service.DefaultIdempotencyConfig()))
	t.Cleanup(func() { service.SetDefaultIdempotencyCoordinator(previous) })
	// 二次查看授权需要这把 Key 已有封存密文（旧密文不限制因子）。
	engine.secretStore.rows[1] = &service.APIKeySecret{
		APIKeyID: 1, UserID: 7, Version: 1,
		KDF:    []byte(`{"algo":"PBKDF2-SHA256","iterations":310000,"salt":"MDEyMzQ1Njc4OWFiY2RlZg=="}`),
		Sealed: "c2VhbGVk",
	}
	return &passwordBudgetEnv{handlerVerificationEngine: engine, redis: redisServer}
}

// passwordEntries 是全部走密码因子的入口；每个函数用给定口令发一次请求。
func (e *passwordBudgetEnv) passwordEntries(t *testing.T) map[string]func(password string) *httptest.ResponseRecorder {
	t.Helper()
	verification := func(password string) string {
		return `{"method":"password","password":"` + password + `"}`
	}
	createSeq := 0
	return map[string]func(string) *httptest.ResponseRecorder{
		"seal": func(password string) *httptest.ResponseRecorder {
			return doJSON(e.router, http.MethodPut, "/api/v1/user/api-keys/1/secret-seal",
				handlerVerificationSealBody(t, password, "password"), nil)
		},
		"create": func(password string) *httptest.ResponseRecorder {
			createSeq++
			return doJSON(e.router, http.MethodPost, "/api/v1/keys",
				`{"name":"budget","verification":`+verification(password)+`}`,
				map[string]string{"Idempotency-Key": "budget-create-" + strconv.Itoa(createSeq)})
		},
		"rotate": func(password string) *httptest.ResponseRecorder {
			return doJSON(e.router, http.MethodPut, "/api/v1/keys/1",
				`{"regenerate_key":true,"verification":`+verification(password)+`}`, nil)
		},
		"batch-rotate": func(password string) *httptest.ResponseRecorder {
			return doJSON(e.router, http.MethodPost, "/api/v1/keys/batch-rotate",
				`{"ids":[1],"verification":`+verification(password)+`}`, nil)
		},
		"reveal-authorize": func(password string) *httptest.ResponseRecorder {
			return doJSON(e.router, http.MethodPost, "/api/v1/user/api-keys/1/reveal-authorize",
				verification(password), nil)
		},
	}
}

func TestSecretSealPasswordGuessingIsBoundedPerUser(t *testing.T) {
	env := newPasswordBudgetEnv(t)
	seal := env.passwordEntries(t)["seal"]

	// 前 5 次错误口令照常报 VERIFY_PASSWORD_INVALID。
	for i := 1; i <= 5; i++ {
		rec := seal("wrong-password")
		require.Equal(t, http.StatusBadRequest, rec.Code, "guess %d: %s", i, rec.Body.String())
		require.Equal(t, "VERIFY_PASSWORD_INVALID", handlerVerificationErrorReason(t, rec))
	}

	// 第 6 次起 429 + Retry-After，而且连正确口令也不再被确认：不是「限流后的 oracle」。
	for _, password := range []string{"wrong-password", "correct-password"} {
		rec := seal(password)
		require.Equal(t, http.StatusTooManyRequests, rec.Code, rec.Body.String())
		require.Equal(t, "VERIFY_PASSWORD_RATE_LIMITED", handlerVerificationErrorReason(t, rec))
		retryAfter, err := strconv.Atoi(rec.Header().Get("Retry-After"))
		require.NoError(t, err, "Retry-After must be set")
		require.Greater(t, retryAfter, 0)
		require.LessOrEqual(t, retryAfter, 60)
	}
	require.Equal(t, "c2VhbGVk", env.secretStore.rows[1].Sealed, "a blocked correct password must not overwrite the sealed secret")

	// 预算按用户记账（不是 IP / 不是入口）：Redis 里只有 user 7 这一个账号维度的密码桶，
	// 而且不碰 2FA 的桶。
	var budgetKeys, twoFactorKeys []string
	for _, key := range env.redis.Keys() {
		if strings.Contains(key, "api_key_password:verify:user:") {
			budgetKeys = append(budgetKeys, key)
		}
		if strings.Contains(key, "2fa:verify:user:") {
			twoFactorKeys = append(twoFactorKeys, key)
		}
	}
	require.Len(t, budgetKeys, 1)
	require.True(t, strings.HasSuffix(budgetKeys[0], "api_key_password:verify:user:7"), budgetKeys[0])
	require.Empty(t, twoFactorKeys, "password guesses must not consume the account's 2FA budget")
}

// spendTwoFactorAttempts 手工消耗账号 2FA 预算的 n 次尝试。
// 滑动窗口脚本用纳秒时间戳当 ZSET 成员；Windows 开发机上系统时钟一个刻度可以长达十几毫秒，
// 紧挨着的两次尝试会得到同一个成员而被去重，所以这里每次之间隔开一点（Linux 上不会发生）。
func spendTwoFactorAttempts(t *testing.T, limiter *middleware2.TwoFactorRateLimiter, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		allowed, _, err := limiter.Check(t.Context(), 7)
		require.NoError(t, err)
		require.True(t, allowed, "2FA attempt %d should still be inside the budget", i+1)
		time.Sleep(25 * time.Millisecond)
	}
}

// 密码因子的预算和账号的 2FA 预算互相独立：密码校验成功不能给 TOTP 猜码续命，
// 密码猜错也不能把账号的登录 2FA 锁住。
func TestPasswordBudgetDoesNotTouchTheTwoFactorBudget(t *testing.T) {
	env := newPasswordBudgetEnv(t)
	seal := env.passwordEntries(t)["seal"]
	client := redis.NewClient(&redis.Options{Addr: env.redis.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	twoFactor := middleware2.NewTwoFactorRateLimiter(client)

	// 账号的 2FA 已经失败了 4 次。
	spendTwoFactorAttempts(t, twoFactor, 4)
	// 密码校验成功（成功即清零）：只能清密码的桶。
	require.Equal(t, http.StatusOK, seal("correct-password").Code)
	spendTwoFactorAttempts(t, twoFactor, 1) // 第 5 次：仍在预算内
	allowed, _, err := twoFactor.Check(t.Context(), 7)
	require.NoError(t, err)
	require.False(t, allowed, "a password success must not refill the 2FA guess budget")

	// 反过来：密码预算被刷满，账号的 2FA 预算照样可用。
	env2 := newPasswordBudgetEnv(t)
	seal2 := env2.passwordEntries(t)["seal"]
	for i := 0; i < 6; i++ {
		seal2("wrong-password")
	}
	require.Equal(t, http.StatusTooManyRequests, seal2("correct-password").Code)
	client2 := redis.NewClient(&redis.Options{Addr: env2.redis.Addr()})
	t.Cleanup(func() { _ = client2.Close() })
	allowed, _, err = middleware2.NewTwoFactorRateLimiter(client2).Check(t.Context(), 7)
	require.NoError(t, err)
	require.True(t, allowed, "an exhausted password budget must not lock the account's 2FA")
}

func TestPasswordBudgetIsSharedByEveryVerificationEntry(t *testing.T) {
	names := []string{"seal", "create", "rotate", "batch-rotate", "reveal-authorize"}
	for _, target := range names {
		t.Run(target, func(t *testing.T) {
			env := newPasswordBudgetEnv(t)
			entries := env.passwordEntries(t)

			// 用其余入口轮流攒满 5 次失败，目标入口自己一次都没用过。
			failures := 0
			for failures < 5 {
				for _, other := range names {
					if other == target || failures >= 5 {
						continue
					}
					rec := entries[other]("wrong-password")
					require.Equal(t, http.StatusBadRequest, rec.Code, "%s failure %d: %s", other, failures+1, rec.Body.String())
					failures++
				}
			}

			rec := entries[target]("correct-password")
			require.Equal(t, http.StatusTooManyRequests, rec.Code, "%s must share the per-user budget: %s", target, rec.Body.String())
			require.Equal(t, "VERIFY_PASSWORD_RATE_LIMITED", handlerVerificationErrorReason(t, rec))
			require.NotEmpty(t, rec.Header().Get("Retry-After"))
			require.Zero(t, env.createRepo.calls, "a rate limited attempt must not create a key")
		})
	}
}

func TestPasswordBudgetClearsOnSuccess(t *testing.T) {
	env := newPasswordBudgetEnv(t)
	seal := env.passwordEntries(t)["seal"]

	for round := 0; round < 3; round++ {
		for i := 1; i <= 4; i++ {
			rec := seal("wrong-password")
			require.Equal(t, http.StatusBadRequest, rec.Code, "round %d guess %d: %s", round, i, rec.Body.String())
		}
		// 正确口令成功即清零：预算只数「连续」失败。
		rec := seal("correct-password")
		require.Equal(t, http.StatusOK, rec.Code, "round %d: %s", round, rec.Body.String())
	}
	// 清零之后又是一整份预算：5 次错误仍是 400，第 6 次才 429。
	for i := 1; i <= 5; i++ {
		require.Equal(t, http.StatusBadRequest, seal("wrong-password").Code, "guess %d", i)
	}
	require.Equal(t, http.StatusTooManyRequests, seal("wrong-password").Code)
}

func TestBatchSealOfFiftyKeysIsNotBlockedByThePasswordBudget(t *testing.T) {
	env := newPasswordBudgetEnv(t)
	seal := env.passwordEntries(t)["seal"]

	// 批量轮换后前端会用同一个正确口令连续封存最多 50 把：每次成功都清零，不能被预算误伤。
	for i := 1; i <= 50; i++ {
		rec := seal("correct-password")
		require.Equal(t, http.StatusOK, rec.Code, "seal %d: %s", i, rec.Body.String())
	}
}

func TestPasswordBudgetFailsClosedWhenTheStoreIsDown(t *testing.T) {
	env := newPasswordBudgetEnv(t)
	seal := env.passwordEntries(t)["seal"]
	env.redis.Close()

	// 预算存储不可用时拒绝校验（fail-closed），而不是退回「不限次数的 oracle」。
	rec := seal("correct-password")
	require.Equal(t, http.StatusServiceUnavailable, rec.Code, rec.Body.String())
	require.Equal(t, "VERIFY_UNAVAILABLE", handlerVerificationErrorReason(t, rec))
	require.Equal(t, "c2VhbGVk", env.secretStore.rows[1].Sealed)
}

func TestWithoutPasswordBudgetBehaviourIsUnchanged(t *testing.T) {
	// 未装配预算（旧测试 / 嵌入方）保持装配前的行为：不限次数、也不报 429。
	engine := newHandlerVerificationEngine(t, handlerVerificationUser(t))
	for i := 0; i < 12; i++ {
		rec := doJSON(engine.router, http.MethodPut, "/api/v1/user/api-keys/1/secret-seal",
			handlerVerificationSealBody(t, "wrong-password", "password"), nil)
		require.Equal(t, http.StatusBadRequest, rec.Code)
	}
}

func TestSetPasswordAttemptLimiterIsNilSafe(t *testing.T) {
	var nilHandler *APIKeyHandler
	nilHandler.SetPasswordAttemptLimiter(nil)
	// 校验服务未装配：什么都不做，不 panic。
	NewAPIKeyHandler(nil, nil).SetPasswordAttemptLimiter(middleware2.NewTwoFactorRateLimiter(nil))
}
