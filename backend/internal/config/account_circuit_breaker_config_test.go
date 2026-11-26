//go:build unit

package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 账号错误率断路器与重试硬上限的默认值必须和文档一致，并且能被环境变量覆盖
// （viper 只解码已注册默认值的键，没注册的键环境变量会被静默丢弃）。
func TestAccountCircuitBreakerAndRetryGuardDefaults(t *testing.T) {
	resetViperWithJWTSecret(t)

	cfg, err := Load()
	require.NoError(t, err)

	cb := cfg.Gateway.AccountCircuitBreaker
	require.True(t, cb.Enabled, "the breaker is on by default")
	require.Equal(t, 300, cb.WindowSeconds)
	require.Equal(t, 0.95, cb.ErrorRate)
	require.Equal(t, 20, cb.MinRequests)
	require.Equal(t, 60, cb.CooldownSeconds)
	require.False(t, cb.BackoffEnabled, "exponential cooldown is opt-in")
	require.Equal(t, 300, cb.MaxCooldownSeconds)
	require.Equal(t, 3, cb.ValveMinAccounts)
	require.Equal(t, 0.5, cb.ValveRatio)

	rg := cfg.Gateway.RetryGuard
	require.Equal(t, 10, rg.MaxAttempts)
	require.Equal(t, 60, rg.MaxElapsedSeconds)
	require.Equal(t, 100, rg.BackoffBaseMs)
	require.Equal(t, 1000, rg.BackoffMaxMs)
	require.Equal(t, 5, rg.RetryAfterMinSeconds)
	require.Equal(t, 30, rg.RetryAfterMaxSeconds)
}

func TestAccountCircuitBreakerAndRetryGuardEnvOverrides(t *testing.T) {
	resetViperWithJWTSecret(t)
	t.Setenv("GATEWAY_ACCOUNT_CIRCUIT_BREAKER_ENABLED", "false")
	t.Setenv("GATEWAY_ACCOUNT_CIRCUIT_BREAKER_WINDOW_SECONDS", "120")
	t.Setenv("GATEWAY_ACCOUNT_CIRCUIT_BREAKER_ERROR_RATE", "0.9")
	t.Setenv("GATEWAY_ACCOUNT_CIRCUIT_BREAKER_MIN_REQUESTS", "50")
	t.Setenv("GATEWAY_ACCOUNT_CIRCUIT_BREAKER_COOLDOWN_SECONDS", "30")
	t.Setenv("GATEWAY_RETRY_GUARD_MAX_ATTEMPTS", "4")
	t.Setenv("GATEWAY_RETRY_GUARD_RETRY_AFTER_MAX_SECONDS", "12")

	cfg, err := Load()
	require.NoError(t, err)

	cb := cfg.Gateway.AccountCircuitBreaker
	require.False(t, cb.Enabled)
	require.Equal(t, 120, cb.WindowSeconds)
	require.Equal(t, 0.9, cb.ErrorRate)
	require.Equal(t, 50, cb.MinRequests)
	require.Equal(t, 30, cb.CooldownSeconds)
	require.Equal(t, 4, cfg.Gateway.RetryGuard.MaxAttempts)
	require.Equal(t, 12, cfg.Gateway.RetryGuard.RetryAfterMaxSeconds)
}
