//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/require"
)

// 审计 P2：API Key 身份校验里的 TOTP 码可以重放（VerifyCode 校验通过后不消耗），
// 被旁观 / 钓鱼拿到的一个码能在 90 秒窗口内反复下发 Key 明文。换成 VerifyCodeOnce 后，
// 同一个（时间步, 码）只能用一次，并与登录共用同一个重放标记。

const keyVerifyTOTPSecret = "JBSWY3DPEHPK3PXP"

func newTOTPKeyVerification(t *testing.T) (*APIKeyVerificationService, *apiKeyVerifyStoreStub, *User) {
	t.Helper()
	user := apiKeyVerifyTestUser(t, "correct-password")
	// 旧密文（无授权因子）：二次查看不限制因子，可以直接用 TOTP 授权。
	svc, store, _ := newAPIKeyVerificationTestService(t, user, apiKeyVerifyTestKey(nil), apiKeyVerifyTestSecret(""), keyVerifyTOTPSecret)
	return svc, store, user
}

// totpCodeForStep 生成「距现在 offset 个 30 秒步」的 TOTP 码，并返回它所属的时间步
// （服务端匹配到的就是这个 counter，与请求到达时的时钟无关）。
func totpCodeForStep(t *testing.T, offset int64) (string, int64) {
	t.Helper()
	at := time.Now().Add(time.Duration(offset) * 30 * time.Second)
	code, err := totp.GenerateCode(keyVerifyTOTPSecret, at)
	require.NoError(t, err)
	return code, at.Unix() / 30
}

func totpInput(code string) *APIKeyVerificationInput {
	return &APIKeyVerificationInput{Method: APIKeyFactorTOTP, TOTPCode: code}
}

// distinctTOTPCodes 取相邻两个时间步的码；它们恰好相同的概率是百万分之一，那一次无法区分，跳过。
func distinctTOTPCodes(t *testing.T) (current, next string) {
	t.Helper()
	current, _ = totpCodeForStep(t, 0)
	next, _ = totpCodeForStep(t, 1)
	if current == next {
		t.Skip("相邻两个时间步恰好生成了同一个码（百万分之一），无法区分")
	}
	return current, next
}

func TestAPIKeyTOTPCodeIsSingleUseAcrossEveryEntry(t *testing.T) {
	ctx := context.Background()
	svc, _, user := newTOTPKeyVerification(t)
	current, next := distinctTOTPCodes(t)

	// 第一次用：创建通过。
	result, err := svc.VerifyForCreate(ctx, user.ID, totpInput(current))
	require.NoError(t, err)
	require.Equal(t, APIKeyFactorTOTP, result.Factor)

	// 同一个码再来一次：创建 / 单条轮换 / 二次查看授权，每个入口都被拒绝。
	_, err = svc.VerifyForCreate(ctx, user.ID, totpInput(current))
	require.ErrorIs(t, err, ErrAPIKeyTOTPInvalid, "create must not accept a used code")
	_, err = svc.VerifyForKeyEdit(ctx, user.ID, 42, totpInput(current))
	require.ErrorIs(t, err, ErrAPIKeyTOTPInvalid, "rotate must not accept a used code")
	_, err = svc.AuthorizeReveal(ctx, user.ID, 42, *totpInput(current))
	require.ErrorIs(t, err, ErrAPIKeyTOTPInvalid, "reveal authorization must not accept a used code")

	// 下一个时间步的新码照常通过，同样只能用一次。
	_, err = svc.VerifyForKeyEdit(ctx, user.ID, 42, totpInput(next))
	require.NoError(t, err)
	_, err = svc.VerifyForCreate(ctx, user.ID, totpInput(next))
	require.ErrorIs(t, err, ErrAPIKeyTOTPInvalid)
}

func TestAPIKeyTOTPSharesTheReplayMarkerWithLogin(t *testing.T) {
	ctx := context.Background()
	svc, _, user := newTOTPKeyVerification(t)

	// 登录先用掉这个码（登录流程在全部校验通过之后才调用 ConsumeLoginCode）：
	// 同一个码不能再拿来下发 Key 明文。
	code, counter := totpCodeForStep(t, 0)
	require.NoError(t, svc.totp.ConsumeLoginCode(ctx, user.ID, counter, code))
	_, err := svc.VerifyForCreate(ctx, user.ID, totpInput(code))
	require.ErrorIs(t, err, ErrAPIKeyTOTPInvalid, "a code that just logged in must not also release a key")

	// 反过来：先校验后登录，登录那边把它当重放。
	next, nextCounter := totpCodeForStep(t, 1)
	if next == code {
		t.Skip("相邻两个时间步恰好生成了同一个码（百万分之一），无法区分")
	}
	_, err = svc.VerifyForCreate(ctx, user.ID, totpInput(next))
	require.NoError(t, err)
	require.ErrorIs(t, svc.totp.ConsumeLoginCode(ctx, user.ID, nextCounter, next), ErrTotpCodeReplayed)
}

func TestAPIKeyTOTPFailedAttemptsDoNotBurnTheRealCode(t *testing.T) {
	ctx := context.Background()
	svc, store, user := newTOTPKeyVerification(t)
	code, _ := totpCodeForStep(t, 0)

	for _, bad := range []string{"", "12345", "abcdef", "1234567", " "} {
		_, err := svc.VerifyForCreate(ctx, user.ID, totpInput(bad))
		require.ErrorIs(t, err, ErrAPIKeyTOTPInvalid, "code %q", bad)
	}
	if code != "000000" {
		_, err := svc.VerifyForCreate(ctx, user.ID, totpInput("000000"))
		require.ErrorIs(t, err, ErrAPIKeyTOTPInvalid)
	}
	require.Empty(t, store.usedCodes, "only a code that verified may be marked as used")

	// 失败不消耗：真正的码随后照常通过。
	_, err := svc.VerifyForCreate(ctx, user.ID, totpInput(code))
	require.NoError(t, err)
	require.Len(t, store.usedCodes, 1)
}

func TestAPIKeyTOTPFailsClosedWhenTheReplayMarkerStoreIsDown(t *testing.T) {
	ctx := context.Background()
	svc, store, user := newTOTPKeyVerification(t)
	code, _ := totpCodeForStep(t, 0)

	store.markErr = errors.New("redis down")
	_, err := svc.VerifyForCreate(ctx, user.ID, totpInput(code))
	require.ErrorIs(t, err, ErrAPIKeyTOTPInvalid, "without a replay marker a code must not be accepted")
	_, err = svc.VerifyForKeyEdit(ctx, user.ID, 42, totpInput(code))
	require.ErrorIs(t, err, ErrAPIKeyTOTPInvalid)

	// 故障恢复后同一个码可以用（故障期间什么都没消耗）。
	store.markErr = nil
	_, err = svc.VerifyForCreate(ctx, user.ID, totpInput(code))
	require.NoError(t, err)
}

func TestAPIKeyTOTPReplayMarkerIsBoundToTheMatchedStep(t *testing.T) {
	ctx := context.Background()
	svc, _, user := newTOTPKeyVerification(t)
	previous, _ := totpCodeForStep(t, -1)
	current, _ := totpCodeForStep(t, 0)
	if previous == current {
		t.Skip("相邻两个时间步恰好生成了同一个码（百万分之一），无法区分")
	}

	// 上一个时间步的码还在 ±1 步的容差内：能用一次，再用就是重放；
	// 当前时间步的码是另一个（时间步, 码），不受影响。
	_, err := svc.VerifyForCreate(ctx, user.ID, totpInput(previous))
	require.NoError(t, err)
	_, err = svc.VerifyForCreate(ctx, user.ID, totpInput(previous))
	require.ErrorIs(t, err, ErrAPIKeyTOTPInvalid)
	_, err = svc.VerifyForCreate(ctx, user.ID, totpInput(current))
	require.NoError(t, err)
}

func TestAPIKeyPasswordFactorIsNotAffectedByTheTOTPReplayMarker(t *testing.T) {
	ctx := context.Background()
	svc, _, user := newTOTPKeyVerification(t)
	code, _ := totpCodeForStep(t, 0)

	_, err := svc.VerifyForCreate(ctx, user.ID, totpInput(code))
	require.NoError(t, err)
	// 密码是静态因子，可以重复使用：不能被误套上「一次性」。
	for i := 0; i < 3; i++ {
		result, err := svc.VerifyForCreate(ctx, user.ID, &APIKeyVerificationInput{Method: APIKeyFactorPassword, Password: "correct-password"})
		require.NoError(t, err)
		require.Equal(t, APIKeyFactorPassword, result.Factor)
	}
}
