//go:build unit

package config

import (
	"fmt"
	"reflect"
	"strings"
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
	require.Equal(t, 100, cb.FastTripMinRequests)

	rg := cfg.Gateway.RetryGuard
	require.Equal(t, 30, rg.MaxAttempts, "filling schedulers must be able to try every candidate before giving up; set 1024 for the previous near-unbounded behavior")
	require.Equal(t, 120, rg.MaxElapsedSeconds)
	require.Equal(t, 100, rg.BackoffBaseMs)
	require.Equal(t, 1000, rg.BackoffMaxMs)
	require.True(t, rg.RetryAfterEnabled)
	require.Equal(t, 5, rg.RetryAfterMinSeconds)
	require.Equal(t, 15, rg.RetryAfterMaxSeconds)
}

func TestAccountCircuitBreakerAndRetryGuardEnvOverrides(t *testing.T) {
	resetViperWithJWTSecret(t)
	t.Setenv("GATEWAY_ACCOUNT_CIRCUIT_BREAKER_ENABLED", "false")
	t.Setenv("GATEWAY_ACCOUNT_CIRCUIT_BREAKER_WINDOW_SECONDS", "120")
	t.Setenv("GATEWAY_ACCOUNT_CIRCUIT_BREAKER_ERROR_RATE", "0.9")
	t.Setenv("GATEWAY_ACCOUNT_CIRCUIT_BREAKER_MIN_REQUESTS", "50")
	t.Setenv("GATEWAY_ACCOUNT_CIRCUIT_BREAKER_COOLDOWN_SECONDS", "30")
	t.Setenv("GATEWAY_ACCOUNT_CIRCUIT_BREAKER_FAST_TRIP_MIN_REQUESTS", "0")
	t.Setenv("GATEWAY_RETRY_GUARD_MAX_ATTEMPTS", "4")
	t.Setenv("GATEWAY_RETRY_GUARD_RETRY_AFTER_ENABLED", "false")
	t.Setenv("GATEWAY_RETRY_GUARD_RETRY_AFTER_MAX_SECONDS", "12")

	cfg, err := Load()
	require.NoError(t, err)

	cb := cfg.Gateway.AccountCircuitBreaker
	require.False(t, cb.Enabled)
	require.Equal(t, 120, cb.WindowSeconds)
	require.Equal(t, 0.9, cb.ErrorRate)
	require.Equal(t, 50, cb.MinRequests)
	require.Equal(t, 30, cb.CooldownSeconds)
	require.Equal(t, 0, cb.FastTripMinRequests, "an explicit 0 turns the fast trip off (the default 100 only applies when the key is absent)")
	require.Equal(t, 4, cfg.Gateway.RetryGuard.MaxAttempts)
	require.False(t, cfg.Gateway.RetryGuard.RetryAfterEnabled)
	require.Equal(t, 12, cfg.Gateway.RetryGuard.RetryAfterMaxSeconds)
}

// 每个 gateway.account_circuit_breaker.* / gateway.retry_guard.* 键都必须能被环境变量覆盖：
// viper 只会为已注册默认值的键读取环境变量，新增字段忘了 SetDefault 时环境变量会被静默丢弃。
// 这里按 mapstructure 标签逐个字段反射：给每个键设一个与默认值不同的环境变量，Load 之后必须读得到。
func TestAccountCircuitBreakerAndRetryGuardEveryKeyIsReachableFromEnv(t *testing.T) {
	resetViperWithJWTSecret(t)
	defaults, err := Load()
	require.NoError(t, err)

	type section struct {
		envPrefix string
		get       func(*Config) reflect.Value
	}
	sections := []section{
		{"GATEWAY_ACCOUNT_CIRCUIT_BREAKER_", func(c *Config) reflect.Value { return reflect.ValueOf(c.Gateway.AccountCircuitBreaker) }},
		{"GATEWAY_RETRY_GUARD_", func(c *Config) reflect.Value { return reflect.ValueOf(c.Gateway.RetryGuard) }},
	}

	// 先为每个字段挑一个与默认值不同的新值，并记下期望。
	type expectation struct {
		section int
		field   int
		want    any
		name    string
	}
	var expectations []expectation
	for si, s := range sections {
		v := s.get(defaults)
		typ := v.Type()
		for i := 0; i < typ.NumField(); i++ {
			tag := typ.Field(i).Tag.Get("mapstructure")
			require.NotEmpty(t, tag, "field %s.%s has no mapstructure tag", typ.Name(), typ.Field(i).Name)
			env := s.envPrefix + strings.ToUpper(tag)
			var raw string
			var want any
			switch f := v.Field(i); f.Kind() {
			case reflect.Bool:
				want = !f.Bool()
				raw = fmt.Sprintf("%t", want)
			case reflect.Int:
				want = int(f.Int()) + 7
				raw = fmt.Sprintf("%d", want)
			case reflect.Float64:
				want = 0.5
				if f.Float() == 0.5 {
					want = 0.25
				}
				raw = fmt.Sprintf("%v", want)
			default:
				t.Fatalf("unsupported field kind %s for %s: extend this guard", f.Kind(), env)
			}
			t.Setenv(env, raw)
			expectations = append(expectations, expectation{section: si, field: i, want: want, name: env})
		}
	}

	resetViperWithJWTSecret(t)
	cfg, err := Load()
	require.NoError(t, err)
	for _, e := range expectations {
		got := sections[e.section].get(cfg).Field(e.field).Interface()
		require.Equal(t, e.want, got, "%s must override the default (is the key registered with viper.SetDefault?)", e.name)
	}
}
