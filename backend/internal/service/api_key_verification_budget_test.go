//go:build unit

package service

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"

	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/require"
)

// 审计 A-P1-06：登录密码因子要有「按用户」的失败预算（连续失败有上限、成功清零），
// 创建 / 轮换 / 封存 / 二次查看四类入口共用同一份，不能哪个入口都是不限次数的密码 oracle。

// passwordBudgetStub 复刻账号预算的语义：窗口内最多 max 次；放行的尝试才记账（与真实的
// 滑动窗口脚本一致，超限请求不延长封锁）；Reset 清零；按用户 ID 分开计数。
type passwordBudgetStub struct {
	mu     sync.Mutex
	max    int
	retry  time.Duration
	err    error
	used   map[int64]int
	checks int
	resets []int64
}

func newPasswordBudgetStub(max int) *passwordBudgetStub {
	return &passwordBudgetStub{max: max, retry: 42 * time.Second, used: map[int64]int{}}
}

func (b *passwordBudgetStub) Check(_ context.Context, userID int64) (bool, time.Duration, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.checks++
	if b.err != nil {
		return false, 0, b.err
	}
	if b.used[userID] >= b.max {
		return false, b.retry, nil
	}
	b.used[userID]++
	return true, 0, nil
}

func (b *passwordBudgetStub) Reset(_ context.Context, userID int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.used, userID)
	b.resets = append(b.resets, userID)
}

func newBudgetedService(t *testing.T, budget APIKeyPasswordAttemptLimiter, totpSecret string) (*APIKeyVerificationService, *User) {
	t.Helper()
	user := apiKeyVerifyTestUser(t, "correct-password")
	key := apiKeyVerifyTestKey(nil)
	secret := apiKeyVerifyTestSecret("")
	svc, _, _ := newAPIKeyVerificationTestService(t, user, key, secret, totpSecret)
	svc.SetPasswordAttemptLimiter(budget)
	return svc, user
}

func passwordInput(password string) *APIKeyVerificationInput {
	return &APIKeyVerificationInput{Method: APIKeyFactorPassword, Password: password}
}

func TestAPIKeyPasswordBudgetBlocksBeforeComparingAndIsPerUser(t *testing.T) {
	ctx := context.Background()
	budget := newPasswordBudgetStub(5)
	svc, user := newBudgetedService(t, budget, "")

	for i := 1; i <= 5; i++ {
		_, err := svc.VerifyForCreate(ctx, user.ID, passwordInput("wrong-password"))
		require.ErrorIs(t, err, ErrAPIKeyPasswordInvalid, "guess %d", i)
	}

	// 预算用尽：正确口令也拿不到 200 / 通过 —— 先限流再 bcrypt，不是限流后的 oracle。
	_, err := svc.VerifyForCreate(ctx, user.ID, passwordInput("correct-password"))
	require.ErrorIs(t, err, ErrAPIKeyPasswordRateLimited)
	require.Equal(t, 42, RetryAfterSecondsFromError(err), "retry_after must carry the limiter's remaining window")
	require.Equal(t, http.StatusTooManyRequests, infraerrors.Code(err))

	// 账号维度：只给 user 7 记账，别的用户互不影响。
	require.Equal(t, map[int64]int{7: 5}, budget.used)
	allowed, _, checkErr := budget.Check(ctx, 8)
	require.NoError(t, checkErr)
	require.True(t, allowed)
}

func TestAPIKeyPasswordBudgetIsSharedByEveryEntry(t *testing.T) {
	ctx := context.Background()
	budget := newPasswordBudgetStub(5)
	svc, user := newBudgetedService(t, budget, "")

	// 创建 ×1、编辑（轮换）×1、封存 ×2、二次查看 ×1：5 次失败来自 4 个不同入口。
	_, err := svc.VerifyForCreate(ctx, user.ID, passwordInput("wrong-password"))
	require.ErrorIs(t, err, ErrAPIKeyPasswordInvalid)
	_, err = svc.VerifyForKeyEdit(ctx, user.ID, 42, passwordInput("wrong-password"))
	require.ErrorIs(t, err, ErrAPIKeyPasswordInvalid)
	require.ErrorIs(t, svc.VerifyForSeal(ctx, user.ID, "wrong-password"), ErrAPIKeyPasswordInvalid)
	require.ErrorIs(t, svc.VerifyForSeal(ctx, user.ID, "wrong-password"), ErrAPIKeyPasswordInvalid)
	_, err = svc.AuthorizeReveal(ctx, user.ID, 42, *passwordInput("wrong-password"))
	require.ErrorIs(t, err, ErrAPIKeyPasswordInvalid)

	// 每个入口再来一次都是 429，哪怕口令是对的。
	_, err = svc.VerifyForCreate(ctx, user.ID, passwordInput("correct-password"))
	require.ErrorIs(t, err, ErrAPIKeyPasswordRateLimited)
	_, err = svc.VerifyForKeyEdit(ctx, user.ID, 42, passwordInput("correct-password"))
	require.ErrorIs(t, err, ErrAPIKeyPasswordRateLimited)
	require.ErrorIs(t, svc.VerifyForSeal(ctx, user.ID, "correct-password"), ErrAPIKeyPasswordRateLimited)
	_, err = svc.AuthorizeReveal(ctx, user.ID, 42, *passwordInput("correct-password"))
	require.ErrorIs(t, err, ErrAPIKeyPasswordRateLimited)
}

func TestAPIKeyPasswordBudgetClearsOnSuccess(t *testing.T) {
	ctx := context.Background()
	budget := newPasswordBudgetStub(5)
	svc, user := newBudgetedService(t, budget, "")

	for round := 0; round < 3; round++ {
		for i := 0; i < 4; i++ {
			require.ErrorIs(t, svc.VerifyForSeal(ctx, user.ID, "wrong-password"), ErrAPIKeyPasswordInvalid)
		}
		require.NoError(t, svc.VerifyForSeal(ctx, user.ID, "correct-password"))
	}
	require.Equal(t, []int64{7, 7, 7}, budget.resets, "every success clears the user's budget")
	require.Empty(t, budget.used)

	// 失败不清零。
	require.ErrorIs(t, svc.VerifyForSeal(ctx, user.ID, "wrong-password"), ErrAPIKeyPasswordInvalid)
	require.Len(t, budget.resets, 3)
	require.Equal(t, map[int64]int{7: 1}, budget.used)
}

func TestAPIKeyPasswordBudgetFailsClosedOnStoreError(t *testing.T) {
	ctx := context.Background()
	budget := newPasswordBudgetStub(5)
	budget.err = errors.New("redis down")
	svc, user := newBudgetedService(t, budget, "")

	_, err := svc.VerifyForCreate(ctx, user.ID, passwordInput("correct-password"))
	require.ErrorIs(t, err, ErrAPIKeyVerificationUnavailable)
	require.ErrorIs(t, svc.VerifyForSeal(ctx, user.ID, "correct-password"), ErrAPIKeyVerificationUnavailable)
	require.Empty(t, budget.resets)
}

func TestAPIKeyPasswordBudgetIgnoresEmptyPasswordAndOtherFactors(t *testing.T) {
	ctx := context.Background()
	budget := newPasswordBudgetStub(5)
	secret := "JBSWY3DPEHPK3PXP"
	svc, user := newBudgetedService(t, budget, secret)

	// 空口令没有猜测价值，不消耗预算。
	for i := 0; i < 10; i++ {
		_, err := svc.VerifyForCreate(ctx, user.ID, passwordInput("   "))
		require.ErrorIs(t, err, ErrAPIKeyPasswordInvalid)
		require.ErrorIs(t, svc.VerifyForSeal(ctx, user.ID, ""), ErrAPIKeyPasswordInvalid)
	}
	require.Zero(t, budget.checks)

	// 密码预算用尽不影响动态码因子（两条因子各管各的失败预算）。
	for i := 0; i < 5; i++ {
		_, _ = svc.VerifyForCreate(ctx, user.ID, passwordInput("wrong-password"))
	}
	_, err := svc.VerifyForCreate(ctx, user.ID, passwordInput("correct-password"))
	require.ErrorIs(t, err, ErrAPIKeyPasswordRateLimited)

	code, genErr := totp.GenerateCode(secret, time.Now())
	require.NoError(t, genErr)
	result, err := svc.VerifyForCreate(ctx, user.ID, &APIKeyVerificationInput{Method: APIKeyFactorTOTP, TOTPCode: code})
	require.NoError(t, err)
	require.Equal(t, APIKeyFactorTOTP, result.Factor)
}

func TestAPIKeyPasswordWithoutBudgetKeepsLegacyBehaviour(t *testing.T) {
	ctx := context.Background()
	svc, user := newBudgetedService(t, nil, "")

	for i := 0; i < 20; i++ {
		_, err := svc.VerifyForCreate(ctx, user.ID, passwordInput("wrong-password"))
		require.ErrorIs(t, err, ErrAPIKeyPasswordInvalid)
	}
	result, err := svc.VerifyForCreate(ctx, user.ID, passwordInput("correct-password"))
	require.NoError(t, err)
	require.Equal(t, APIKeyFactorPassword, result.Factor)
}

func TestAPIKeyPasswordRateLimitedErrorRoundsRetryAfterUp(t *testing.T) {
	require.Equal(t, 2, RetryAfterSecondsFromError(apiKeyPasswordRateLimited(1100*time.Millisecond)))
	require.Equal(t, 60, RetryAfterSecondsFromError(apiKeyPasswordRateLimited(0)), "unknown window falls back to the full minute")
	require.Equal(t, 1, RetryAfterSecondsFromError(apiKeyPasswordRateLimited(time.Millisecond)))
}
