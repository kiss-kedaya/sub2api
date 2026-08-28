//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// newTokenCostTestEnv 构造带渠道定价的计费环境：group 100 挂一个渠道，定价由 pricing 指定。
func newTokenCostTestEnv(t *testing.T, groupPlatform string, pricing []ChannelModelPricing, catalog *PricingService) (*BillingService, *ModelPricingResolver) {
	t.Helper()
	repo := &mockChannelRepository{
		listAllFn: func(_ context.Context) ([]Channel, error) {
			return []Channel{{
				ID: 1, Name: "ch", Status: StatusActive, GroupIDs: []int64{100}, ModelPricing: pricing,
			}}, nil
		},
		getGroupPlatformsFn: func(_ context.Context, _ []int64) (map[int64]string, error) {
			return map[int64]string{100: groupPlatform}, nil
		},
	}
	cs := NewChannelService(repo, nil, nil, nil)
	bs := NewBillingService(&config.Config{}, catalog)
	return bs, NewModelPricingResolver(cs, bs)
}

// geminiCatalogStub 无阶梯字段的 gemini 目录条目（用于验证"无数据即无阶梯"）。
func geminiCatalogStub() *PricingService {
	return newStubPricingServiceFromMap(map[string]*LiteLLMModelPricing{
		"gemini-2.5-pro": {
			Mode:                    "chat",
			InputCostPerToken:       1.25e-6,
			OutputCostPerToken:      10e-6,
			CacheReadInputTokenCost: 0.3125e-6,
		},
	})
}

// geminiLadderCatalogJSON 镜像真实目录的 gemini pro 条目：above_200k 绝对价，
// 由解析层折算成 200K 阈值 + 输入 ×2 / 输出 ×1.5。
const geminiLadderCatalogJSON = `{
	"gemini-2.5-pro": {"litellm_provider": "vertex_ai-language-models", "mode": "chat",
		"input_cost_per_token": 1.25e-06, "output_cost_per_token": 1e-05,
		"cache_read_input_token_cost": 3.125e-07,
		"input_cost_per_token_above_200k_tokens": 2.5e-06,
		"output_cost_per_token_above_200k_tokens": 1.5e-05,
		"cache_read_input_token_cost_above_200k_tokens": 6.25e-07}
}`

func geminiLadderCatalogStub(t *testing.T) *PricingService {
	t.Helper()
	return newStubPricingServiceFromJSON(t, geminiLadderCatalogJSON)
}

// 渠道平价之上叠加目录阶梯：与分组价卡/OpenAI 渠道价的既有语义一致，
// 超阈值整单按渠道价 × 目录倍率。
func TestCalculateTokenCostForRequest_ChannelFlatPriceStacksCatalogLadder(t *testing.T) {
	bs, resolver := newTokenCostTestEnv(t, PlatformGemini, []ChannelModelPricing{{
		Platform: PlatformGemini, Models: []string{"gemini-2.5-pro"}, BillingMode: BillingModeToken,
		InputPrice: testPtrFloat64(10e-6), OutputPrice: testPtrFloat64(40e-6),
	}}, geminiLadderCatalogStub(t))
	group := &Group{ID: 100, Platform: PlatformGemini, LongContextPricingEnabled: true}
	gid := group.ID
	resolved := resolver.Resolve(context.Background(), PricingInput{Model: "gemini-2.5-pro", GroupID: &gid, Group: group})
	require.Equal(t, PricingSourceChannel, resolved.Source)

	tokens := UsageTokens{InputTokens: 300000, OutputTokens: 1000}
	got, err := bs.CalculateTokenCostForRequest(TokenCostRequest{
		Ctx: context.Background(), Model: "gemini-2.5-pro", Group: group, Tokens: tokens, RateMultiplier: 1,
		Resolver: resolver, Resolved: resolved,
	})
	require.NoError(t, err)
	require.InDelta(t, 300000*10e-6*2, got.InputCost, 1e-9)
	require.InDelta(t, 1000*40e-6*1.5, got.OutputCost, 1e-9)
	require.True(t, got.LongContextBillingApplied)
}

// 渠道配置了定价区间时以渠道区间为准：目录阶梯（倍率）不再叠加。
func TestCalculateTokenCostForRequest_ChannelIntervalsOverrideCatalogLadder(t *testing.T) {
	bs, resolver := newTokenCostTestEnv(t, PlatformGemini, []ChannelModelPricing{{
		Platform: PlatformGemini, Models: []string{"gemini-2.5-pro"}, BillingMode: BillingModeToken,
		Intervals: []PricingInterval{{MinTokens: 0, InputPrice: testPtrFloat64(10e-6), OutputPrice: testPtrFloat64(40e-6)}},
	}}, geminiLadderCatalogStub(t))
	group := &Group{ID: 100, Platform: PlatformGemini, LongContextPricingEnabled: true}
	gid := group.ID
	resolved := resolver.Resolve(context.Background(), PricingInput{Model: "gemini-2.5-pro", GroupID: &gid, Group: group})
	require.Equal(t, PricingSourceChannel, resolved.Source)
	require.NotEmpty(t, resolved.Intervals)

	tokens := UsageTokens{InputTokens: 300000, OutputTokens: 1000}
	got, err := bs.CalculateTokenCostForRequest(TokenCostRequest{
		Ctx: context.Background(), Model: "gemini-2.5-pro", Group: group, Tokens: tokens, RateMultiplier: 1,
		Resolver: resolver, Resolved: resolved,
	})
	require.NoError(t, err)
	require.InDelta(t, 300000*10e-6, got.InputCost, 1e-9)
	require.InDelta(t, 1000*40e-6, got.OutputCost, 1e-9)
	require.False(t, got.LongContextBillingApplied)
}

// 目录阶梯跟随分组长上下文开关；开启时为整单换档（输入 ×2、输出 ×1.5）。
func TestCalculateTokenCostForRequest_CatalogLadderFollowsGroupToggle(t *testing.T) {
	bs, resolver := newTokenCostTestEnv(t, PlatformGemini, nil, geminiLadderCatalogStub(t))
	tokens := UsageTokens{InputTokens: 300000, OutputTokens: 1000}

	for _, enabled := range []bool{true, false} {
		group := &Group{ID: 100, Platform: PlatformGemini, LongContextPricingEnabled: enabled}
		gid := group.ID
		resolved := resolver.Resolve(context.Background(), PricingInput{Model: "gemini-2.5-pro", GroupID: &gid, Group: group})
		require.Equal(t, PricingSourceLiteLLM, resolved.Source)

		got, err := bs.CalculateTokenCostForRequest(TokenCostRequest{
			Ctx: context.Background(), Model: "gemini-2.5-pro", Group: group, Tokens: tokens, RateMultiplier: 1,
			Resolver: resolver, Resolved: resolved,
		})
		require.NoError(t, err)
		if enabled {
			// 300K × 1.25e-6 × 2 = 0.75；1000 × 10e-6 × 1.5 = 0.015
			require.InDelta(t, 0.765, got.ActualCost, 1e-9)
			require.True(t, got.LongContextBillingApplied)
		} else {
			require.InDelta(t, 0.385, got.ActualCost, 1e-9)
			require.False(t, got.LongContextBillingApplied)
		}
	}
}

// 目录条目没有阶梯字段时，开关开启也不产生阶梯。
func TestCalculateTokenCostForRequest_NoLadderFieldsMeansNoLadder(t *testing.T) {
	bs, resolver := newTokenCostTestEnv(t, PlatformGemini, nil, geminiCatalogStub())
	group := &Group{ID: 100, Platform: PlatformGemini, LongContextPricingEnabled: true}
	gid := group.ID
	resolved := resolver.Resolve(context.Background(), PricingInput{Model: "gemini-2.5-pro", GroupID: &gid, Group: group})

	tokens := UsageTokens{InputTokens: 300000, OutputTokens: 1000}
	got, err := bs.CalculateTokenCostForRequest(TokenCostRequest{
		Ctx: context.Background(), Model: "gemini-2.5-pro", Group: group, Tokens: tokens, RateMultiplier: 1,
		Resolver: resolver, Resolved: resolved,
	})
	require.NoError(t, err)
	require.InDelta(t, 0.385, got.ActualCost, 1e-9)
	require.False(t, got.LongContextBillingApplied)
}

func TestCalculateTokenCostForRequest_BuiltInPricingUsesUnifiedPath(t *testing.T) {
	bs, resolver := newTokenCostTestEnv(t, PlatformOpenAI, nil, newStubPricingServiceFromJSON(t, openAILadderCatalogJSON))
	group := &Group{ID: 100, Platform: PlatformOpenAI, LongContextPricingEnabled: true}
	gid := group.ID
	tokens := UsageTokens{InputTokens: 300000, OutputTokens: 1000}
	resolved := resolver.Resolve(context.Background(), PricingInput{Model: "gpt-5.4", GroupID: &gid, Group: group})

	got, err := bs.CalculateTokenCostForRequest(TokenCostRequest{
		Ctx: context.Background(), Model: "gpt-5.4", Group: group, Tokens: tokens, RateMultiplier: 1,
		Resolver: resolver, Resolved: resolved,
	})
	require.NoError(t, err)
	want, err := bs.CalculateCostUnified(CostInput{
		Ctx: context.Background(), Model: "gpt-5.4", GroupID: &gid, Group: group, Tokens: tokens,
		RequestCount: 1, RateMultiplier: 1, Resolver: resolver,
	})
	require.NoError(t, err)
	require.Equal(t, want, got)
	// 目录阶梯：超 272K 整单输入 ×2
	require.InDelta(t, 300000*2.5e-6*2, got.InputCost, 1e-9)
	require.True(t, got.LongContextBillingApplied)
}

func TestCalculateTokenCostForRequest_NoResolverFallsBackToCatalog(t *testing.T) {
	bs := NewBillingService(&config.Config{}, nil)
	tokens := UsageTokens{InputTokens: 1000, OutputTokens: 10}
	got, err := bs.CalculateTokenCostForRequest(TokenCostRequest{Model: "gpt-5.4", Tokens: tokens, RateMultiplier: 1})
	require.NoError(t, err)
	want, err := bs.CalculateCost("gpt-5.4", tokens, 1)
	require.NoError(t, err)
	require.Equal(t, want, got)
}
