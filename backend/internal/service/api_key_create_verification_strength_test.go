//go:build unit

package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// 评审 PR #688 之后补的测试：保护强度序（on > auto > off）、「没有记录 → off」、
// 写库成功之后回包不再读库。

// 强度序本身钉死：on(2) > auto(1) > off(0)，未知值按 auto；手写的下降表与强度函数逐格一致。
func TestAPIKeyCreateVerificationStrengthOrder(t *testing.T) {
	require.Equal(t, 2, apiKeyCreateVerificationStrength(APIKeyCreateVerificationOn))
	require.Equal(t, 1, apiKeyCreateVerificationStrength(APIKeyCreateVerificationAuto))
	require.Equal(t, 0, apiKeyCreateVerificationStrength(APIKeyCreateVerificationOff))
	for _, unknown := range []string{"", "banana", "ON", " on"} {
		require.Equal(t, 1, apiKeyCreateVerificationStrength(unknown), "%q behaves as auto", unknown)
	}
	for _, from := range cvModes {
		for _, to := range cvModes {
			require.Equal(t,
				apiKeyCreateVerificationStrength(to) < apiKeyCreateVerificationStrength(from),
				cvLowered[from][to], "%s -> %s", from, to)
		}
	}
}

// 「没有记录」就是 auto：直接写 off 同样是强度下降（auto → off）。有任何可用验证方式的账号都必须验证身份。
func TestSetCreateVerificationModeNoRecordToOffNeedsVerification(t *testing.T) {
	ctx := context.Background()
	for _, acct := range cvAccounts {
		if !acct.hasFactor() {
			continue
		}
		t.Run(acct.name, func(t *testing.T) {
			env := newCVEnv(t, acct, "") // 没有记录
			require.Empty(t, env.prefs.rows)

			change, err := env.svc.SetCreateVerificationMode(ctx, cvUserID, APIKeyCreateVerificationOff, nil)
			requireRelaxRequired(t, err)
			require.Nil(t, change)
			require.Empty(t, env.prefs.rows, "nothing is stored by an unverified request")
			require.Empty(t, env.prefs.swapLog())

			change, err = env.svc.SetCreateVerificationMode(ctx, cvUserID, APIKeyCreateVerificationOff, env.credential(t, acct.primary))
			require.NoError(t, err)
			require.True(t, change.Relaxed)
			require.Equal(t, APIKeyCreateVerificationAuto, change.From)
			require.Equal(t, APIKeyCreateVerificationOff, env.prefs.label(cvUserID))
		})
	}
}

// 被劫持的会话能做的只有「升高」：任何账号、任何起点，不带校验的写入都只能让强度上升或持平。
func TestSetCreateVerificationModeUnverifiedWritesCanOnlyRaiseTheStrength(t *testing.T) {
	ctx := context.Background()
	for _, acct := range cvAccounts {
		if !acct.hasFactor() {
			continue
		}
		for _, from := range cvModes {
			for _, to := range cvModes {
				t.Run(fmt.Sprintf("%s/%s->%s", acct.name, from, to), func(t *testing.T) {
					env := newCVEnv(t, acct, from)
					_, err := env.svc.SetCreateVerificationMode(ctx, cvUserID, to, nil)
					if err != nil {
						requireRelaxRequired(t, err)
					}
					after := env.prefs.label(cvUserID)
					require.GreaterOrEqual(t, apiKeyCreateVerificationStrength(after), apiKeyCreateVerificationStrength(from),
						"an unverified request must never leave the stored strength lower than it found it")
				})
			}
		}
	}
}

// failAfterWritePrefs 在第一次成功写入之后让所有读取失败（模拟「写成功了、之后库抖了一下」）。
type failAfterWritePrefs struct {
	*cvPrefs
}

func (p failAfterWritePrefs) CompareAndSetAPIKeyCreateVerificationMode(ctx context.Context, userID int64, expected, next string) (bool, error) {
	ok, err := p.cvPrefs.CompareAndSetAPIKeyCreateVerificationMode(ctx, userID, expected, next)
	if ok && err == nil {
		p.cvPrefs.mu.Lock()
		p.cvPrefs.getErr = fmt.Errorf("db hiccup after the write")
		p.cvPrefs.mu.Unlock()
	}
	return ok, err
}

// 评审 L1：写库成功之后如果还要再读一次才能回包，这一读失败调用方就拿到 5xx，但开关其实已经改了。
// 现在服务层直接给出改后的视图：写库成功就一定成功返回，而且写库之后不再读库。
func TestSetCreateVerificationModeSucceedsAndAnswersWithoutReadingAfterTheWrite(t *testing.T) {
	ctx := context.Background()
	env := newCVEnv(t, cvWithTOTP, APIKeyCreateVerificationAuto)
	env.svc.SetCreateVerificationPreferences(failAfterWritePrefs{cvPrefs: env.prefs})

	change, err := env.svc.SetCreateVerificationMode(ctx, cvUserID, APIKeyCreateVerificationOff, env.credential(t, APIKeyFactorTOTP))
	require.NoError(t, err, "the write committed, so the call must report success")
	require.True(t, change.Changed)
	require.True(t, change.Relaxed)
	require.Equal(t, APIKeyCreateVerificationOff, env.prefs.label(cvUserID))
	require.Equal(t, 1, env.prefs.getCalls, "exactly one read (the one the decision is based on); none after the write")

	// 回包用的视图：已经是改后的有效状态。
	require.NotNil(t, change.Factors)
	require.Equal(t, APIKeyCreateVerificationOff, change.Factors.CreateVerificationMode)
	require.False(t, change.Factors.CreateVerificationRequired)
	require.True(t, change.Factors.CreateVerificationDefaultRequired)
	require.True(t, change.Factors.TOTPEnabled)

	// 对照：之后单独去读（比如前端刷新）确实会失败——这正是不能让回包依赖它的原因。
	_, err = env.svc.Factors(ctx, cvUserID)
	require.Error(t, err)
}

// 评审的原始复现：第二次读取失败（handler 以前在写库之后做的那一读）不影响结果。
type failOnNthGetPrefs struct {
	*cvPrefs
	n, calls int
}

func (p *failOnNthGetPrefs) GetAPIKeyCreateVerificationMode(ctx context.Context, userID int64) (string, error) {
	p.calls++
	if p.calls == p.n {
		return "", context.DeadlineExceeded
	}
	return p.cvPrefs.GetAPIKeyCreateVerificationMode(ctx, userID)
}

func TestSetCreateVerificationModeIgnoresAFailingSecondRead(t *testing.T) {
	ctx := context.Background()
	env := newCVEnv(t, cvWithTOTP, APIKeyCreateVerificationAuto)
	env.svc.SetCreateVerificationPreferences(&failOnNthGetPrefs{cvPrefs: env.prefs, n: 2})

	change, err := env.svc.SetCreateVerificationMode(ctx, cvUserID, APIKeyCreateVerificationOff, env.credential(t, APIKeyFactorTOTP))
	require.NoError(t, err)
	require.True(t, change.Relaxed)
	require.NotNil(t, change.Factors)
	require.Equal(t, APIKeyCreateVerificationOff, change.Factors.CreateVerificationMode)
	require.Equal(t, APIKeyCreateVerificationOff, env.prefs.label(cvUserID))
}
