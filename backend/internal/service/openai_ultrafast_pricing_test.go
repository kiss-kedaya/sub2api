package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 太平洋标准时间 UTC-8；2027-03-01 00:00 PT 即 2027-03-01T08:00:00Z
// （太平洋夏令时 2027-03-14 才开始，边界瞬间没有歧义）。
var gpt62PromoCutoverPT = time.Date(2027, time.March, 1, 0, 0, 0, 0, time.FixedZone("PST", -8*60*60))

func TestOpenAIUltrafastMultiplierGPT62PromoBoundary(t *testing.T) {
	// 固化的太平洋时间边界必须等于 08:00Z，防止有人把边界写成 UTC 零点。
	require.True(t, gpt62PromoCutoverPT.Equal(time.Date(2027, time.March, 1, 8, 0, 0, 0, time.UTC)))

	cases := []struct {
		name string
		at   time.Time
		want float64
	}{
		{"2027-02-28 00:00 PT", time.Date(2027, time.February, 28, 0, 0, 0, 0, gpt62PromoCutoverPT.Location()), 3},
		{"2027-02-28 23:59:59 PT", time.Date(2027, time.February, 28, 23, 59, 59, 0, gpt62PromoCutoverPT.Location()), 3},
		{"2027-03-01 00:00:00 PT", gpt62PromoCutoverPT, 6},
		{"2027-03-01 00:00:01 PT", gpt62PromoCutoverPT.Add(time.Second), 6},
		{"2027-03-01 00:00:00 UTC-ish (07:59:59Z 前一秒)", time.Date(2027, time.March, 1, 7, 59, 59, 0, time.UTC), 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.InDelta(t, tc.want, openAIUltrafastMultiplier("gpt-6.2-sol", tc.at), 1e-12)
			require.InDelta(t, tc.want, openAIUltrafastMultiplier("gpt-6.2-astra-high", tc.at), 1e-12)
		})
	}
}

func TestOpenAIUltrafastMultiplierNonGPT62Models(t *testing.T) {
	beforeCutover := gpt62PromoCutoverPT.Add(-time.Second)
	afterCutover := gpt62PromoCutoverPT

	// GPT-6 Astra / GPT-6.1 Sol 没有促销期，始终 6x。
	for _, model := range []string{"gpt-6-astra", "gpt-6", "gpt-6.1-sol", "gpt-6.1-sol-openai-compact"} {
		require.InDelta(t, 6, openAIUltrafastMultiplier(model, beforeCutover), 1e-12, model)
		require.InDelta(t, 6, openAIUltrafastMultiplier(model, afterCutover), 1e-12, model)
	}

	// GPT-6.2 全系（含 effort/compact 变体）走促销 → 常规切换。
	for _, model := range []string{"gpt-6.2-sol", "gpt-6.2-sol-medium", "gpt-6.2-sol-openai-compact", "gpt-6.2-astra", "gpt-6.2-astra-xhigh"} {
		require.InDelta(t, 3, openAIUltrafastMultiplier(model, beforeCutover), 1e-12, model)
		require.InDelta(t, 6, openAIUltrafastMultiplier(model, afterCutover), 1e-12, model)
	}
}

func TestOpenAIModelSupportsUltrafast(t *testing.T) {
	for _, model := range []string{"gpt-6.2-sol", "gpt-6.2-astra", "gpt-6-astra", "gpt-6.1-sol"} {
		require.True(t, openAIModelSupportsUltrafast(model), model)
	}
	for _, model := range []string{"gpt-5.5", "gpt-5.4", "gpt-6.2", "gpt-5.6-sol", ""} {
		require.False(t, openAIModelSupportsUltrafast(model), model)
	}
}

// 时间敏感规则必须可注入时间源，不能散落 time.Now：单测直接固定 BillingService.now。
func TestBillingServiceUltrafastMultiplierUsesInjectedClock(t *testing.T) {
	base := &ModelPricing{InputPricePerToken: 2e-6, OutputPricePerToken: 10e-6}

	for _, tc := range []struct {
		name string
		at   time.Time
		want float64
	}{
		{"promo", gpt62PromoCutoverPT.Add(-time.Minute), 3},
		{"regular", gpt62PromoCutoverPT, 6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &BillingService{now: func() time.Time { return tc.at }}
			got := svc.applyModelSpecificPricingPolicy("gpt-6.2-astra", base)
			require.InDelta(t, tc.want, got.UltrafastMultiplier, 1e-12)
			// 不能污染共享的模型价卡。
			require.Zero(t, base.UltrafastMultiplier)
		})
	}

	// 请求时刻（延迟结算）优先于注入的当前时刻。
	svc := &BillingService{now: func() time.Time { return gpt62PromoCutoverPT }}
	got := svc.applyModelSpecificPricingPolicyEx("gpt-6.2-sol", base, false, gpt62PromoCutoverPT.Add(-time.Second))
	require.InDelta(t, 3, got.UltrafastMultiplier, 1e-12)
}

// Ultra Fast 倍率优先级：渠道覆盖 > 模型自有倍率 > 通用默认。
func TestConfiguredServiceTierMultiplierUltrafastOverridePrecedence(t *testing.T) {
	require.InDelta(t, 2, serviceTierCostMultiplier(OpenAIFastTierUltrafast), 1e-12)

	modelDefault := &ModelPricing{UltrafastMultiplier: 3}
	require.InDelta(t, 3, configuredServiceTierMultiplier(OpenAIFastTierUltrafast, modelDefault), 1e-12,
		"未配置渠道倍率时必须回退模型档位倍率")

	override := 4.5
	withOverride := &ModelPricing{UltrafastMultiplier: 3, UltrafastMultiplierOverride: &override}
	require.InDelta(t, override, configuredServiceTierMultiplier(OpenAIFastTierUltrafast, withOverride), 1e-12,
		"渠道覆盖必须优先")

	// 非正覆盖值视为「未配置」，不能把档位乘成 0 或负数。
	zero := 0.0
	zeroOverride := &ModelPricing{UltrafastMultiplier: 6, UltrafastMultiplierOverride: &zero}
	require.InDelta(t, 6, configuredServiceTierMultiplier(OpenAIFastTierUltrafast, zeroOverride), 1e-12)

	// 两者都没配置时回退通用 2x，保证档位不会免费。
	empty := &ModelPricing{}
	require.InDelta(t, 2, configuredServiceTierMultiplier(OpenAIFastTierUltrafast, empty), 1e-12)
}

// 渠道定价解析路径（CalculateCostUnified → ModelPricingResolver）也必须应用覆盖。
func TestResolverChannelUltrafastOverride(t *testing.T) {
	override := 5.0
	resolved := &ResolvedPricing{BasePricing: &ModelPricing{UltrafastMultiplier: 3}}
	r := &ModelPricingResolver{}
	r.applyTokenOverrides(&ChannelModelPricing{UltrafastMultiplier: &override}, resolved)
	require.NotNil(t, resolved.BasePricing.UltrafastMultiplierOverride)
	require.InDelta(t, 5, configuredServiceTierMultiplier(OpenAIFastTierUltrafast, resolved.BasePricing), 1e-12)
	// 模型自有倍率保留，便于覆盖撤销后回退。
	require.InDelta(t, 3, resolved.BasePricing.UltrafastMultiplier, 1e-12)

	// 渠道未配置 → 保留模型档位倍率。
	fallback := &ResolvedPricing{BasePricing: &ModelPricing{UltrafastMultiplier: 3}}
	r.applyTokenOverrides(&ChannelModelPricing{}, fallback)
	require.Nil(t, fallback.BasePricing.UltrafastMultiplierOverride)
	require.InDelta(t, 3, configuredServiceTierMultiplier(OpenAIFastTierUltrafast, fallback.BasePricing), 1e-12)
}

// 全链路：默认价卡 + 渠道覆盖，覆盖优先、未配置回退模型（含日期切换）。
func TestGetModelPricingWithChannelUltrafast(t *testing.T) {
	newService := func(at time.Time) *BillingService {
		svc := &BillingService{
			fallbackPrices: make(map[string]*ModelPricing),
			now:            func() time.Time { return at },
		}
		svc.initFallbackPricing()
		return svc
	}

	svc := newService(gpt62PromoCutoverPT.Add(-time.Hour))
	modelOnly, err := svc.GetModelPricingWithChannel("gpt-6.2-astra", nil)
	require.NoError(t, err)
	require.InDelta(t, 3, modelOnly.UltrafastMultiplier, 1e-12)
	require.Nil(t, modelOnly.UltrafastMultiplierOverride)

	override := 4.5
	withChannel, err := svc.GetModelPricingWithChannel("gpt-6.2-astra", &ChannelModelPricing{UltrafastMultiplier: &override})
	require.NoError(t, err)
	require.InDelta(t, 3, withChannel.UltrafastMultiplier, 1e-12)
	require.NotNil(t, withChannel.UltrafastMultiplierOverride)
	require.InDelta(t, override, configuredServiceTierMultiplier(OpenAIFastTierUltrafast, withChannel), 1e-12)

	// 越过促销截止点后，未配置渠道覆盖的模型自动切 6x。
	after := newService(gpt62PromoCutoverPT)
	regular, err := after.GetModelPricingWithChannel("gpt-6.2-sol", nil)
	require.NoError(t, err)
	require.InDelta(t, 6, regular.UltrafastMultiplier, 1e-12)
}
