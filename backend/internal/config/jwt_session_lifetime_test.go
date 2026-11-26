package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 登录会话寿命与绑定处置的配置：每枚 refresh token 滑动 14 天、会话绝对上限 90 天、
// 重试宽限 60 秒、绑定不一致默认 rebind。

func TestLoadDefaultSessionLifetimeAndBindingAction(t *testing.T) {
	resetViperWithJWTSecret(t)
	cfg, err := Load()
	require.NoError(t, err)

	require.Equal(t, 14, cfg.JWT.RefreshTokenExpireDays, "boss decision: a refresh token is valid for 14 days of idleness")
	require.Equal(t, 90, cfg.JWT.SessionMaxLifetimeDays)
	require.Equal(t, 60, cfg.JWT.RefreshTokenReuseLeewaySeconds)
	require.Equal(t, SessionBindingOnMismatchRebind, cfg.Security.SessionBindingOnMismatch)
	require.False(t, cfg.SessionBindingRevokeOnMismatch())
	require.Equal(t, DefaultRefreshTokenExpireDays, cfg.JWT.RefreshTokenExpireDays, "the constants are the single source of the defaults")
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

func TestValidateSessionLifetimeRules(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{"session cap shorter than one token", func(c *Config) { c.JWT.SessionMaxLifetimeDays = 13 }, "jwt.session_max_lifetime_days (13) must be >= jwt.refresh_token_expire_days (14)"},
		{"session cap equal to the sliding window is fine", func(c *Config) { c.JWT.SessionMaxLifetimeDays = 14 }, ""},
		{"session cap must be positive", func(c *Config) { c.JWT.SessionMaxLifetimeDays = 0 }, "jwt.session_max_lifetime_days must be positive"},
		{"refresh days must be positive", func(c *Config) { c.JWT.RefreshTokenExpireDays = 0 }, "jwt.refresh_token_expire_days must be positive"},
		{"session cap above a year only warns", func(c *Config) { c.JWT.SessionMaxLifetimeDays = 400 }, ""},
		{"negative leeway", func(c *Config) { c.JWT.RefreshTokenReuseLeewaySeconds = -1 }, "jwt.refresh_token_reuse_leeway_seconds must be non-negative"},
		{"leeway at the cap", func(c *Config) { c.JWT.RefreshTokenReuseLeewaySeconds = MaxRefreshTokenReuseLeewaySeconds }, ""},
		{"leeway above the cap would defeat replay detection", func(c *Config) { c.JWT.RefreshTokenReuseLeewaySeconds = MaxRefreshTokenReuseLeewaySeconds + 1 }, "must be <= 600"},
		{"unknown mismatch action", func(c *Config) { c.Security.SessionBindingOnMismatch = "ignore" }, "security.session_binding_on_mismatch must be one of: rebind, revoke"},
		{"mismatch action is case-insensitive", func(c *Config) { c.Security.SessionBindingOnMismatch = " Revoke " }, ""},
		{"empty mismatch action means rebind", func(c *Config) { c.Security.SessionBindingOnMismatch = "" }, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetViperWithJWTSecret(t)
			cfg, err := Load()
			require.NoError(t, err)
			tc.mutate(cfg)

			err = cfg.Validate()
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tc.wantErr)
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
