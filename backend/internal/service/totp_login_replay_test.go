package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// newPlainLoginTOTPService 构造一个未注入 cryptoSecurity 的 TOTP 服务，
// 模拟"没有配置支付/敏感操作安全能力"的部署：登录重放保护必须独立生效。
func newPlainLoginTOTPService(t *testing.T) *TotpService {
	t.Helper()
	_, _, repo, cache := newCryptoSecurityTestServices(t, true)
	settings := NewSettingService(&totpVMSettingRepoStub{values: map[string]string{
		SettingKeyTotpEnabled: "true",
	}}, nil)
	return NewTotpService(repo, cryptoSecurityTestEncryptor{}, cache, settings, nil, nil)
}

// 登录成功消费的 TOTP 码在同一时间窗内不能再次用于登录，
// 且该保护不依赖支付/安全能力是否就绪。
func TestLoginTOTPCodeReplayRejectedWithoutPaymentSecurity(t *testing.T) {
	svc := newPlainLoginTOTPService(t)
	ctx := context.Background()
	code := cryptoSecurityTestCode(t, cryptoSecurityTestSecret)

	counter, err := svc.VerifyLoginCode(ctx, 7, code)
	require.NoError(t, err)
	require.NoError(t, svc.ConsumeLoginCode(ctx, 7, counter, code), "first use must succeed")

	require.ErrorIs(t, svc.ConsumeLoginCode(ctx, 7, counter, code), ErrTotpCodeReplayed,
		"same code+window must be rejected on replay")
}

// 重放键按「用户 + 时间步 + 验证码」隔离：不同用户、不同时间窗互不影响。
func TestLoginTOTPReplayMarkerIsPerUserAndPerWindow(t *testing.T) {
	svc := newPlainLoginTOTPService(t)
	ctx := context.Background()
	code := cryptoSecurityTestCode(t, cryptoSecurityTestSecret)

	counter, err := svc.VerifyLoginCode(ctx, 7, code)
	require.NoError(t, err)
	require.NoError(t, svc.ConsumeLoginCode(ctx, 7, counter, code))

	// 另一个用户使用同一个码不受影响。
	require.NoError(t, svc.ConsumeLoginCode(ctx, 8, counter, code))

	// 不同时间步是不同的码槽（窗口外允许），不受上一窗口标记影响。
	require.NoError(t, svc.ConsumeLoginCode(ctx, 7, counter+1, code))
}

// 配置了支付/安全能力的部署仍同时消费支付重放标记：
// 同一个码完成登录后不能再次用于资金操作，双标记语义保持不变。
func TestLoginReplayMarkerKeepsPaymentReplayProtection(t *testing.T) {
	security, svc, _, _ := newCryptoSecurityTestServices(t, true)
	ctx := context.Background()
	code := cryptoSecurityTestCode(t, cryptoSecurityTestSecret)

	counter, err := svc.VerifyLoginCode(ctx, 7, code)
	require.NoError(t, err)
	require.NoError(t, svc.ConsumeLoginCode(ctx, 7, counter, code))

	require.ErrorIs(t, security.VerifyRisk(ctx, 7, code), ErrCryptoTotpReplayed)
	require.ErrorIs(t, svc.ConsumeLoginCode(ctx, 7, counter, code), ErrTotpCodeReplayed)
}
