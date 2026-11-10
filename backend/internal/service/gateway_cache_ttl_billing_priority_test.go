package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// 计费侧的缓存 TTL 优先级：密钥 > 账号 > 全局。
//
// 之前计费只看账号和全局，密钥级设置只影响发给上游的请求体，于是同一把
// 「强制 1h」的密钥在全局开关开着时按 5m 计费、关着时按 1h 计费；密钥选「关闭」
// 而全局开着时，请求没被改写，计费却仍被归到 5m。
func TestResolveCacheTTLUsageOverrideTarget_KeyThenAccountThenGlobal(t *testing.T) {
	oauth := func(extra map[string]any) *Account {
		return &Account{Platform: PlatformAnthropic, Type: AccountTypeOAuth, Extra: extra}
	}
	accountOverride := func(target string) map[string]any {
		return map[string]any{"cache_ttl_override_enabled": true, "cache_ttl_override_target": target}
	}
	key := func(mode string) *APIKey { return &APIKey{AnthropicCacheTTLMode: mode} }

	cases := []struct {
		name       string
		global1h   bool
		apiKey     *APIKey
		account    *Account
		wantTarget string
		wantOK     bool
	}{
		// 密钥说了算
		{"key 1h beats global-on (was billed 5m)", true, key(AnthropicCacheTTLMode1h), oauth(nil), cacheTTLTarget1h, true},
		{"key 1h with global off", false, key(AnthropicCacheTTLMode1h), oauth(nil), cacheTTLTarget1h, true},
		{"key 5m beats account 1h", false, key(AnthropicCacheTTLMode5m), oauth(accountOverride("1h")), cacheTTLTarget5m, true},
		{"key 1h beats account 5m", true, key(AnthropicCacheTTLMode1h), oauth(accountOverride("5m")), cacheTTLTarget1h, true},
		{"key off beats global-on (was billed 5m)", true, key(AnthropicCacheTTLModeOff), oauth(nil), "", false},
		{"key off beats account override", true, key(AnthropicCacheTTLModeOff), oauth(accountOverride("1h")), "", false},

		// 密钥跟随全局：账号其次
		{"inherit: account 1h beats global-on", true, key(AnthropicCacheTTLModeInherit), oauth(accountOverride("1h")), cacheTTLTarget1h, true},
		{"inherit: account 5m with global off", false, key(AnthropicCacheTTLModeInherit), oauth(accountOverride("5m")), cacheTTLTarget5m, true},
		{"empty mode is inherit", true, key(""), oauth(accountOverride("1h")), cacheTTLTarget1h, true},

		// 密钥和账号都没说：全局最后
		{"inherit: global-on bills 5m", true, key(AnthropicCacheTTLModeInherit), oauth(nil), cacheTTLTarget5m, true},
		{"inherit: nothing set", false, key(AnthropicCacheTTLModeInherit), oauth(nil), "", false},

		// 没有密钥（内部调用）：行为与改动前一致
		{"no key: account override", true, nil, oauth(accountOverride("1h")), cacheTTLTarget1h, true},
		{"no key: global-on", true, nil, oauth(nil), cacheTTLTarget5m, true},
		{"no key: nothing set", false, nil, oauth(nil), "", false},
		{"no key, no account", true, nil, nil, "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			value := "false"
			if tc.global1h {
				value = "true"
			}
			repo := &gatewayTTLSettingRepo{data: map[string]string{SettingKeyEnableAnthropicCacheTTL1hInjection: value}}
			gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{})
			svc := &GatewayService{settingService: NewSettingService(repo, &config.Config{})}

			target, ok := svc.resolveCacheTTLUsageOverrideTarget(context.Background(), tc.apiKey, tc.account)
			require.Equal(t, tc.wantOK, ok)
			require.Equal(t, tc.wantTarget, target)
		})
	}
}

// 密钥级设置只对落到 Anthropic 平台的请求生效，与请求侧改写的范围一致。
func TestResolveCacheTTLUsageOverrideTarget_KeyScopeIsAnthropicOnly(t *testing.T) {
	repo := &gatewayTTLSettingRepo{data: map[string]string{SettingKeyEnableAnthropicCacheTTL1hInjection: "false"}}
	gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{})
	svc := &GatewayService{settingService: NewSettingService(repo, &config.Config{})}
	forced := &APIKey{AnthropicCacheTTLMode: AnthropicCacheTTLMode1h}

	// OpenAI 账号、密钥不属于 Anthropic 分组：密钥设置不参与。
	_, ok := svc.resolveCacheTTLUsageOverrideTarget(context.Background(), forced, &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth})
	require.False(t, ok)

	// Anthropic API Key 账号：账号级开关不适用（它只认 OAuth/SetupToken），但密钥级设置适用，
	// 因为请求侧对这类账号同样会按密钥改写。
	target, ok := svc.resolveCacheTTLUsageOverrideTarget(context.Background(), forced, &Account{Platform: PlatformAnthropic, Type: AccountTypeAPIKey})
	require.True(t, ok)
	require.Equal(t, cacheTTLTarget1h, target)

	// 账号平台不是 Anthropic，但密钥所属分组是：视为落到 Anthropic。
	grouped := &APIKey{AnthropicCacheTTLMode: AnthropicCacheTTLMode5m, Group: &Group{Platform: PlatformAnthropic}}
	target, ok = svc.resolveCacheTTLUsageOverrideTarget(context.Background(), grouped, &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth})
	require.True(t, ok)
	require.Equal(t, cacheTTLTarget5m, target)
}
