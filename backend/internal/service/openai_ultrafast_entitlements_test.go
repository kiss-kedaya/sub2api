package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func ultrafastAccount(plan string) *Account {
	return &Account{
		ID:          1,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Credentials: map[string]any{"plan_type": plan},
	}
}

// 未配置放行列表 → 默认不限制（与改造前行为一致）。
func TestOpenAIUltrafastEntitledDefaultsToUnrestricted(t *testing.T) {
	t.Setenv(openAIUltrafastEntitlementsEnv, "")
	for _, plan := range []string{"pro", "promax", "plus", "free", ""} {
		require.True(t, openAIUltrafastEntitled(ultrafastAccount(plan), "gpt-6.2-sol"), plan)
		require.True(t, openAIUltrafastEntitled(ultrafastAccount(plan), "gpt-6.2-astra"), plan)
	}

	// 解析失败的配置同样按「未配置」放行，避免一处 JSON 手误全站停用。
	t.Setenv(openAIUltrafastEntitlementsEnv, `{"plan":`)
	for _, plan := range []string{"pro", "promax"} {
		require.True(t, openAIUltrafastEntitled(ultrafastAccount(plan), "gpt-6.2-astra"), plan)
	}
}

// 配置了放行列表后，只放行列出的 套餐 × 模型 组合。
func TestOpenAIUltrafastEntitledAllowlist(t *testing.T) {
	t.Setenv(openAIUltrafastEntitlementsEnv, `[
		{"plan":"pro","models":["gpt-6.2-sol"]},
		{"plan":"promax","models":["gpt-6.2-sol","gpt-6.2-astra"]}
	]`)

	cases := []struct {
		plan   string
		model  string
		expect bool
	}{
		{"pro", "gpt-6.2-sol", true},
		{"pro", "gpt-6.2-astra", false},
		{"promax", "gpt-6.2-sol", true},
		{"promax", "gpt-6.2-astra", true},
		{"plus", "gpt-6.2-sol", false},
		{"", "gpt-6.2-sol", false},
	}
	for _, tc := range cases {
		require.Equal(t, tc.expect, openAIUltrafastEntitled(ultrafastAccount(tc.plan), tc.model),
			"plan=%q model=%q", tc.plan, tc.model)
	}
}

// 套餐名归一化：大小写、空格、"-"、"_" 以及 chatgpt 前缀都视为同一档。
func TestOpenAIUltrafastEntitledPlanNormalization(t *testing.T) {
	t.Setenv(openAIUltrafastEntitlementsEnv, `[{"plan":"ChatGPT-Pro","models":["gpt-6.2-sol"]}]`)
	require.True(t, openAIUltrafastEntitled(ultrafastAccount("pro"), "gpt-6.2-sol"))
	require.True(t, openAIUltrafastEntitled(ultrafastAccount("chatgpt_pro"), "gpt-6.2-sol"))
	require.True(t, openAIUltrafastEntitled(ultrafastAccount("Pro"), "gpt-6.2-sol"))
	require.False(t, openAIUltrafastEntitled(ultrafastAccount("promax"), "gpt-6.2-sol"))
}

// 空 plan = 不限制套餐；空 models = 该套餐可用全部 Ultra Fast 模型；支持 "*" 通配。
func TestOpenAIUltrafastEntitledWildcards(t *testing.T) {
	t.Setenv(openAIUltrafastEntitlementsEnv, `[{"models":["gpt-6.2-sol"]}]`)
	require.True(t, openAIUltrafastEntitled(ultrafastAccount("whatever"), "gpt-6.2-sol"))
	require.False(t, openAIUltrafastEntitled(ultrafastAccount("whatever"), "gpt-6.2-astra"))

	t.Setenv(openAIUltrafastEntitlementsEnv, `[{"plan":"promax"}]`)
	require.True(t, openAIUltrafastEntitled(ultrafastAccount("promax"), "gpt-6.2-sol"))
	require.True(t, openAIUltrafastEntitled(ultrafastAccount("promax"), "gpt-6.2-astra"))
	require.False(t, openAIUltrafastEntitled(ultrafastAccount("pro"), "gpt-6.2-astra"))

	t.Setenv(openAIUltrafastEntitlementsEnv, `[{"plan":"*","models":["gpt-6.2-*"]}]`)
	require.True(t, openAIUltrafastEntitled(ultrafastAccount("plus"), "gpt-6.2-sol"))
	require.True(t, openAIUltrafastEntitled(ultrafastAccount("pro"), "gpt-6.2-astra"))
}
