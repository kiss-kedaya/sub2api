package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 登录会话寿命与绑定处置的配置：每枚 refresh token 滑动 14 天、会话绝对上限 90 天、
// 重试宽限 30 秒（上限 120 秒）、绑定不一致默认 rebind。

func TestLoadDefaultSessionLifetimeAndBindingAction(t *testing.T) {
	resetViperWithJWTSecret(t)
	cfg, err := Load()
	require.NoError(t, err)

	require.Equal(t, 14, cfg.JWT.RefreshTokenExpireDays, "boss decision: a refresh token is valid for 14 days of idleness")
	require.Equal(t, 90, cfg.JWT.SessionMaxLifetimeDays)
	require.Equal(t, 30, cfg.JWT.RefreshTokenReuseLeewaySeconds, "a short default: a longer window is easier to dodge")
	require.Equal(t, 120, MaxRefreshTokenReuseLeewaySeconds)
	require.Equal(t, SessionBindingOnMismatchRebind, cfg.Security.SessionBindingOnMismatch)
	require.False(t, cfg.SessionBindingRevokeOnMismatch())
	require.Equal(t, DefaultRefreshTokenExpireDays, cfg.JWT.RefreshTokenExpireDays, "the constants are the single source of the defaults")
	require.Equal(t, DefaultRefreshTokenReuseLeewaySeconds, cfg.JWT.RefreshTokenReuseLeewaySeconds)
}

// 新键都能用环境变量设置（viper 只认注册过默认值的键）。
func TestLoadSessionLifetimeSettingsFromEnv(t *testing.T) {
	resetViperWithJWTSecret(t)
	t.Setenv("JWT_REFRESH_TOKEN_EXPIRE_DAYS", "7")
	t.Setenv("JWT_SESSION_MAX_LIFETIME_DAYS", "30")
	t.Setenv("JWT_REFRESH_TOKEN_REUSE_LEEWAY_SECONDS", "120")
	t.Setenv("SECURITY_SESSION_BINDING_ON_MISMATCH", "revoke")
	cfg, err := Load()
	require.NoError(t, err)

	require.Equal(t, 7, cfg.JWT.RefreshTokenExpireDays)
	require.Equal(t, 30, cfg.JWT.SessionMaxLifetimeDays)
	require.Equal(t, 120, cfg.JWT.RefreshTokenReuseLeewaySeconds)
	require.Equal(t, SessionBindingOnMismatchRevoke, cfg.Security.SessionBindingOnMismatch)
	require.True(t, cfg.SessionBindingRevokeOnMismatch())
}

func TestLoadAcceptsLeewayZeroAsOff(t *testing.T) {
	resetViperWithJWTSecret(t)
	t.Setenv("JWT_REFRESH_TOKEN_REUSE_LEEWAY_SECONDS", "0")
	cfg, err := Load()
	require.NoError(t, err)
	require.Zero(t, cfg.JWT.RefreshTokenReuseLeewaySeconds)
}

// 升级兼容：旧版本把 refresh_token_expire_days 同时当会话绝对上限，老部署可能只把它调大过（比如 120 天）
// 而没设 session_max_lifetime_days（默认 90）。升级后启动不能失败——会话上限自动抬高到相等。
func TestLoadRaisesTheSessionCapInsteadOfFailingWhenRefreshDaysWasRaisedAlone(t *testing.T) {
	resetViperWithJWTSecret(t)
	t.Setenv("JWT_REFRESH_TOKEN_EXPIRE_DAYS", "120")
	cfg, err := Load()
	require.NoError(t, err, "an upgrade that only ever raised refresh_token_expire_days must still start")
	require.Equal(t, 120, cfg.JWT.RefreshTokenExpireDays)
	require.Equal(t, 120, cfg.JWT.SessionMaxLifetimeDays, "the session cap is raised to match one token's validity")
}

func TestValidateSessionLifetimeRules(t *testing.T) {
	cases := []struct {
		name         string
		mutate       func(*Config)
		wantErr      string
		wantSessions int // 校验之后期望的会话上限（0 = 不检查）
	}{
		{"session cap shorter than one token is raised, not rejected", func(c *Config) { c.JWT.SessionMaxLifetimeDays = 13 }, "", 14},
		{"session cap equal to the sliding window is fine", func(c *Config) { c.JWT.SessionMaxLifetimeDays = 14 }, "", 14},
		{"session cap must be positive", func(c *Config) { c.JWT.SessionMaxLifetimeDays = 0 }, "jwt.session_max_lifetime_days must be positive", 0},
		{"refresh days must be positive", func(c *Config) { c.JWT.RefreshTokenExpireDays = 0 }, "jwt.refresh_token_expire_days must be positive", 0},
		{"session cap above a year only warns", func(c *Config) { c.JWT.SessionMaxLifetimeDays = 400 }, "", 400},
		{"negative leeway", func(c *Config) { c.JWT.RefreshTokenReuseLeewaySeconds = -1 }, "jwt.refresh_token_reuse_leeway_seconds must be non-negative", 0},
		{"leeway at the cap", func(c *Config) { c.JWT.RefreshTokenReuseLeewaySeconds = MaxRefreshTokenReuseLeewaySeconds }, "", 0},
		{"leeway above the cap would defeat replay detection", func(c *Config) { c.JWT.RefreshTokenReuseLeewaySeconds = MaxRefreshTokenReuseLeewaySeconds + 1 }, "must be <= 120", 0},
		{"the old 600 second cap is gone", func(c *Config) { c.JWT.RefreshTokenReuseLeewaySeconds = 600 }, "must be <= 120", 0},
		{"unknown mismatch action", func(c *Config) { c.Security.SessionBindingOnMismatch = "ignore" }, "security.session_binding_on_mismatch must be one of: rebind, revoke", 0},
		{"mismatch action is case-insensitive", func(c *Config) { c.Security.SessionBindingOnMismatch = " Revoke " }, "", 0},
		{"empty mismatch action means rebind", func(c *Config) { c.Security.SessionBindingOnMismatch = "" }, "", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetViperWithJWTSecret(t)
			cfg, err := Load()
			require.NoError(t, err)
			tc.mutate(cfg)

			err = cfg.Validate()
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			if tc.wantSessions != 0 {
				require.Equal(t, tc.wantSessions, cfg.JWT.SessionMaxLifetimeDays)
			}
		})
	}
}

// 归一化：空值与 rebind 都是 rebind；revoke 大小写不敏感；手写 Config 的零值按默认 rebind 处理，
// 不会把测试或旧部署意外踢下线。
func TestSessionBindingOnMismatchNormalization(t *testing.T) {
	for _, in := range []string{"", "rebind", " REBIND "} {
		got, err := NormalizeSessionBindingOnMismatch(in)
		require.NoError(t, err)
		require.Equal(t, SessionBindingOnMismatchRebind, got)
	}
	got, err := NormalizeSessionBindingOnMismatch("Revoke")
	require.NoError(t, err)
	require.Equal(t, SessionBindingOnMismatchRevoke, got)
	_, err = NormalizeSessionBindingOnMismatch("kick")
	require.Error(t, err)

	require.False(t, (&Config{}).SessionBindingRevokeOnMismatch())
	require.False(t, (*Config)(nil).SessionBindingRevokeOnMismatch())
	require.False(t, (&Config{Security: SecurityConfig{SessionBindingOnMismatch: "garbage"}}).SessionBindingRevokeOnMismatch())
	require.True(t, (&Config{Security: SecurityConfig{SessionBindingOnMismatch: "revoke"}}).SessionBindingRevokeOnMismatch())
}
