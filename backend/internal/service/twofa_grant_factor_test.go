//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 因子凭证互相隔离：TOTP 凭证不能被当成 Passkey 凭证复用（fail-closed）。
func TestTwoFactorGrantFactorIsolation(t *testing.T) {
	ctx := context.Background()
	cache := newTwoFactorGrantTestCache()
	svc := newTwoFactorGrantTestService(t, cache)
	binding := twoFactorGrantTestBinding()

	totpGrant, err := svc.IssueTwoFactorGrant(ctx, 42, TwoFactorGrantScopeLogin, FactorTOTP, binding)
	require.NoError(t, err)

	_, err = svc.ConsumeTwoFactorGrant(ctx, totpGrant.Token, 42, FactorPasskey, binding)
	require.ErrorIs(t, err, errTwoFactorGrantUnknown)

	// 用正确的因子仍可正常消费（证明是因子不匹配而不是凭证坏了）。
	consumed, err := svc.ConsumeTwoFactorGrant(ctx, totpGrant.Token, 42, FactorTOTP, binding)
	require.NoError(t, err)
	require.Equal(t, FactorTOTP, consumed.Factor)
}

// 两个因子各自独立签发 5 分钟凭证，互不覆盖、互不消耗。
func TestTwoFactorGrantFactorsAreIndependent(t *testing.T) {
	ctx := context.Background()
	cache := newTwoFactorGrantTestCache()
	svc := newTwoFactorGrantTestService(t, cache)
	binding := twoFactorGrantTestBinding()

	totpGrant, err := svc.IssueTwoFactorGrant(ctx, 42, TwoFactorGrantScopePassword, FactorTOTP, binding)
	require.NoError(t, err)
	passkeyGrant, err := svc.IssueTwoFactorGrant(ctx, 42, TwoFactorGrantScopePassword, FactorPasskey, binding)
	require.NoError(t, err)
	require.NotEqual(t, totpGrant.Token, passkeyGrant.Token)

	// Passkey 被消费/重签后，TOTP 凭证仍然有效。
	passkeyUsed, err := svc.ConsumeTwoFactorGrant(ctx, passkeyGrant.Token, 42, FactorPasskey, binding)
	require.NoError(t, err)
	require.Equal(t, FactorPasskey, passkeyUsed.Factor)

	totpUsed, err := svc.ConsumeTwoFactorGrant(ctx, totpGrant.Token, 42, FactorTOTP, binding)
	require.NoError(t, err)
	require.Equal(t, FactorTOTP, totpUsed.Factor)

	// 两个凭证的 TTL 都是 5 分钟（同一窗口语义）。
	require.LessOrEqual(t, totpUsed.Remaining, TwoFactorGrantTTL)
	require.Greater(t, totpUsed.Remaining, 4*time.Minute)
}

// 非法因子不允许签发/消费。
func TestTwoFactorGrantRejectsUnknownFactor(t *testing.T) {
	ctx := context.Background()
	svc := newTwoFactorGrantTestService(t, newTwoFactorGrantTestCache())
	binding := twoFactorGrantTestBinding()

	_, err := svc.IssueTwoFactorGrant(ctx, 42, TwoFactorGrantScopeLogin, Factor("sms"), binding)
	require.ErrorIs(t, err, ErrTwoFactorGrantUnavailable)

	_, err = svc.ConsumeTwoFactorGrant(ctx, "token", 42, Factor("sms"), binding)
	require.Error(t, err)
}

// 缓存里记录的因子非法（被篡改/旧数据）时按未知凭证拒绝。
func TestTwoFactorGrantRejectsTamperedFactor(t *testing.T) {
	ctx := context.Background()
	cache := newTwoFactorGrantTestCache()
	svc := newTwoFactorGrantTestService(t, cache)
	binding := twoFactorGrantTestBinding()

	grant, err := svc.IssueTwoFactorGrant(ctx, 42, TwoFactorGrantScopeLogin, FactorTOTP, binding)
	require.NoError(t, err)

	digest := twoFactorGrantDigest(grant.Token)
	record := cache.records[digest]
	require.NotNil(t, record)
	record.Factor = "sms"

	_, err = svc.ConsumeTwoFactorGrant(ctx, grant.Token, 42, FactorTOTP, binding)
	require.ErrorIs(t, err, errTwoFactorGrantUnknown)
}
