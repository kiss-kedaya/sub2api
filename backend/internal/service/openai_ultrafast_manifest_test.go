package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func codexTierIDs(t *testing.T, capabilities map[string]json.RawMessage) []string {
	t.Helper()
	raw := capabilities["service_tiers"]
	require.NotEmpty(t, raw, "service_tiers must be advertised")
	var tiers []map[string]any
	require.NoError(t, json.Unmarshal(raw, &tiers))
	ids := make([]string, 0, len(tiers))
	for _, tier := range tiers {
		if id, ok := tier["id"].(string); ok {
			ids = append(ids, id)
		}
	}
	return ids
}

func codexSubscriptionAccount(plan string) *Account {
	account := ultrafastAccount(plan)
	account.Credentials["base_url"] = "https://chatgpt.com/backend-api/codex"
	return account
}

// 默认不限制：OAuth 订阅账号的 GPT-6.2 目录同时声明 Fast 与 Ultra Fast，
// 档位描述里的倍率与计费规则同源。
func TestAccountCodexToolCapabilitiesAdvertisesGPT62Ultrafast(t *testing.T) {
	t.Setenv(openAIUltrafastEntitlementsEnv, "")

	for _, model := range []string{"gpt-6.2-sol", "gpt-6.2-astra", "gpt-6.2-sol-high"} {
		capabilities := accountCodexToolCapabilities(codexSubscriptionAccount("pro"), model)
		require.Equal(t, []string{OpenAIFastTierPriority, OpenAIFastTierUltrafast}, codexTierIDs(t, capabilities), model)
		require.Contains(t, string(capabilities["service_tiers"]), "Standard token pricing", model)
	}
}

// 配置了放行白名单后，未列出套餐的账号目录不再声明 Ultra Fast。
func TestAccountCodexToolCapabilitiesRespectsUltrafastAllowlist(t *testing.T) {
	t.Setenv(openAIUltrafastEntitlementsEnv, `[
		{"plan":"pro","models":["gpt-6.2-sol"]},
		{"plan":"promax","models":["gpt-6.2-sol","gpt-6.2-astra"]}
	]`)

	proSol := accountCodexToolCapabilities(codexSubscriptionAccount("pro"), "gpt-6.2-sol")
	require.Equal(t, []string{OpenAIFastTierPriority, OpenAIFastTierUltrafast}, codexTierIDs(t, proSol))

	// 未放行的账号不声明 Ultra Fast（Fast 档位由模型目录默认描述符合并，不在本函数追加）。
	proAstra := accountCodexToolCapabilities(codexSubscriptionAccount("pro"), "gpt-6.2-astra")
	require.NotContains(t, string(proAstra["service_tiers"]), OpenAIFastTierUltrafast)

	plus := accountCodexToolCapabilities(codexSubscriptionAccount("plus"), "gpt-6.2-sol")
	require.NotContains(t, string(plus["service_tiers"]), OpenAIFastTierUltrafast)

	promaxAstra := accountCodexToolCapabilities(codexSubscriptionAccount("promax"), "gpt-6.2-astra")
	require.Equal(t, []string{OpenAIFastTierPriority, OpenAIFastTierUltrafast}, codexTierIDs(t, promaxAstra))
}

// 账号目录自己声明过 Ultra Fast 时必须保留（workspace grant），且不能重复追加。
func TestAccountCodexToolCapabilitiesPreservesDeclaredUltrafastTier(t *testing.T) {
	t.Setenv(openAIUltrafastEntitlementsEnv, `[{"plan":"pro","models":["gpt-6.2-sol"]}]`)

	account := codexSubscriptionAccount("pro")
	account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Models: map[string]UpstreamModelMetadata{
		"gpt-6.2-sol": {
			ID: "gpt-6.2-sol",
			CodexToolCapabilities: map[string]json.RawMessage{
				"service_tiers": json.RawMessage(`[{"id":"priority","name":"Fast"},{"id":"ultrafast","name":"Ultrafast","description":"workspace grant"}]`),
			},
		},
	}})

	capabilities := accountCodexToolCapabilities(account, "gpt-6.2-sol")
	raw := string(capabilities["service_tiers"])
	require.Contains(t, raw, "workspace grant")
	require.Equal(t, 1, strings.Count(raw, `"ultrafast"`))
}

// API Key 走官方 OpenAI 地址时公开支持 GPT-6.2 Ultra Fast；第三方地址不声明。
func TestAccountCodexToolCapabilitiesAPIKeyOfficialGPT62(t *testing.T) {
	official := &Account{
		ID:          2,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Credentials: map[string]any{"base_url": "https://api.openai.com/v1"},
	}
	require.Equal(t,
		[]string{OpenAIFastTierPriority, OpenAIFastTierUltrafast},
		codexTierIDs(t, accountCodexToolCapabilities(official, "gpt-6.2-astra")),
	)

	thirdParty := &Account{
		ID:          3,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Credentials: map[string]any{"base_url": "https://api.example.com/v1"},
	}
	capabilities := accountCodexToolCapabilities(thirdParty, "gpt-6.2-astra")
	require.NotContains(t, string(capabilities["service_tiers"]), OpenAIFastTierUltrafast)
}
