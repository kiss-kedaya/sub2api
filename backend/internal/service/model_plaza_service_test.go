//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// newPlazaService 构造 ListGroups 测试用的 ModelPlazaService（不接计费服务：展示定价原样透传）。
func newPlazaService(channels []Channel, groups []Group, pricing *PricingService) *ModelPlazaService {
	repo := &mockChannelRepository{
		listAllFn: func(ctx context.Context) ([]Channel, error) { return channels, nil },
	}
	return NewModelPlazaService(repo, &stubGroupRepoForAvailable{activeGroups: groups}, pricing, nil, nil)
}

func plazaPricedChannel(id int64, name string, groupIDs []int64, platform string, models ...string) Channel {
	return Channel{
		ID:       id,
		Name:     name,
		Status:   StatusActive,
		GroupIDs: groupIDs,
		ModelPricing: []ChannelModelPricing{{
			Platform:    platform,
			Models:      models,
			BillingMode: BillingModeToken,
			InputPrice:  testPtrFloat64(3e-6),
			OutputPrice: testPtrFloat64(1.5e-5),
		}},
	}
}

func TestListPlazaGroups_GroupCentricAggregation(t *testing.T) {
	// 两个渠道挂同一分组:模型并入同一 PlazaGroup;无模型的分组不返回。
	channels := []Channel{
		plazaPricedChannel(1, "chA", []int64{10}, "anthropic", "claude-sonnet"),
		plazaPricedChannel(2, "chB", []int64{10}, "anthropic", "claude-opus"),
	}
	groups := []Group{
		{ID: 10, Name: "g-main", Description: "desc", Platform: "anthropic", RateMultiplier: 1},
		{ID: 20, Name: "g-empty", Platform: "anthropic", RateMultiplier: 0.5},
	}
	svc := newPlazaService(channels, groups, nil)
	out, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 1, "无模型的分组不应返回")
	require.Equal(t, int64(10), out[0].ID)
	require.Equal(t, "desc", out[0].Description)
	require.Len(t, out[0].Models, 2)
	// 组内模型按名称排序
	require.Equal(t, "claude-opus", out[0].Models[0].Name)
	require.Equal(t, "claude-sonnet", out[0].Models[1].Name)
}

func TestWithDefaultMaxReasoningEffortMultiplier_Fable51(t *testing.T) {
	base := &ChannelModelPricing{BillingMode: BillingModeToken}
	got := withDefaultMaxReasoningEffortMultiplier(base, "claude-fable-5-1")
	require.NotSame(t, base, got)
	require.NotNil(t, got.MaxReasoningEffortMultiplier)
	require.Equal(t, 3.0, *got.MaxReasoningEffortMultiplier)
	require.Nil(t, base.MaxReasoningEffortMultiplier)

	configured := 1.25
	custom := &ChannelModelPricing{MaxReasoningEffortMultiplier: &configured}
	require.Same(t, custom, withDefaultMaxReasoningEffortMultiplier(custom, "claude-fable-5-1"))
}

func TestListPlazaGroups_DedupFirstWinsWithPricingUpgrade(t *testing.T) {
	// 同名模型:先见者胜;仅当已存条目无定价而新条目有定价时升级替换。
	unpriced := Channel{
		ID: 1, Name: "alpha", Status: StatusActive, GroupIDs: []int64{10},
		// mapping-only → SupportedModels 产出无定价条目
		ModelMapping: map[string]map[string]string{
			"anthropic": {"claude-sonnet": "claude-sonnet"},
		},
	}
	priced := plazaPricedChannel(2, "beta", []int64{10}, "anthropic", "claude-sonnet")
	groups := []Group{{ID: 10, Name: "g", Platform: "anthropic", RateMultiplier: 1}}

	// alpha(无价)按名称序先于 beta(有价):先见者无价,应被有价条目升级。
	svc := newPlazaService([]Channel{priced, unpriced}, groups, nil)
	out, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Len(t, out[0].Models, 1)
	require.NotNil(t, out[0].Models[0].Pricing, "无价条目应被有价条目升级")
	require.NotNil(t, out[0].Models[0].Pricing.InputPrice)
}

func TestListPlazaGroups_PlatformIsolation(t *testing.T) {
	// 渠道同时有 anthropic/openai 定价,anthropic 分组只应看到 anthropic 模型。
	ch := Channel{
		ID: 1, Name: "multi", Status: StatusActive, GroupIDs: []int64{10, 20},
		ModelPricing: []ChannelModelPricing{
			{Platform: "anthropic", Models: []string{"claude-sonnet"}, InputPrice: testPtrFloat64(3e-6)},
			{Platform: "openai", Models: []string{"gpt-5"}, InputPrice: testPtrFloat64(2e-6)},
		},
	}
	groups := []Group{
		{ID: 10, Name: "g-claude", Platform: "anthropic", RateMultiplier: 1},
		{ID: 20, Name: "g-gpt", Platform: "openai", RateMultiplier: 1},
	}
	svc := newPlazaService([]Channel{ch}, groups, nil)
	out, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 2)
	byName := map[string][]PlazaModel{}
	for _, g := range out {
		byName[g.Name] = g.Models
	}
	require.Len(t, byName["g-claude"], 1)
	require.Equal(t, "claude-sonnet", byName["g-claude"][0].Name)
	require.Len(t, byName["g-gpt"], 1)
	require.Equal(t, "gpt-5", byName["g-gpt"][0].Name)
}

func TestListPlazaGroups_CompositeIncludesConfiguredConcretePlatforms(t *testing.T) {
	anthropicPrice := 3e-6
	openAIPrice := 2e-6
	ch := Channel{
		ID: 1, Name: "multi", Status: StatusActive, GroupIDs: []int64{10},
		ModelPricing: []ChannelModelPricing{
			{Platform: PlatformAnthropic, Models: []string{"shared-model"}, InputPrice: &anthropicPrice},
			{Platform: PlatformOpenAI, Models: []string{"shared-model"}, InputPrice: &openAIPrice},
			{Platform: "", Models: []string{"empty-platform"}},
			{Platform: PlatformComposite, Models: []string{"nested-composite"}},
			{Platform: "unknown-platform", Models: []string{"unknown-platform"}},
		},
	}
	groups := []Group{{ID: 10, Name: "composite", Platform: PlatformComposite, RateMultiplier: 1}}

	out, err := newPlazaService([]Channel{ch}, groups, nil).ListGroups(context.Background())

	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Len(t, out[0].Models, 2, "only concrete platforms are included and same-named models remain distinct")
	require.Equal(t, PlatformAnthropic, out[0].Models[0].Platform)
	require.Equal(t, PlatformOpenAI, out[0].Models[1].Platform)
	require.InDelta(t, anthropicPrice, *out[0].Models[0].Pricing.InputPrice, 1e-12)
	require.InDelta(t, openAIPrice, *out[0].Models[1].Pricing.InputPrice, 1e-12)
}

func TestListPlazaGroups_CompositeAndOrdinaryGroupsDoNotLeakPlatforms(t *testing.T) {
	ch := Channel{
		ID: 1, Name: "multi", Status: StatusActive, GroupIDs: []int64{10, 20},
		ModelPricing: []ChannelModelPricing{
			{Platform: PlatformAnthropic, Models: []string{"claude-sonnet"}, InputPrice: testPtrFloat64(3e-6)},
			{Platform: PlatformOpenAI, Models: []string{"gpt-5"}, InputPrice: testPtrFloat64(2e-6)},
		},
	}
	groups := []Group{
		{ID: 10, Name: "anthropic-only", Platform: PlatformAnthropic, RateMultiplier: 1},
		{ID: 20, Name: "composite", Platform: PlatformComposite, RateMultiplier: 1},
	}

	out, err := newPlazaService([]Channel{ch}, groups, nil).ListGroups(context.Background())

	require.NoError(t, err)
	require.Len(t, out, 2)
	byName := map[string]PlazaGroup{}
	for _, group := range out {
		byName[group.Name] = group
	}
	require.Len(t, byName["anthropic-only"].Models, 1)
	require.Equal(t, []PlazaModel{{
		Name: "claude-sonnet", Platform: PlatformAnthropic, Pricing: byName["anthropic-only"].Models[0].Pricing,
	}}, byName["anthropic-only"].Models)
	require.Len(t, byName["composite"].Models, 2)
	require.Equal(t, []string{"claude-sonnet", "gpt-5"}, []string{
		byName["composite"].Models[0].Name,
		byName["composite"].Models[1].Name,
	})
	require.Equal(t, []string{PlatformAnthropic, PlatformOpenAI}, []string{
		byName["composite"].Models[0].Platform,
		byName["composite"].Models[1].Platform,
	})
}

func TestListPlazaGroups_InactiveChannelSkipped(t *testing.T) {
	inactive := plazaPricedChannel(1, "off", []int64{10}, "anthropic", "claude-sonnet")
	inactive.Status = "inactive"
	groups := []Group{{ID: 10, Name: "g", Platform: "anthropic", RateMultiplier: 1}}
	svc := newPlazaService([]Channel{inactive}, groups, nil)
	out, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	require.Empty(t, out)
}

func TestListPlazaGroups_SortedByRateMultiplierAsc(t *testing.T) {
	channels := []Channel{
		plazaPricedChannel(1, "ch", []int64{10, 20, 30}, "anthropic", "claude-sonnet"),
	}
	groups := []Group{
		{ID: 10, Name: "b-standard", Platform: "anthropic", RateMultiplier: 1},
		{ID: 20, Name: "a-standard", Platform: "anthropic", RateMultiplier: 1},
		{ID: 30, Name: "cheap", Platform: "anthropic", RateMultiplier: 0.5},
	}
	svc := newPlazaService(channels, groups, nil)
	out, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 3)
	require.Equal(t, "cheap", out[0].Name, "倍率低者在前")
	require.Equal(t, "a-standard", out[1].Name, "同倍率按名称")
	require.Equal(t, "b-standard", out[2].Name)
}

func TestListPlazaGroups_OfficialPricingFill(t *testing.T) {
	pricingSvc := newStubPricingServiceFromMap(map[string]*LiteLLMModelPricing{
		"claude-sonnet": {
			Mode:                                "chat",
			InputCostPerToken:                   3e-6,
			OutputCostPerToken:                  1.5e-5,
			CacheCreationInputTokenCost:         3.75e-6,
			CacheCreationInputTokenCostAbove1hr: 6e-6,
			CacheReadInputTokenCost:             3e-7,
		},
		"token-absent": {Mode: "image_generation", TokenPricingAbsent: true, OutputCostPerImage: 0.04},
	})
	channels := []Channel{
		plazaPricedChannel(1, "ch", []int64{10}, "anthropic", "claude-sonnet", "unknown-model", "token-absent"),
	}
	groups := []Group{{ID: 10, Name: "g", Platform: "anthropic", RateMultiplier: 1}}
	svc := newPlazaService(channels, groups, pricingSvc)
	// 官方价与计费同源：需要计费服务与解析器（官方参考不查渠道，解析器无需渠道服务）。
	svc.billingService = NewBillingService(&config.Config{}, pricingSvc)
	svc.resolver = NewModelPricingResolver(nil, svc.billingService)
	out, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Len(t, out[0].Models, 3)

	byName := map[string]PlazaModel{}
	for _, m := range out[0].Models {
		byName[m.Name] = m
	}
	// 命中:填充完整官方价(含 1h 缓存写入)
	official := byName["claude-sonnet"].OfficialPricing
	require.NotNil(t, official)
	require.InDelta(t, 3e-6, *official.InputPrice, 1e-12)
	require.InDelta(t, 6e-6, *official.CacheWrite1hPrice, 1e-12)
	require.InDelta(t, 3e-7, *official.CacheReadPrice, 1e-12)
	// 未命中:nil(GetModelPricing 的 claude 系列模糊匹配对非 claude 名不生效)
	require.Nil(t, byName["unknown-model"].OfficialPricing)
	// TokenPricingAbsent 条目不作为官方 token 价展示
	require.Nil(t, byName["token-absent"].OfficialPricing)
}

func TestListPlazaGroups_GroupImagePriceOverridesChannelPricing(t *testing.T) {
	// 图片计费模型:档位价按实收口径合成(分组图片价 > 渠道档位价 > 渠道默认按次价),
	// 分组独立倍率字段透传;未配图片价的分组保持渠道定价原样。
	perReq := 0.2
	tier4K := 0.3
	imgPrice := 0.02
	channels := []Channel{{
		ID: 1, Name: "img-ch", Status: StatusActive, GroupIDs: []int64{10, 20},
		ModelPricing: []ChannelModelPricing{{
			Platform:        "openai",
			Models:          []string{"gpt-image-2"},
			BillingMode:     BillingModeImage,
			PerRequestPrice: &perReq,
			Intervals:       []PricingInterval{{TierLabel: "4K", PerRequestPrice: &tier4K}},
		}},
	}}
	groups := []Group{
		{ID: 10, Name: "g-media", Platform: "openai", RateMultiplier: 1,
			ImagePrice1K: &imgPrice, ImageRateIndependent: true, ImageRateMultiplier: 1},
		{ID: 20, Name: "g-plain", Platform: "openai", RateMultiplier: 0.1},
	}
	svc := newPlazaService(channels, groups, nil)
	out, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 2)
	byName := map[string]PlazaGroup{}
	for _, g := range out {
		byName[g.Name] = g
	}

	media := byName["g-media"]
	require.True(t, media.ImageRateIndependent)
	require.InDelta(t, 1.0, media.ImageRateMultiplier, 1e-9)
	require.Len(t, media.Models, 1)
	p := media.Models[0].Pricing
	require.NotNil(t, p)
	require.Len(t, p.Intervals, 3)
	tierPrices := map[string]float64{}
	for _, iv := range p.Intervals {
		require.NotNil(t, iv.PerRequestPrice)
		tierPrices[iv.TierLabel] = *iv.PerRequestPrice
	}
	require.InDelta(t, 0.02, tierPrices["1K"], 1e-9, "1K 用分组图片价")
	require.InDelta(t, 0.2, tierPrices["2K"], 1e-9, "2K 分组未配,回落渠道默认按次价")
	require.InDelta(t, 0.3, tierPrices["4K"], 1e-9, "4K 分组未配,回落渠道档位价")

	plain := byName["g-plain"]
	require.False(t, plain.ImageRateIndependent)
	require.Len(t, plain.Models, 1)
	pp := plain.Models[0].Pricing
	require.NotNil(t, pp)
	require.Len(t, pp.Intervals, 1, "未配分组图片价:渠道定价原样")
	require.InDelta(t, 0.2, *pp.PerRequestPrice, 1e-9)

	// 合成为克隆,渠道原始定价不被修改
	require.Len(t, channels[0].ModelPricing[0].Intervals, 1)
}

func TestListPlazaGroups_GroupImagePriceIgnoredForNonImageModes(t *testing.T) {
	// token 模式定价不受分组图片价影响。
	imgPrice := 0.02
	channels := []Channel{plazaPricedChannel(1, "ch", []int64{10}, "openai", "gpt-5")}
	groups := []Group{{ID: 10, Name: "g", Platform: "openai", RateMultiplier: 1, ImagePrice1K: &imgPrice}}
	svc := newPlazaService(channels, groups, nil)
	out, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 1)
	p := out[0].Models[0].Pricing
	require.NotNil(t, p)
	require.Empty(t, p.Intervals)
	require.NotNil(t, p.InputPrice)
	require.Nil(t, p.PerRequestPrice)
}

func TestListPlazaGroups_RepoErrorsPropagate(t *testing.T) {
	sentinel := errors.New("boom")
	repo := &mockChannelRepository{
		listAllFn: func(ctx context.Context) ([]Channel, error) { return nil, sentinel },
	}
	svc := NewModelPlazaService(repo, &stubGroupRepoForAvailable{}, nil, nil, nil)
	out, err := svc.ListGroups(context.Background())
	require.Nil(t, out)
	require.ErrorIs(t, err, sentinel)

	svc2 := NewModelPlazaService(
		&mockChannelRepository{listAllFn: func(ctx context.Context) ([]Channel, error) { return nil, nil }},
		&stubGroupRepoForAvailable{listActiveErr: sentinel},
		nil, nil, nil,
	)
	out2, err2 := svc2.ListGroups(context.Background())
	require.Nil(t, out2)
	require.ErrorIs(t, err2, sentinel)
}

// newPlazaServiceWithBilling 构造接入计费服务与解析器的广场服务：解析器的渠道服务与广场共用同一份渠道数据。
func newPlazaServiceWithBilling(channels []Channel, groups []Group, groupPlatforms map[int64]string, catalog *PricingService) *ModelPlazaService {
	repo := &mockChannelRepository{
		listAllFn: func(ctx context.Context) ([]Channel, error) { return channels, nil },
		getGroupPlatformsFn: func(ctx context.Context, _ []int64) (map[int64]string, error) {
			return groupPlatforms, nil
		},
	}
	cs := NewChannelService(repo, nil, nil, nil, nil)
	bs := NewBillingService(&config.Config{}, catalog)
	return NewModelPlazaService(repo, &stubGroupRepoForAvailable{activeGroups: groups}, catalog, bs, NewModelPricingResolver(cs, bs))
}

func plazaModelsByName(models []PlazaModel) map[string]PlazaModel {
	out := make(map[string]PlazaModel, len(models))
	for _, m := range models {
		out[m.Name] = m
	}
	return out
}

func TestListGroups_TokenLadderFollowsGroupToggle(t *testing.T) {
	// 同一渠道挂开启/关闭阶梯的两个分组：实付档位随分组开关，官方阶梯不受影响。
	channels := []Channel{{
		ID: 1, Name: "ch", Status: StatusActive, GroupIDs: []int64{10, 20},
		ModelPricing: []ChannelModelPricing{{Platform: PlatformOpenAI, Models: []string{"gpt-5.4"}, BillingMode: BillingModeToken}},
	}}
	groups := []Group{
		{ID: 10, Name: "on", Platform: PlatformOpenAI, RateMultiplier: 1, LongContextPricingEnabled: true},
		{ID: 20, Name: "off", Platform: PlatformOpenAI, RateMultiplier: 2, LongContextPricingEnabled: false},
	}
	svc := newPlazaServiceWithBilling(channels, groups, map[int64]string{10: PlatformOpenAI, 20: PlatformOpenAI},
		newStubPricingServiceFromJSON(t, openAILadderCatalogJSON))
	out, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 2)

	on, off := out[0], out[1]
	require.True(t, on.LongContextPricingEnabled)
	require.False(t, off.LongContextPricingEnabled)

	onModel := on.Models[0]
	require.Equal(t, ContextPricingBasisWholeRequest, onModel.LongContextBasis)
	require.Len(t, onModel.Pricing.Intervals, 2)
	require.Equal(t, "≤272K", onModel.Pricing.Intervals[0].TierLabel)
	require.Equal(t, ">272K", onModel.Pricing.Intervals[1].TierLabel)
	require.InDelta(t, 2.5e-6, *onModel.Pricing.InputPrice, 1e-15)
	require.InDelta(t, 5e-6, *onModel.Pricing.Intervals[1].InputPrice, 1e-15)
	require.InDelta(t, 22.5e-6, *onModel.Pricing.Intervals[1].OutputPrice, 1e-15)
	require.InDelta(t, 5e-6, *onModel.Pricing.Intervals[1].CacheWritePrice, 1e-15)
	require.InDelta(t, 0.5e-6, *onModel.Pricing.Intervals[1].CacheReadPrice, 1e-15)

	offModel := off.Models[0]
	require.Empty(t, offModel.LongContextBasis)
	require.Empty(t, offModel.Pricing.Intervals)
	require.InDelta(t, 2.5e-6, *offModel.Pricing.InputPrice, 1e-15)

	for _, m := range []PlazaModel{onModel, offModel} {
		require.NotNil(t, m.OfficialPricing)
		require.Len(t, m.OfficialPricing.Intervals, 2, "官方阶梯不受分组开关影响")
		require.InDelta(t, 5e-6, *m.OfficialPricing.Intervals[1].InputPrice, 1e-15)
		require.InDelta(t, 2.5e-6, *m.OfficialPricing.InputPrice, 1e-15)
	}
}

func TestListGroups_GeminiCatalogLadderShownWholeRequest(t *testing.T) {
	channels := []Channel{{
		ID: 1, Name: "ch", Status: StatusActive, GroupIDs: []int64{10},
		ModelMapping: map[string]map[string]string{PlatformGemini: {"gemini-2.5-pro": "gemini-2.5-pro"}},
	}}
	groups := []Group{{ID: 10, Name: "g", Platform: PlatformGemini, RateMultiplier: 1, LongContextPricingEnabled: true}}
	svc := newPlazaServiceWithBilling(channels, groups, map[int64]string{10: PlatformGemini},
		newStubPricingServiceFromJSON(t, geminiLadderCatalogJSON))
	out, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 1)
	m := out[0].Models[0]
	require.Equal(t, ContextPricingBasisWholeRequest, m.LongContextBasis)
	require.Len(t, m.Pricing.Intervals, 2)
	require.Equal(t, "≤200K", m.Pricing.Intervals[0].TierLabel)
	require.Equal(t, ">200K", m.Pricing.Intervals[1].TierLabel)
	require.InDelta(t, 2.5e-6, *m.Pricing.Intervals[1].InputPrice, 1e-15)
	require.InDelta(t, 15e-6, *m.Pricing.Intervals[1].OutputPrice, 1e-15)
	// 官方参考价与实付同源：都来自目录数据的阶梯字段
	require.NotNil(t, m.OfficialPricing)
	require.Len(t, m.OfficialPricing.Intervals, 2)
}

func TestListGroups_GroupTokenCardOverridesChannelPricing(t *testing.T) {
	channels := []Channel{plazaPricedChannel(1, "ch", []int64{10}, PlatformAnthropic, "claude-sonnet-4")}
	groups := []Group{{
		ID: 10, Name: "g", Platform: PlatformAnthropic, RateMultiplier: 1, LongContextPricingEnabled: true,
		ModelPricing: []ChannelModelPricing{{Models: []string{"claude-sonnet-*"}, BillingMode: BillingModeToken, InputPrice: testPtrFloat64(1e-6)}},
	}}
	svc := newPlazaServiceWithBilling(channels, groups, map[int64]string{10: PlatformAnthropic}, nil)
	out, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	m := out[0].Models[0]
	require.InDelta(t, 1e-6, *m.Pricing.InputPrice, 1e-15, "分组价卡优先于渠道平价")
	require.InDelta(t, 15e-6, *m.Pricing.OutputPrice, 1e-15, "卡未配置的项回落目录价")
	require.Empty(t, m.Pricing.Intervals)
}

func TestListGroups_GroupCustomCardReplacesOfficialReference(t *testing.T) {
	// 分组逐模型价卡生效时，参考价列改用自定义起步价；价卡未配置的项回落目录价；
	// 未配置价卡的其它分组仍显示官方价，且官方长上下文阶梯不受影响。
	pricingSvc := newStubPricingServiceFromMap(map[string]*LiteLLMModelPricing{
		"claude-sonnet": {
			Mode:                        "chat",
			InputCostPerToken:           3e-6,
			OutputCostPerToken:          1.5e-5,
			CacheCreationInputTokenCost: 3.75e-6,
			CacheReadInputTokenCost:     3e-7,
		},
	})
	channels := []Channel{plazaPricedChannel(1, "ch", []int64{10, 20}, PlatformAnthropic, "claude-sonnet")}
	groups := []Group{
		{
			ID: 10, Name: "custom", Platform: PlatformAnthropic, RateMultiplier: 2, LongContextPricingEnabled: true,
			ModelPricing: []ChannelModelPricing{{
				Models: []string{"claude-sonnet"}, BillingMode: BillingModeToken,
				InputPrice: testPtrFloat64(1e-6),
			}},
		},
		{ID: 20, Name: "official", Platform: PlatformAnthropic, RateMultiplier: 1, LongContextPricingEnabled: true},
	}
	svc := newPlazaServiceWithBilling(channels, groups,
		map[int64]string{10: PlatformAnthropic, 20: PlatformAnthropic}, pricingSvc)
	out, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 2)

	byGroup := map[int64]PlazaModel{}
	for _, g := range out {
		require.Len(t, g.Models, 1)
		byGroup[g.ID] = g.Models[0]
	}

	custom := byGroup[10]
	require.NotNil(t, custom.OfficialPricing)
	require.True(t, custom.OfficialPricing.Custom, "价卡生效时参考价应标记为自定义")
	require.InDelta(t, 1e-6, *custom.OfficialPricing.InputPrice, 1e-15)
	require.InDelta(t, 1.5e-5, *custom.OfficialPricing.OutputPrice, 1e-15, "价卡未配置输出价时回落目录价")
	require.InDelta(t, 3.75e-6, *custom.OfficialPricing.CacheWritePrice, 1e-15)

	official := byGroup[20]
	require.NotNil(t, official.OfficialPricing)
	require.False(t, official.OfficialPricing.Custom, "未配置价卡的分组仍显示官方价")
	require.InDelta(t, 3e-6, *official.OfficialPricing.InputPrice, 1e-15)
}

func TestListGroups_ImageModelKeepsTierSynthesisWithBilling(t *testing.T) {
	channels := []Channel{{
		ID: 1, Name: "ch", Status: StatusActive, GroupIDs: []int64{10},
		ModelPricing: []ChannelModelPricing{{
			Platform: PlatformOpenAI, Models: []string{"gpt-image-2"}, BillingMode: BillingModeImage,
			PerRequestPrice: testPtrFloat64(0.04),
		}},
	}}
	groups := []Group{{
		ID: 10, Name: "g", Platform: PlatformOpenAI, RateMultiplier: 1, LongContextPricingEnabled: true,
		ImagePrice1K: testPtrFloat64(0.02),
	}}
	svc := newPlazaServiceWithBilling(channels, groups, map[int64]string{10: PlatformOpenAI}, nil)
	out, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	m := out[0].Models[0]
	require.Equal(t, BillingModeImage, m.Pricing.BillingMode)
	require.Empty(t, m.LongContextBasis)
	require.Len(t, m.Pricing.Intervals, 3)
	require.InDelta(t, 0.02, *m.Pricing.Intervals[0].PerRequestPrice, 1e-12)
	require.InDelta(t, 0.04, *m.Pricing.Intervals[1].PerRequestPrice, 1e-12)
}

func TestListGroups_CatalogMissingStillShowsChannelFlatPricing(t *testing.T) {
	// 目录查不到的模型：计费按渠道平价（未配置项 $0），广场单档展示渠道平价。
	// 参考价列同样显示渠道标价（和分组价卡对目录外模型的行为一致），不打「自定义价」标记。
	channels := []Channel{plazaPricedChannel(1, "ch", []int64{10}, PlatformAnthropic, "unknown-model-xyz")}
	groups := []Group{{ID: 10, Name: "g", Platform: PlatformAnthropic, RateMultiplier: 1, LongContextPricingEnabled: true}}
	svc := newPlazaServiceWithBilling(channels, groups, map[int64]string{10: PlatformAnthropic}, nil)
	out, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	m := out[0].Models[0]
	require.NotNil(t, m.Pricing)
	require.InDelta(t, 3e-6, *m.Pricing.InputPrice, 1e-15)
	require.Empty(t, m.Pricing.Intervals)
	require.Nil(t, m.Pricing.CacheWritePrice, "目录无价且渠道未配置 → 无价")
	require.NotNil(t, m.OfficialPricing)
	require.InDelta(t, 3e-6, *m.OfficialPricing.InputPrice, 1e-15)
	require.InDelta(t, 1.5e-5, *m.OfficialPricing.OutputPrice, 1e-15)
	require.Nil(t, m.OfficialPricing.CacheWritePrice)
	require.False(t, m.OfficialPricing.Custom, "渠道标价不打自定义标记")
	require.Empty(t, m.OfficialPricing.Intervals)
}

func TestListGroups_TimePricingPassthrough(t *testing.T) {
	channels := []Channel{{
		ID: 1, Name: "ch", Status: StatusActive, GroupIDs: []int64{10},
		ModelPricing: []ChannelModelPricing{{
			Platform: PlatformDeepseek, Models: []string{"deepseek-chat"}, BillingMode: BillingModeToken,
			InputPrice: testPtrFloat64(0.28e-6), OutputPrice: testPtrFloat64(0.42e-6),
			TimePricing: &ChannelTimePricing{Timezone: "Asia/Shanghai", Periods: []ChannelTimePricingPeriod{
				{StartTime: "00:30", EndTime: "08:30", Multiplier: 0.5},
			}},
		}},
	}}
	groups := []Group{{ID: 10, Name: "cn", Platform: PlatformDeepseek, RateMultiplier: 1, LongContextPricingEnabled: true}}
	svc := newPlazaServiceWithBilling(channels, groups, map[int64]string{10: PlatformDeepseek}, nil)
	out, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	m := out[0].Models[0]
	require.NotNil(t, m.TimePricing)
	require.Equal(t, "Asia/Shanghai", m.TimePricing.Timezone)
	require.Len(t, m.TimePricing.Periods, 1)
	require.InDelta(t, 0.5, m.TimePricing.Periods[0].Multiplier, 1e-12)
	// 展示单价为标准时段价
	require.InDelta(t, 0.28e-6, *m.Pricing.InputPrice, 1e-15)
}

type stubPlazaListedModels struct {
	models map[string][]string
}

func (s stubPlazaListedModels) GetAvailableModels(_ context.Context, _ *int64, platform string) []string {
	return s.models[platform]
}

func TestListPlazaGroups_AccountListedModelsFillMissingAndKeepChannel(t *testing.T) {
	channels := []Channel{plazaPricedChannel(1, "ch", []int64{10}, PlatformOpenAI, "gpt-5")}
	groups := []Group{{
		ID: 10, Name: "openai", Platform: PlatformOpenAI, RateMultiplier: 1,
		ModelAllowlist: GroupModelAllowlist{Enabled: true, Models: []string{"gpt-5", "typesafe/jev"}},
	}}
	pricing := newStubPricingServiceFromMap(map[string]*LiteLLMModelPricing{
		"typesafe/jev": {Mode: "chat", InputCostPerToken: 1e-6, OutputCostPerToken: 2e-6},
	})
	svc := newPlazaService(channels, groups, pricing)
	svc.SetListedModelCatalog(stubPlazaListedModels{models: map[string][]string{
		PlatformOpenAI:    {"GPT-5", "typesafe/jev", "gpt-*", "blocked-model", " "},
		PlatformAnthropic: {"claude-should-not-leak"},
	}})

	out, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 1)
	byName := map[string]PlazaModel{}
	for _, m := range out[0].Models {
		byName[m.Name] = m
	}
	require.Len(t, byName, 2)
	require.NotContains(t, byName, "GPT-5")
	require.NotContains(t, byName, "blocked-model")
	require.NotContains(t, byName, "claude-should-not-leak")
	require.NotNil(t, byName["gpt-5"].Pricing)
	require.InDelta(t, 3e-6, *byName["gpt-5"].Pricing.InputPrice, 1e-15)
	require.Equal(t, PlatformOpenAI, byName["typesafe/jev"].Platform)
	require.NotNil(t, byName["typesafe/jev"].Pricing)
	require.InDelta(t, 1e-6, *byName["typesafe/jev"].Pricing.InputPrice, 1e-15)
	require.InDelta(t, 2e-6, *byName["typesafe/jev"].Pricing.OutputPrice, 1e-15)
}

func TestListPlazaGroups_AccountListedModelsShowWithoutChannelPrice(t *testing.T) {
	groups := []Group{{ID: 10, Name: "mapping-only", Platform: PlatformOpenAI, RateMultiplier: 1}}
	svc := newPlazaService(nil, groups, nil)
	svc.SetListedModelCatalog(stubPlazaListedModels{models: map[string][]string{
		PlatformOpenAI: {"typesafe/jev"},
	}})

	out, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Equal(t, []string{"typesafe/jev"}, []string{out[0].Models[0].Name})
	require.Nil(t, out[0].Models[0].Pricing)
}

func TestListPlazaGroups_AttachesCatalogMetadata(t *testing.T) {
	channels := []Channel{plazaPricedChannel(1, "ch", []int64{10}, PlatformOpenAI, "deepseek-v4.1-flash", "acme-unknown-9")}
	groups := []Group{{ID: 10, Name: "openai", Platform: PlatformOpenAI, RateMultiplier: 1}}
	svc := newPlazaService(channels, groups, nil)
	svc.SetModelMetadataCatalog(newTestModelMetadataCatalog(testModelsDevRegistry()))

	out, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 1)
	byName := map[string]PlazaModel{}
	for _, m := range out[0].Models {
		byName[m.Name] = m
	}

	flash := byName["deepseek-v4.1-flash"].Metadata
	require.NotNil(t, flash)
	require.Equal(t, int64(1_048_576), flash.ContextWindow)
	require.Equal(t, int64(393_216), flash.MaxOutputTokens, "authoritative output wins over the registry's 384000")
	require.Equal(t, []string{"text", "image"}, flash.InputModalities)
	require.Equal(t, []string{"text"}, flash.OutputModalities)
	require.Equal(t, "deepseek-v4.1", flash.Family)
	require.NotNil(t, flash.Capabilities)
	require.True(t, flash.Capabilities.Reasoning)
	require.True(t, flash.Capabilities.ToolCall)

	unknown := byName["acme-unknown-9"].Metadata
	require.NotNil(t, unknown, "a derived display name is still renderable")
	require.Zero(t, unknown.ContextWindow, "unknown models must not get fabricated limits")
}

func TestListPlazaGroups_WithoutCatalogKeepsPricingOnly(t *testing.T) {
	channels := []Channel{plazaPricedChannel(1, "ch", []int64{10}, PlatformOpenAI, "deepseek-v4.1-flash")}
	groups := []Group{{ID: 10, Name: "openai", Platform: PlatformOpenAI, RateMultiplier: 1}}
	svc := newPlazaService(channels, groups, nil)

	out, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Len(t, out[0].Models, 1)
	require.Nil(t, out[0].Models[0].Metadata, "a nil catalogue degrades to pricing only")
	require.NotNil(t, out[0].Models[0].Pricing)
}

func TestListGroups_GroupPerRequestAndVideoCardsDisplayInPlaza(t *testing.T) {
	// Issue #436: Seedance / 异步视频配置分组 per_request 或 video 价卡后，广场必须展示对应按次/按秒价格，
	// 即使渠道条目没有定价或全局目录里存在 token 兜底价；同时分组价卡优先于分组默认 VideoPrice*。
	catalog := newStubPricingServiceFromMap(map[string]*LiteLLMModelPricing{
		"doubao-seedance-2.5": {Mode: "chat", InputCostPerToken: 1e-6, OutputCostPerToken: 2e-6},
	})
	channels := []Channel{{
		ID: 1, Name: "ark-ch", Status: StatusActive, GroupIDs: []int64{10, 20},
		ModelMapping: map[string]map[string]string{
			PlatformOpenAI: {
				"doubao-seedance-2.5": "doubao-seedance-2.5",
				"doubao-seedance-pro": "doubao-seedance-pro",
			},
		},
	}}
	groups := []Group{
		{
			ID: 10, Name: "per-request-group", Platform: PlatformOpenAI, RateMultiplier: 0.5,
			VideoRateIndependent: true, VideoRateMultiplier: 0.8,
			VideoPrice480P: testPtrFloat64(0.05),
			ModelPricing: []ChannelModelPricing{{
				Platform:        PlatformOpenAI,
				Models:          []string{"doubao-seedance-2.5"},
				BillingMode:     BillingModePerRequest,
				PerRequestPrice: testPtrFloat64(10),
			}, {
				Platform:    PlatformOpenAI,
				Models:      []string{"doubao-seedance-pro"},
				BillingMode: BillingModeVideo,
				Intervals: []PricingInterval{
					{TierLabel: "480p", PerRequestPrice: testPtrFloat64(0.3)},
					{TierLabel: "720p", PerRequestPrice: testPtrFloat64(0.5)},
				},
			}},
		},
		{
			ID: 20, Name: "flat-video-group", Platform: PlatformOpenAI, RateMultiplier: 1,
			ModelPricing: []ChannelModelPricing{{
				Platform:        PlatformOpenAI,
				Models:          []string{"doubao-seedance-*"},
				BillingMode:     BillingModeVideo,
				PerRequestPrice: testPtrFloat64(0.4),
			}},
		},
	}

	svc := newPlazaServiceWithBilling(channels, groups,
		map[int64]string{10: PlatformOpenAI, 20: PlatformOpenAI}, catalog)
	out, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 2)

	byGroup := map[int64]PlazaGroup{}
	for _, g := range out {
		byGroup[g.ID] = g
	}

	g10 := byGroup[10]
	require.True(t, g10.VideoRateIndependent)
	require.InDelta(t, 0.8, g10.VideoRateMultiplier, 1e-9)
	m10 := plazaModelsByName(g10.Models)

	seedanceReq := m10["doubao-seedance-2.5"]
	require.NotNil(t, seedanceReq.Pricing)
	require.Equal(t, BillingModePerRequest, seedanceReq.Pricing.BillingMode)
	require.NotNil(t, seedanceReq.Pricing.PerRequestPrice)
	require.InDelta(t, 10.0, *seedanceReq.Pricing.PerRequestPrice, 1e-12)
	require.Empty(t, seedanceReq.Pricing.Intervals)
	require.Nil(t, seedanceReq.Pricing.InputPrice, "按次价卡不应残留 token 输入价")

	seedanceTiered := m10["doubao-seedance-pro"]
	require.NotNil(t, seedanceTiered.Pricing)
	require.Equal(t, BillingModeVideo, seedanceTiered.Pricing.BillingMode)
	require.Nil(t, seedanceTiered.Pricing.PerRequestPrice)
	require.Len(t, seedanceTiered.Pricing.Intervals, 2, "分组价卡优先于 VideoPrice480P，不强行扩成 3 档")
	require.Equal(t, "480p", seedanceTiered.Pricing.Intervals[0].TierLabel)
	require.InDelta(t, 0.3, *seedanceTiered.Pricing.Intervals[0].PerRequestPrice, 1e-12)
	require.Equal(t, "720p", seedanceTiered.Pricing.Intervals[1].TierLabel)
	require.InDelta(t, 0.5, *seedanceTiered.Pricing.Intervals[1].PerRequestPrice, 1e-12)

	g20 := byGroup[20]
	require.False(t, g20.VideoRateIndependent)
	m20 := plazaModelsByName(g20.Models)
	flat := m20["doubao-seedance-2.5"]
	require.NotNil(t, flat.Pricing)
	require.Equal(t, BillingModeVideo, flat.Pricing.BillingMode)
	require.NotNil(t, flat.Pricing.PerRequestPrice)
	require.InDelta(t, 0.4, *flat.Pricing.PerRequestPrice, 1e-12)
}

func TestListGroups_GroupVideoPricesAndMiniMaxH3DisplayInPlaza(t *testing.T) {
	channels := []Channel{{
		ID: 1, Name: "media-ch", Status: StatusActive, GroupIDs: []int64{10, 20, 30},
		ModelPricing: []ChannelModelPricing{
			{
				Platform:        PlatformGrok,
				Models:          []string{"grok-imagine-video-1.5"},
				BillingMode:     BillingModeVideo,
				PerRequestPrice: testPtrFloat64(0.1),
				Intervals:       []PricingInterval{{TierLabel: "1080p", PerRequestPrice: testPtrFloat64(0.2)}},
			},
			{
				Platform: PlatformMiniMax,
				Models:   []string{"MiniMax-H3"},
			},
		},
		ModelMapping: map[string]map[string]string{
			PlatformGrok: {"grok-imagine-video": "grok-imagine-video"},
		},
	}}
	groups := []Group{
		{
			ID: 10, Name: "grok-override", Platform: PlatformGrok, RateMultiplier: 1,
			VideoRateIndependent: true, VideoRateMultiplier: 0.5,
			VideoPrice480P: testPtrFloat64(0.06),
			VideoModelPrices: map[string]map[string]float64{
				VideoPriceFamilyGrokImagineVideo15: {VideoBillingResolution720P: 0.14},
			},
		},
		{
			ID: 20, Name: "minimax-sec", Platform: PlatformMiniMax, RateMultiplier: 1,
			VideoModelPrices: map[string]map[string]float64{
				MiniMaxH3PriceFamily: {MiniMaxH3PerSecond: 0.02},
			},
		},
		{
			ID: 30, Name: "minimax-req", Platform: PlatformMiniMax, RateMultiplier: 1,
			VideoModelPrices: map[string]map[string]float64{
				MiniMaxH3PriceFamily: {"request_5s": 0.08},
			},
		},
	}

	svc := newPlazaServiceWithBilling(channels, groups,
		map[int64]string{10: PlatformGrok, 20: PlatformMiniMax, 30: PlatformMiniMax}, nil)
	out, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	byGroup := map[int64]PlazaGroup{}
	for _, g := range out {
		byGroup[g.ID] = g
	}

	// Grok 1.5: 480p 用分组平价 0.06, 720p 用分组模型专价 0.14, 1080p 回落渠道档位价 0.2
	grokModels := plazaModelsByName(byGroup[10].Models)
	v15 := grokModels["grok-imagine-video-1.5"].Pricing
	require.NotNil(t, v15)
	require.Equal(t, BillingModeVideo, v15.BillingMode)
	require.Len(t, v15.Intervals, 3)
	require.InDelta(t, 0.06, *v15.Intervals[0].PerRequestPrice, 1e-12)
	require.InDelta(t, 0.14, *v15.Intervals[1].PerRequestPrice, 1e-12)
	require.InDelta(t, 0.2, *v15.Intervals[2].PerRequestPrice, 1e-12)

	// Grok 标准版无渠道定价：480p 用分组价 0.06，720p 回落内置默认价，未配 1080p 不硬拼 1080p
	vStd := grokModels["grok-imagine-video"].Pricing
	require.NotNil(t, vStd)
	require.Equal(t, BillingModeVideo, vStd.BillingMode)
	require.Len(t, vStd.Intervals, 2)
	require.Equal(t, "480p", vStd.Intervals[0].TierLabel)
	require.InDelta(t, 0.06, *vStd.Intervals[0].PerRequestPrice, 1e-12)
	require.Equal(t, "720p", vStd.Intervals[1].TierLabel)
	require.InDelta(t, defaultGrokImagineVideoPrice720P, *vStd.Intervals[1].PerRequestPrice, 1e-12)

	// MiniMax-H3 按秒
	h3Sec := plazaModelsByName(byGroup[20].Models)["MiniMax-H3"].Pricing
	require.NotNil(t, h3Sec)
	require.Equal(t, BillingModeVideo, h3Sec.BillingMode)
	require.NotNil(t, h3Sec.PerRequestPrice)
	require.InDelta(t, 0.02, *h3Sec.PerRequestPrice, 1e-12)

	// MiniMax-H3 按 5s 次计费
	h3Req := plazaModelsByName(byGroup[30].Models)["MiniMax-H3"].Pricing
	require.NotNil(t, h3Req)
	require.Equal(t, BillingModePerRequest, h3Req.BillingMode)
	require.Len(t, h3Req.Intervals, 1)
	require.Equal(t, "5s", h3Req.Intervals[0].TierLabel)
	require.InDelta(t, 0.08, *h3Req.Intervals[0].PerRequestPrice, 1e-12)
}

func TestListGroups_ChannelWildcardPerRequestOnAccountListedModel(t *testing.T) {
	channels := []Channel{{
		ID: 1, Name: "ch", Status: StatusActive, GroupIDs: []int64{10},
		ModelPricing: []ChannelModelPricing{{
			Platform:        PlatformOpenAI,
			Models:          []string{"doubao-seedance-*"},
			BillingMode:     BillingModePerRequest,
			PerRequestPrice: testPtrFloat64(1.5),
		}},
	}}
	groups := []Group{{ID: 10, Name: "g", Platform: PlatformOpenAI, RateMultiplier: 1}}
	svc := newPlazaServiceWithBilling(channels, groups, map[int64]string{10: PlatformOpenAI}, nil)
	svc.SetListedModelCatalog(stubPlazaListedModels{models: map[string][]string{
		PlatformOpenAI: {"doubao-seedance-2.5"},
	}})

	out, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	m := plazaModelsByName(out[0].Models)["doubao-seedance-2.5"]
	require.NotNil(t, m.Pricing)
	require.Equal(t, BillingModePerRequest, m.Pricing.BillingMode)
	require.NotNil(t, m.Pricing.PerRequestPrice)
	require.InDelta(t, 1.5, *m.Pricing.PerRequestPrice, 1e-12)
}

func TestListGroups_DeepSeekToolPricingAndOverrides(t *testing.T) {
	// DeepSeek 分组自动暴露 5 个工具端点：
	// - 分组显式列 / per_request 价卡 > 渠道 per_request > 官方默认价；
	// - Fetch 未配时回落 Search（再回落官方默认），显式 0 表示免费不回落；
	// - 渠道通配 token 价卡 deepseek-* 不可劫持工具的按次定价；
	// - ToolRateIndependent / ToolRateMultiplier 透传至 PlazaGroup。
	channels := []Channel{{
		ID: 1, Name: "ds-ch", Status: StatusActive, GroupIDs: []int64{10, 20},
		ModelPricing: []ChannelModelPricing{
			{
				Platform:    PlatformDeepseek,
				Models:      []string{"deepseek-*"},
				BillingMode: BillingModeToken,
				InputPrice:  testPtrFloat64(0.28e-6),
				OutputPrice: testPtrFloat64(0.42e-6),
			},
			{
				Platform:    PlatformDeepseek,
				Models:      []string{"deepseek-chat"},
				BillingMode: BillingModeToken,
				InputPrice:  testPtrFloat64(0.28e-6),
				OutputPrice: testPtrFloat64(0.42e-6),
			},
			{
				Platform:        PlatformDeepseek,
				Models:          []string{DeepSeekToolsLabelOCR},
				BillingMode:     BillingModePerRequest,
				PerRequestPrice: testPtrFloat64(0.002),
			},
		},
	}}
	groups := []Group{
		{
			ID: 10, Name: "ds-custom", Platform: PlatformDeepseek, RateMultiplier: 0.3,
			ToolRateIndependent: true, ToolRateMultiplier: 1.0,
			SearchPricePer1k:             testPtrFloat64(4.0),
			FetchPricePer1k:              testPtrFloat64(0), // 显式免费
			OCRPricePer1kPages:           testPtrFloat64(1.2),
			AudioTTSPricePerMillionChars: testPtrFloat64(200),
			ModelPricing: []ChannelModelPricing{{
				Platform:        PlatformDeepseek,
				Models:          []string{DeepSeekToolsLabelASR},
				BillingMode:     BillingModePerRequest,
				PerRequestPrice: testPtrFloat64(0.5),
			}},
		},
		{
			ID: 20, Name: "ds-default", Platform: PlatformDeepseek, RateMultiplier: 0.05,
			SearchPricePer1k: testPtrFloat64(6.0), // Fetch 未配置，应继承 Search=6/1k=0.006
		},
	}

	svc := newPlazaServiceWithBilling(channels, groups,
		map[int64]string{10: PlatformDeepseek, 20: PlatformDeepseek}, nil)
	out, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 2)

	byGroup := map[int64]PlazaGroup{}
	for _, g := range out {
		byGroup[g.ID] = g
	}

	g10 := byGroup[10]
	require.True(t, g10.ToolRateIndependent)
	require.InDelta(t, 1.0, g10.ToolRateMultiplier, 1e-12)
	m10 := plazaModelsByName(g10.Models)
	for _, toolName := range DeepSeekToolCatalogOrder {
		tm, ok := m10[toolName]
		require.Truef(t, ok, "DeepSeek group must expose %s", toolName)
		require.NotNil(t, tm.Pricing)
		require.Equal(t, BillingModePerRequest, tm.Pricing.BillingMode)
		require.Nil(t, tm.Pricing.InputPrice, "wildcard token card must not hijack tool %s", toolName)
		require.NotNil(t, tm.Pricing.PerRequestPrice)
	}
	require.InDelta(t, 0.004, *m10[DeepSeekToolsLabelSearch].Pricing.PerRequestPrice, 1e-12)
	require.InDelta(t, 0.0, *m10[DeepSeekToolsLabelFetch].Pricing.PerRequestPrice, 1e-12, "explicit 0 fetch must stay free")
	require.InDelta(t, 0.0012, *m10[DeepSeekToolsLabelOCR].Pricing.PerRequestPrice, 1e-12, "group OCR column overrides channel OCR card")
	require.InDelta(t, 200.0, *m10[DeepSeekToolsLabelTTS].Pricing.PerRequestPrice, 1e-12)
	require.InDelta(t, 0.5, *m10[DeepSeekToolsLabelASR].Pricing.PerRequestPrice, 1e-12, "group per_request card overrides official ASR")

	g20 := byGroup[20]
	require.False(t, g20.ToolRateIndependent)
	m20 := plazaModelsByName(g20.Models)
	require.InDelta(t, 0.006, *m20[DeepSeekToolsLabelSearch].Pricing.PerRequestPrice, 1e-12)
	require.InDelta(t, 0.006, *m20[DeepSeekToolsLabelFetch].Pricing.PerRequestPrice, 1e-12, "unset fetch inherits search")
	require.InDelta(t, 0.002, *m20[DeepSeekToolsLabelOCR].Pricing.PerRequestPrice, 1e-12, "channel OCR card overrides official default")
	require.InDelta(t, 300.0, *m20[DeepSeekToolsLabelTTS].Pricing.PerRequestPrice, 1e-12)
	require.InDelta(t, 0.78, *m20[DeepSeekToolsLabelASR].Pricing.PerRequestPrice, 1e-12)
}
