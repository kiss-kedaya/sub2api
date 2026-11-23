package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/stretchr/testify/require"
)

// 审计 A-P1-06：密码因子没有「按用户」的失败预算，持会话者可以对 secret-seal 的 PUT
// （以及创建 / 轮换 / 二次查看）在线猜登录密码。
//
// 这里测的是 handler 的接线：预算耗尽 → 429 + Retry-After、成功 → Reset、按用户记账、
// 五个入口共用、存储故障 fail-closed。预算本身用内存假实现（handler 包按 depguard 不能 import redis）；
// 真正走 Redis 滑动窗口的部分（60 秒 5 次、成功清零、按用户、与 2FA 桶独立）在
// internal/server/middleware 与 internal/server 的测试里。

// fakePasswordBudget 是 service.APIKeyPasswordAttemptLimiter 的内存实现：
// 每个用户最多 max 次尝试，放行的尝试才记账（与真实的滑动窗口一致），Reset 清零。
type fakePasswordBudget struct {
	mu     sync.Mutex
	max    int
	retry  time.Duration
	err    error
	used   map[int64]int
	resets []int64
}

func newFakePasswordBudget(max int) *fakePasswordBudget {
	return &fakePasswordBudget{max: max, retry: 42 * time.Second, used: map[int64]int{}}
}

func (b *fakePasswordBudget) Check(_ context.Context, userID int64) (bool, time.Duration, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.err != nil {
		return false, 0, b.err
	}
	if b.used[userID] >= b.max {
		return false, b.retry, nil
	}
	b.used[userID]++
	return true, 0, nil
}

func (b *fakePasswordBudget) Reset(_ context.Context, userID int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.used, userID)
	b.resets = append(b.resets, userID)
}

func (b *fakePasswordBudget) usedBy(userID int64) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.used[userID]
}

func (b *fakePasswordBudget) resetCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.resets)
}

type passwordBudgetEnv struct {
	*handlerVerificationEngine
	budget *fakePasswordBudget
}

func newPasswordBudgetEnv(t *testing.T) *passwordBudgetEnv {
	t.Helper()
	engine := newHandlerVerificationEngine(t, handlerVerificationUser(t))
	budget := newFakePasswordBudget(5)
	engine.handler.SetPasswordAttemptLimiter(budget)
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
	return &passwordBudgetEnv{handlerVerificationEngine: engine, budget: budget}
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

	// 前 5 次错误口令照常报 VERIFY_PASSWORD_INVALID，每次都记在当前用户（7）名下。
	for i := 1; i <= 5; i++ {
		rec := seal("wrong-password")
		require.Equal(t, http.StatusBadRequest, rec.Code, "guess %d: %s", i, rec.Body.String())
		require.Equal(t, "VERIFY_PASSWORD_INVALID", handlerVerificationErrorReason(t, rec))
		require.Equal(t, i, env.budget.usedBy(7))
	}

	// 第 6 次起 429 + Retry-After，而且连正确口令也不再被确认：不是「限流后的 oracle」。
	for _, password := range []string{"wrong-password", "correct-password"} {
		rec := seal(password)
		require.Equal(t, http.StatusTooManyRequests, rec.Code, rec.Body.String())
		require.Equal(t, "VERIFY_PASSWORD_RATE_LIMITED", handlerVerificationErrorReason(t, rec))
		require.Equal(t, "42", rec.Header().Get("Retry-After"), "Retry-After carries the limiter's remaining window")
	}
	require.Equal(t, "c2VhbGVk", env.secretStore.rows[1].Sealed, "a blocked correct password must not overwrite the sealed secret")
	require.Zero(t, env.budget.resetCount(), "a blocked attempt is not a success")
	require.Equal(t, map[int64]int{7: 5}, env.budget.used, "only the authenticated user is charged")
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
			require.Equal(t, "42", rec.Header().Get("Retry-After"), "%s must write Retry-After", target)
			require.Zero(t, env.createRepo.calls, "a rate limited attempt must not create a key")
		})
	}
}

func TestPasswordBudgetClearsOnSuccess(t *testing.T) {
	env := newPasswordBudgetEnv(t)
	seal := env.passwordEntries(t)["seal"]

	for round := 1; round <= 3; round++ {
		for i := 1; i <= 4; i++ {
			rec := seal("wrong-password")
			require.Equal(t, http.StatusBadRequest, rec.Code, "round %d guess %d: %s", round, i, rec.Body.String())
		}
		require.Equal(t, 4, env.budget.usedBy(7))
		// 正确口令成功即清零：预算只数「连续」失败。
		rec := seal("correct-password")
		require.Equal(t, http.StatusOK, rec.Code, "round %d: %s", round, rec.Body.String())
		require.Equal(t, round, env.budget.resetCount())
		require.Zero(t, env.budget.usedBy(7), "success clears the user's failures")
	}
	// 清零之后又是一整份预算：5 次错误仍是 400，第 6 次才 429。
	for i := 1; i <= 5; i++ {
		require.Equal(t, http.StatusBadRequest, seal("wrong-password").Code, "guess %d", i)
	}
	require.Equal(t, http.StatusTooManyRequests, seal("wrong-password").Code)

	// 失败本身不清零。
	require.Equal(t, 3, env.budget.resetCount())
}

func TestBatchSealOfFiftyKeysIsNotBlockedByThePasswordBudget(t *testing.T) {
	env := newPasswordBudgetEnv(t)
	seal := env.passwordEntries(t)["seal"]

	// 批量轮换后前端会用同一个正确口令连续封存最多 50 把：每次成功都清零，不能被预算误伤。
	for i := 1; i <= 50; i++ {
		rec := seal("correct-password")
		require.Equal(t, http.StatusOK, rec.Code, "seal %d: %s", i, rec.Body.String())
	}
	require.Equal(t, 50, env.budget.resetCount())
}

func TestPasswordBudgetFailsClosedWhenTheStoreIsDown(t *testing.T) {
	env := newPasswordBudgetEnv(t)
	seal := env.passwordEntries(t)["seal"]
	env.budget.err = errors.New("redis down")

	// 预算存储不可用时拒绝校验（fail-closed），而不是退回「不限次数的 oracle」。
	rec := seal("correct-password")
	require.Equal(t, http.StatusServiceUnavailable, rec.Code, rec.Body.String())
	require.Equal(t, "VERIFY_UNAVAILABLE", handlerVerificationErrorReason(t, rec))
	require.Equal(t, "c2VhbGVk", env.secretStore.rows[1].Sealed)
	require.Zero(t, env.budget.resetCount())
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
	nilHandler.SetPasswordAttemptLimiter(newFakePasswordBudget(5))
	// 校验服务未装配：什么都不做，不 panic。
	NewAPIKeyHandler(nil, nil).SetPasswordAttemptLimiter(newFakePasswordBudget(5))
}
