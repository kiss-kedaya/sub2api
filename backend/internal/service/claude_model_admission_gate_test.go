package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

// fixtures -----------------------------------------------------------------

// newGrokCatalogTestGroup 构造报告里的 group#25：grok 平台、0.12 倍率，
// 组内配了会命中 claude-* 的供应商档价（修复前被当作 Claude 的价格）。
func newGrokCatalogTestGroup(id int64, crossProtocol bool) *Group {
	return &Group{
		ID:                             id,
		Name:                           "grok",
		Platform:                       PlatformGrok,
		Status:                         StatusActive,
		RateMultiplier:                 0.12,
		CrossProtocolConversionEnabled: crossProtocol,
		ModelPricing: []ChannelModelPricing{
			{
				Platform:    PlatformGrok,
				Models:      []string{"grok-4.5", "grok-*", "claude-*"},
				InputPrice:  floatPtr(2e-6),
				OutputPrice: floatPtr(6e-6),
			},
		},
	}
}

func newGrokCatalogTestAccount(id int64) Account {
	return Account{
		ID:          id,
		Name:        "grok-acc",
		Platform:    PlatformGrok,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Credentials: map[string]any{"api_key": "sk-test"},
	}
}

// 准入闸门 ---------------------------------------------------------------

// 漏洞主链：grok 分组收到 claude 模型必须拒绝（/v1/messages 返回 404
// model_not_found，与 /v1/chat/completions 一致）。
func TestClaudeModelAdmission_BlocksSupplierGroupForClaudeModel(t *testing.T) {
	group := newGrokCatalogTestGroup(25, false)
	account := newGrokCatalogTestAccount(1)

	require.True(t, claudeModelAdmission("claude-opus-4-8", PlatformGrok, group, &account))
	require.True(t, claudeModelAdmission("claude-sonnet-4-5", PlatformGrok, group, &account))

	// 同分组的 grok 模型不受影响。
	require.False(t, claudeModelAdmission("grok-4.5", PlatformGrok, group, &account))
}

// 正常场景不受影响。
func TestClaudeModelAdmission_KeepsExistingTraffic(t *testing.T) {
	grokGroup := newGrokCatalogTestGroup(25, false)
	grokAccount := newGrokCatalogTestAccount(1)

	require.False(t, claudeModelAdmission("claude-opus-4-8", PlatformGrok, grokGroup, nil))
	require.False(t, claudeModelAdmission("claude-opus-4-8", "", nil, &grokAccount))

	claudeAccount := Account{ID: 2, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Status: StatusActive}
	require.False(t, claudeModelAdmission("claude-opus-4-8", PlatformAnthropic, &Group{ID: 7, Platform: PlatformAnthropic}, &claudeAccount))

	openAIAccount := Account{ID: 3, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive}
	require.False(t, claudeModelAdmission("claude-opus-4-8", PlatformOpenAI, &Group{ID: 8, Platform: PlatformOpenAI}, &openAIAccount))
}

// 分组显式开启跨协议转换时闸门不否决（不误伤合法部署）。
func TestClaudeModelAdmission_CrossProtocolGroupNotBlocked(t *testing.T) {
	account := newGrokCatalogTestAccount(1)
	require.False(t, claudeModelAdmission("claude-opus-4-8", PlatformGrok, newGrokCatalogTestGroup(25, true), &account))
	require.True(t, claudeModelAdmission("claude-opus-4-8", PlatformGrok, newGrokCatalogTestGroup(25, false), &account))
}

// key 路由层的“分组可用”判定必须拦下同场景。
func TestClaudeModelAdmission_KeyRouteGroupUsability(t *testing.T) {
	grokGroup := newGrokCatalogTestGroup(25, false)
	require.False(t, groupUsableForRequest(grokGroup, PlatformAnthropic, "claude-opus-4-8",
		map[string]struct{}{PlatformGrok: {}}))

	open := newGrokCatalogTestGroup(25, true)
	require.True(t, groupUsableForRequest(open, PlatformAnthropic, "claude-opus-4-8",
		map[string]struct{}{PlatformGrok: {}}))

	require.True(t, groupUsableForRequest(grokGroup, PlatformGrok, "grok-4.5",
		map[string]struct{}{PlatformGrok: {}}))
}

// 计费锚点 ---------------------------------------------------------------

// 只有“Claude 模型 + 非 Anthropic 分组 + 组内为该模型配了档价”才算错配。
func TestClaudePricingAnchor_DetectsSupplierCatalogMispricing(t *testing.T) {
	grok := newGrokCatalogTestGroup(25, false)
	require.NotNil(t, claudeCatalogMispricingGroup(grok, "claude-opus-4-8"))
	require.Nil(t, claudeCatalogMispricingGroup(grok, "grok-4.5"))

	claudeGroup := &Group{ID: 7, Platform: PlatformAnthropic, Status: StatusActive}
	require.Nil(t, claudeCatalogMispricingGroup(claudeGroup, "claude-opus-4-8"))
}

// 修复前：组内 Grok 档价（$2/$6 per M）抢先命中；修复后必须命中 Claude 真价。
func TestClaudePricingAnchor_ClaudeFallbackUsesRealClaudePrices(t *testing.T) {
	billingSvc := NewBillingService(nil, nil)
	resolver := NewModelPricingResolver(nil, billingSvc)
	grok := newGrokCatalogTestGroup(25, false)
	gid := grok.ID

	before := resolver.Resolve(context.Background(), PricingInput{Model: "claude-opus-4-8", GroupID: &gid, Group: grok})
	require.NotNil(t, before.BasePricing)
	require.Equal(t, 2e-6, before.BasePricing.InputPricePerToken,
		"修复前的漏洞现象：组内供应商档价抢先命中")

	ctx := withClaudeFallbackPricingGroup(context.Background(), grok, "claude-opus-4-8", true)
	after := resolver.Resolve(ctx, PricingInput{Model: "claude-opus-4-8", GroupID: &gid, Group: grok})
	require.NotNil(t, after.BasePricing)
	require.Equal(t, 5e-6, after.BasePricing.InputPricePerToken, "应为 Claude Opus 真价 $5/M")
	require.Equal(t, 25e-6, after.BasePricing.OutputPricePerToken)
	require.Equal(t, 0.5e-6, after.BasePricing.CacheReadPricePerToken)
}

// 两个价差极大的 Claude 模型必须按各自真价算出不同金额。
func TestClaudePricingAnchor_TwoClaudeModelsBillDifferentCosts(t *testing.T) {
	billingSvc := NewBillingService(nil, nil)
	resolver := NewModelPricingResolver(nil, billingSvc)
	grok := newGrokCatalogTestGroup(25, false)
	tokens := UsageTokens{InputTokens: 1_000_000, OutputTokens: 1_000_000}

	costFor := func(model string) float64 {
		ctx := withClaudeFallbackPricingGroup(context.Background(), grok, model, true)
		cost, err := billingSvc.CalculateTokenCostForRequest(TokenCostRequest{
			Ctx:            ctx,
			Model:          model,
			Group:          grok,
			Tokens:         tokens,
			RateMultiplier: 0.12,
			PricingAt:      time.Now(),
			Resolver:       resolver,
		})
		require.NoError(t, err)
		return cost.TotalCost
	}

	require.InDelta(t, 30.0, costFor("claude-opus-4-8"), 1e-9, "Opus-4.8: ($5 + $25)/M tokens")
	require.InDelta(t, 18.0, costFor("claude-sonnet-4-5"), 1e-9, "Sonnet-4.5: ($3 + $15)/M tokens")
}

// 回归锁：正常场景（Anthropic 分组 + Claude 模型）计费与修复前一致。
func TestClaudePricingAnchor_NormalAnthropicGroupUnchanged(t *testing.T) {
	billingSvc := NewBillingService(nil, nil)
	resolver := NewModelPricingResolver(nil, billingSvc)
	claudeGroup := &Group{ID: 7, Platform: PlatformAnthropic, RateMultiplier: 1, Status: StatusActive}
	gid := claudeGroup.ID

	require.Nil(t, claudeCatalogMispricingGroup(claudeGroup, "claude-sonnet-4-5"))

	resolved := resolver.Resolve(context.Background(), PricingInput{Model: "claude-sonnet-4-5", GroupID: &gid, Group: claudeGroup})
	require.Equal(t, 3e-6, resolved.BasePricing.InputPricePerToken)
	require.Equal(t, 15e-6, resolved.BasePricing.OutputPricePerToken)

	cost, err := billingSvc.CalculateTokenCostForRequest(TokenCostRequest{
		Ctx:            context.Background(),
		Model:          "claude-sonnet-4-5",
		Group:          claudeGroup,
		Tokens:         UsageTokens{InputTokens: 1000, OutputTokens: 500},
		RateMultiplier: 1,
		PricingAt:      time.Now(),
		Resolver:       resolver,
	})
	require.NoError(t, err)
	require.InDelta(t, 1000*3e-6+500*15e-6, cost.TotalCost, 1e-12)
}

// 方向性回归锁：闸门必须用「实际分组的上游平台」判定，而不是入站协议。
// 早期实现用入站协议（/v1/messages 恒为 anthropic）判定，导致漏洞根本没被堵住。
func TestClaudeModelAdmission_UsesUpstreamPlatformNotInboundProtocol(t *testing.T) {
	grokGroup := newGrokCatalogTestGroup(25, false)
	grokAcc := newGrokCatalogTestAccount(1)

	// 入站协议是 anthropic，但实际上游分组是 grok → 必须拒绝。
	require.True(t, claudeModelAdmission("claude-opus-4-8", PlatformGrok, grokGroup, &grokAcc),
		"grok 分组 + claude 模型 + grok 账号 必须拒绝（漏洞主链）")

	// 只有把上游平台错误地当成 anthropic 时才会放行——这正是要防的 bug。
	require.False(t, claudeModelAdmission("claude-opus-4-8", PlatformAnthropic, grokGroup, &grokAcc),
		"平台上为 anthropic 时不收紧（该分支不应被 /v1/messages 走到）")

	// 调度入口从分组解析上游平台，composite 才退到已解析目标平台。
	ctxComposite := WithResolvedTargetPlatform(context.Background(), PlatformGrok)
	require.Equal(t, PlatformGrok, upstreamPlatformForClaudeGate(ctxComposite, &Group{ID: 1, Platform: PlatformComposite}))
	require.Equal(t, PlatformGrok, upstreamPlatformForClaudeGate(context.Background(), grokGroup))
}

// 调度层：装上闸门后 grok 账号不可服务 claude 模型，非 Claude 模型不装闸门。
func TestClaudeModelAdmission_SchedulerGate(t *testing.T) {
	group := newGrokCatalogTestGroup(25, false)
	account := newGrokCatalogTestAccount(1)

	base := context.WithValue(context.Background(), ctxkey.Group, group)
	require.True(t, schedulerClaudeGateAllows(base, group, &account), "未装闸门时保留既有语义")

	gated := WithSchedulerClaudeModelGate(base, PlatformGrok, "claude-opus-4-8")
	require.False(t, schedulerClaudeGateAllows(gated, group, &account))

	// 开跨协议转换的分组不受影响。
	open := newGrokCatalogTestGroup(25, true)
	gatedOpen := WithSchedulerClaudeModelGate(context.WithValue(context.Background(), ctxkey.Group, open), PlatformGrok, "claude-opus-4-8")
	require.True(t, schedulerClaudeGateAllows(gatedOpen, open, &account))

	// 非 Claude 模型不装闸门（零开销）。
	require.Nil(t, WithSchedulerClaudeModelGate(base, PlatformGrok, "grok-4.5").Value(schedulerClaudeGateContextKey{}))
}

// claude-* 通配来自 xai 默认映射（默认开启），因此“账号被授权服务 Claude”
// 不能只看 IsModelSupported —— 闸门自身必须先于该方法否决供应商账号。
func TestClaudeModelAdmission_NotBypassedByGrokCrossClientWildcard(t *testing.T) {
	group := newGrokCatalogTestGroup(25, false)
	acc := newGrokCatalogTestAccount(1)
	// 无论默认映射有没有写入 claude-*，闸门都必须拒绝。
	require.True(t, claudeModelAdmission("claude-opus-4-8", PlatformGrok, group, &acc))
	require.True(t, claudeModelAdmission("claude-sonnet-4-5", PlatformGrok, group, &acc))
}

// 回归锁：正常场景（key 主组就是 Anthropic 分组）不得进入真价校正分支，
// 计费仍按该分组渠道价与原倍率计算。
func TestClaudePricingAnchor_NoAnchorKeepsPrimaryGroup(t *testing.T) {
	billingSvc := NewBillingService(nil, nil)
	claudeGroup := &Group{ID: 7, Platform: PlatformAnthropic, RateMultiplier: 1.2, Status: StatusActive}
	gid := claudeGroup.ID
	apiKey := &APIKey{ID: 1, GroupID: &gid, Group: claudeGroup}
	svc := &GatewayService{billingService: billingSvc}

	billingGroup, billingGroupID, claudeFallback := svc.resolveClaudeBillingAnchor(context.Background(), apiKey, "claude-sonnet-4-5")
	require.False(t, claudeFallback, "正常场景不得进入 Claude 真价校正")
	require.Equal(t, claudeGroup, billingGroup)
	require.NotNil(t, billingGroupID)
	require.Equal(t, int64(7), *billingGroupID)

	// 非 Claude 模型同样不校正。
	_, _, fallbackGrok := svc.resolveClaudeBillingAnchor(context.Background(), apiKey, "grok-4.5")
	require.False(t, fallbackGrok)
}
