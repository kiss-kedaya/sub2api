//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// 线上真实配置做夹具：DeepSeek 的两个分组各绑一个渠道，渠道标价和目录价不同。
// 目录价（$/1M）flash 0.22 / 0.66 / 0.007，pro 0.66 / 1.98 / 0.022。
func deepseekReferenceService(t *testing.T) *ModelPlazaService {
	t.Helper()
	catalog := newStubPricingServiceFromMap(map[string]*LiteLLMModelPricing{
		"deepseek-v4.1-flash": {Mode: "chat", LiteLLMProvider: "deepseek", InputCostPerToken: 0.22e-6, OutputCostPerToken: 0.66e-6, CacheReadInputTokenCost: 0.007e-6},
		"deepseek-v4-pro":     {Mode: "chat", LiteLLMProvider: "deepseek", InputCostPerToken: 0.66e-6, OutputCostPerToken: 1.98e-6, CacheReadInputTokenCost: 0.022e-6},
	})
	peak := &ChannelTimePricing{Timezone: "Asia/Shanghai", WeekdaysOnly: true, Periods: []ChannelTimePricingPeriod{
		{StartTime: "09:00:00", EndTime: "12:00:00", Multiplier: 2},
		{StartTime: "14:00:00", EndTime: "18:00:00", Multiplier: 2},
	}}
	token := func(model string, in, out, cacheRead float64, tp *ChannelTimePricing) ChannelModelPricing {
		return ChannelModelPricing{
			Platform: PlatformDeepseek, Models: []string{model}, BillingMode: BillingModeToken,
			InputPrice: testPtrFloat64(in), OutputPrice: testPtrFloat64(out), CacheReadPrice: testPtrFloat64(cacheRead),
			TimePricing: tp,
		}
	}
	channels := []Channel{
		{ID: 6, Name: "国模", Status: StatusActive, GroupIDs: []int64{36}, ModelPricing: []ChannelModelPricing{
			token("deepseek-v4.1-flash", 2e-6, 8e-6, 0.04e-6, nil),
			token("deepseek-v4-pro", 9e-6, 27e-6, 0.3e-6, nil),
		}},
		{ID: 8, Name: "峰谷", Status: StatusActive, GroupIDs: []int64{99}, ModelPricing: []ChannelModelPricing{
			token("deepseek-v4.1-flash", 1e-6, 4e-6, 0.02e-6, peak),
			token("deepseek-v4-pro", 4e-6, 13.5e-6, 0.15e-6, peak),
		}},
		// 只放行模型、没有改价的渠道条目：参考价仍应是目录价。
		{ID: 9, Name: "放行", Status: StatusActive, GroupIDs: []int64{100}, ModelPricing: []ChannelModelPricing{
			{Platform: PlatformDeepseek, Models: []string{"deepseek-v4.1-flash"}, BillingMode: BillingModeToken},
		}},
	}
	groups := []Group{
		{ID: 36, Name: "DeepSeek", Platform: PlatformDeepseek, RateMultiplier: 0.14, LongContextPricingEnabled: true},
		{ID: 99, Name: "DeepSeek 官key", Platform: PlatformDeepseek, RateMultiplier: 0.3, LongContextPricingEnabled: true},
		{ID: 100, Name: "放行", Platform: PlatformDeepseek, RateMultiplier: 1, LongContextPricingEnabled: true},
	}
	return newPlazaServiceWithBilling(channels, groups,
		map[int64]string{36: PlatformDeepseek, 99: PlatformDeepseek, 100: PlatformDeepseek}, catalog)
}

func plazaGroupsByID(t *testing.T, svc *ModelPlazaService) map[int64]PlazaGroup {
	t.Helper()
	out, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	byID := make(map[int64]PlazaGroup, len(out))
	for _, g := range out {
		byID[g.ID] = g
	}
	return byID
}

func TestListGroups_ChannelPriceShownAsReferencePrice(t *testing.T) {
	groups := plazaGroupsByID(t, deepseekReferenceService(t))
	require.Len(t, groups, 3)

	// 国模渠道：flash 标价 $2 / $8 / $0.04，目录价是 $0.22，参考价列必须显示渠道标价。
	flash := plazaModelsByName(groups[36].Models)["deepseek-v4.1-flash"]
	require.NotNil(t, flash.OfficialPricing)
	require.InDelta(t, 2e-6, *flash.OfficialPricing.InputPrice, 1e-15)
	require.InDelta(t, 8e-6, *flash.OfficialPricing.OutputPrice, 1e-15)
	require.InDelta(t, 0.04e-6, *flash.OfficialPricing.CacheReadPrice, 1e-15)
	require.False(t, flash.OfficialPricing.Custom, "渠道标价不打自定义标记")
	require.Empty(t, flash.OfficialPricing.Intervals)

	pro := plazaModelsByName(groups[36].Models)["deepseek-v4-pro"]
	require.InDelta(t, 9e-6, *pro.OfficialPricing.InputPrice, 1e-15)
	require.InDelta(t, 27e-6, *pro.OfficialPricing.OutputPrice, 1e-15)

	// 峰谷渠道：同一个模型标价 $1 / $4 / $0.02。参考价是不含分时倍率的标准价，不是 $2。
	flash = plazaModelsByName(groups[99].Models)["deepseek-v4.1-flash"]
	require.InDelta(t, 1e-6, *flash.OfficialPricing.InputPrice, 1e-15)
	require.InDelta(t, 4e-6, *flash.OfficialPricing.OutputPrice, 1e-15)
	require.InDelta(t, 0.02e-6, *flash.OfficialPricing.CacheReadPrice, 1e-15)
	require.NotNil(t, flash.TimePricing)
	require.Len(t, flash.TimePricing.Periods, 2)

	pro = plazaModelsByName(groups[99].Models)["deepseek-v4-pro"]
	require.InDelta(t, 4e-6, *pro.OfficialPricing.InputPrice, 1e-15)
	require.InDelta(t, 13.5e-6, *pro.OfficialPricing.OutputPrice, 1e-15)
	require.InDelta(t, 0.15e-6, *pro.OfficialPricing.CacheReadPrice, 1e-15)
}

func TestListGroups_ChannelEntryWithoutPriceKeepsCatalogReference(t *testing.T) {
	groups := plazaGroupsByID(t, deepseekReferenceService(t))
	flash := plazaModelsByName(groups[100].Models)["deepseek-v4.1-flash"]
	require.NotNil(t, flash.OfficialPricing)
	require.InDelta(t, 0.22e-6, *flash.OfficialPricing.InputPrice, 1e-15)
	require.InDelta(t, 0.66e-6, *flash.OfficialPricing.OutputPrice, 1e-15)
	require.InDelta(t, 0.007e-6, *flash.OfficialPricing.CacheReadPrice, 1e-15)
	require.False(t, flash.OfficialPricing.Custom)
}

// 页面上 实付 = 参考价 × 倍率：每个分组里每个模型的参考价必须等于计费首档单价。
func TestListGroups_ReferencePriceEqualsBilledBasePrice(t *testing.T) {
	for id, g := range plazaGroupsByID(t, deepseekReferenceService(t)) {
		for _, m := range g.Models {
			require.NotNil(t, m.OfficialPricing, "group %d %s", id, m.Name)
			require.NotNil(t, m.Pricing, "group %d %s", id, m.Name)
			require.InDelta(t, *m.Pricing.InputPrice, *m.OfficialPricing.InputPrice, 1e-15, "group %d %s input", id, m.Name)
			require.InDelta(t, *m.Pricing.OutputPrice, *m.OfficialPricing.OutputPrice, 1e-15, "group %d %s output", id, m.Name)
			require.InDelta(t, *m.Pricing.CacheReadPrice, *m.OfficialPricing.CacheReadPrice, 1e-15, "group %d %s cache", id, m.Name)
		}
	}
}

func TestListGroups_ChannelPriceOnLadderModelMatchesBilledTiers(t *testing.T) {
	// gpt-5.4 目录带 272K 阶梯。渠道只改输入价：参考价的每一档必须和实付档逐档一致，
	// 阶梯开关关闭的分组则是单档。
	channels := []Channel{{
		ID: 1, Name: "ch", Status: StatusActive, GroupIDs: []int64{10, 20},
		ModelPricing: []ChannelModelPricing{{
			Platform: PlatformOpenAI, Models: []string{"gpt-5.4"}, BillingMode: BillingModeToken,
			InputPrice: testPtrFloat64(1e-6),
		}},
	}}
	groups := []Group{
		{ID: 10, Name: "on", Platform: PlatformOpenAI, RateMultiplier: 1, LongContextPricingEnabled: true},
		{ID: 20, Name: "off", Platform: PlatformOpenAI, RateMultiplier: 1, LongContextPricingEnabled: false},
	}
	svc := newPlazaServiceWithBilling(channels, groups, map[int64]string{10: PlatformOpenAI, 20: PlatformOpenAI},
		newStubPricingServiceFromJSON(t, openAILadderCatalogJSON))
	byID := plazaGroupsByID(t, svc)

	on := byID[10].Models[0]
	require.NotNil(t, on.OfficialPricing)
	require.InDelta(t, 1e-6, *on.OfficialPricing.InputPrice, 1e-15, "参考价起步价取渠道价，不是目录的 2.5/1M")
	require.Len(t, on.Pricing.Intervals, 2)
	require.Len(t, on.OfficialPricing.Intervals, len(on.Pricing.Intervals))
	for i := range on.Pricing.Intervals {
		require.Equal(t, on.Pricing.Intervals[i].TierLabel, on.OfficialPricing.Intervals[i].TierLabel)
		require.InDelta(t, *on.Pricing.Intervals[i].InputPrice, *on.OfficialPricing.Intervals[i].InputPrice, 1e-15)
		require.InDelta(t, *on.Pricing.Intervals[i].OutputPrice, *on.OfficialPricing.Intervals[i].OutputPrice, 1e-15)
	}

	off := byID[20].Models[0]
	require.Empty(t, off.Pricing.Intervals)
	require.NotNil(t, off.OfficialPricing)
	require.Empty(t, off.OfficialPricing.Intervals, "阶梯关闭时实付是单档，参考价也不能画目录的两档")
	require.InDelta(t, 1e-6, *off.OfficialPricing.InputPrice, 1e-15)
}

func TestListGroups_GroupCardOnLadderModelShowsCustomFirstTier(t *testing.T) {
	// 分组价卡只改起步价。阶梯模型上，参考价第一档必须是价卡价，而不是目录价，
	// 否则前端有 Intervals 时第一行会显示目录价，和实付对不上。
	channels := []Channel{{
		ID: 1, Name: "ch", Status: StatusActive, GroupIDs: []int64{10},
		ModelPricing: []ChannelModelPricing{{Platform: PlatformOpenAI, Models: []string{"gpt-5.4"}, BillingMode: BillingModeToken}},
	}}
	groups := []Group{{
		ID: 10, Name: "card", Platform: PlatformOpenAI, RateMultiplier: 1, LongContextPricingEnabled: true,
		ModelPricing: []ChannelModelPricing{{Models: []string{"gpt-5.4"}, BillingMode: BillingModeToken, InputPrice: testPtrFloat64(1e-6)}},
	}}
	svc := newPlazaServiceWithBilling(channels, groups, map[int64]string{10: PlatformOpenAI},
		newStubPricingServiceFromJSON(t, openAILadderCatalogJSON))
	m := plazaGroupsByID(t, svc)[10].Models[0]

	require.NotNil(t, m.OfficialPricing)
	require.True(t, m.OfficialPricing.Custom)
	require.InDelta(t, 1e-6, *m.OfficialPricing.InputPrice, 1e-15)
	require.Len(t, m.OfficialPricing.Intervals, len(m.Pricing.Intervals))
	require.NotEmpty(t, m.OfficialPricing.Intervals)
	require.InDelta(t, 1e-6, *m.OfficialPricing.Intervals[0].InputPrice, 1e-15, "第一档是价卡价")
	for i := range m.Pricing.Intervals {
		require.InDelta(t, *m.Pricing.Intervals[i].InputPrice, *m.OfficialPricing.Intervals[i].InputPrice, 1e-15)
	}
}

func TestConfiguredReferencePricing(t *testing.T) {
	catalog := &PlazaOfficialPricing{
		InputPrice: testPtrFloat64(0.22e-6), OutputPrice: testPtrFloat64(0.66e-6), CacheReadPrice: testPtrFloat64(0.007e-6),
	}
	tier := func(in, out, cacheRead float64) ContextPricingTier {
		return ContextPricingTier{Input: testPtrFloat64(in), Output: testPtrFloat64(out), CacheRead: testPtrFloat64(cacheRead)}
	}
	sched := func(source string, tiers ...ContextPricingTier) *ContextPricingSchedule {
		return &ContextPricingSchedule{Source: source, Tiers: tiers}
	}

	t.Run("没有阶梯表(非 token 计费)保持目录价", func(t *testing.T) {
		require.Same(t, catalog, configuredReferencePricing(catalog, nil))
		require.Same(t, catalog, configuredReferencePricing(catalog, &ContextPricingSchedule{Source: PricingSourceChannel}))
	})
	t.Run("定价来自目录保持目录价", func(t *testing.T) {
		for _, src := range []string{PricingSourceLiteLLM, PricingSourceFallback, ""} {
			require.Same(t, catalog, configuredReferencePricing(catalog, sched(src, tier(5e-6, 5e-6, 5e-6))), src)
		}
	})
	t.Run("渠道改价用渠道价且不打标记", func(t *testing.T) {
		got := configuredReferencePricing(catalog, sched(PricingSourceChannel, tier(1e-6, 4e-6, 0.02e-6)))
		require.NotSame(t, catalog, got)
		require.InDelta(t, 1e-6, *got.InputPrice, 1e-15)
		require.InDelta(t, 4e-6, *got.OutputPrice, 1e-15)
		require.False(t, got.Custom)
		require.Empty(t, got.Intervals)
	})
	t.Run("渠道条目没改价时沿用目录对象", func(t *testing.T) {
		require.Same(t, catalog, configuredReferencePricing(catalog, sched(PricingSourceChannel, tier(0.22e-6, 0.66e-6, 0.007e-6))))
	})
	t.Run("目录没有这个模型时直接用渠道价", func(t *testing.T) {
		got := configuredReferencePricing(nil, sched(PricingSourceChannel, tier(1e-6, 4e-6, 0.02e-6)))
		require.NotNil(t, got)
		require.InDelta(t, 1e-6, *got.InputPrice, 1e-15)
		require.False(t, got.Custom)
	})
	t.Run("分组价卡一律用阶梯表并打标记，即使和目录价相同", func(t *testing.T) {
		got := configuredReferencePricing(catalog, sched(PricingSourceGroup, tier(0.22e-6, 0.66e-6, 0.007e-6)))
		require.NotSame(t, catalog, got)
		require.True(t, got.Custom)
	})
	t.Run("多档逐档给出", func(t *testing.T) {
		two := sched(PricingSourceChannel,
			ContextPricingTier{MinTokens: 0, MaxTokens: ptrInt(272000), Input: testPtrFloat64(1e-6), Output: testPtrFloat64(4e-6)},
			ContextPricingTier{MinTokens: 272000, Input: testPtrFloat64(2e-6), Output: testPtrFloat64(6e-6)},
		)
		got := configuredReferencePricing(catalog, two)
		require.Len(t, got.Intervals, 2)
		require.InDelta(t, 2e-6, *got.Intervals[1].InputPrice, 1e-15)
	})
}

func TestSamePrice(t *testing.T) {
	require.True(t, samePrice(nil, nil))
	require.True(t, samePrice(nil, testPtrFloat64(0)), "nil 与 0 都表示没有价格")
	require.True(t, samePrice(testPtrFloat64(0.22e-6), testPtrFloat64(0.22e-6*(1+1e-12))), "探针 12 位有效数字内的噪声视为相同")
	require.False(t, samePrice(testPtrFloat64(0.22e-6), testPtrFloat64(0.23e-6)))
	require.False(t, samePrice(nil, testPtrFloat64(1e-9)))
}
