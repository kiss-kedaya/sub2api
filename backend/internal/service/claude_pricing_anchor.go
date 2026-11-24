package service

// Claude 模型计费锚点校正。
//
// 修复前：请求计费锚定「key 主组」（apiKey.Group / apiKey.GroupID）——倍率取主组
// RateMultiplier、单价取主组渠道价。当一个非 Claude 分组（如 0.12 倍率的 grok 组
// group#25）通过 /v1/messages 收到 claude-* 请求时，这条链路会把组内 Grok 档价
// 与 0.12 倍率套到真实打到 Claude 上游的 Claude 模型上，实测低计 8.4~13.9 倍。
//
// 修复口径（只纠正跨目录/跨平台错配，不改正常场景）：
//   - 触发条件（全部满足才算错配）：
//     1. 计费模型属于 Claude 族；
//     2. 实际选中的分组不是 Anthropic 系平台（grok/CN 供应商/openai/composite…）；
//     3. 该分组为这个模型配有渠道档价（那档价属于供应商目录，不是 Claude 价）。
//   - 校正动作：
//     1. 定价：跳过该分组的渠道档价，改用全局 Claude 真价（LiteLLM → fallbackPrices
//        的 claude-* 表）；分组 token 卡（管理员显式配的价）仍然有效。
//     2. 倍率：改按「能服务 Claude 的上游分组」解析——优先实际渠道所属分组，其次
//        从 key 的候选分组里挑第一个 Anthropic 系分组；找不到时保留原值（组内倍率），
//        由准入闸门负责拒绝，不在这里发明倍率。
//
// 正常场景（Anthropic 分组 + claude 模型，或 OpenAI 分组 + gpt 模型）两个条件都
// 不满足，走原逻辑，计费结果逐位一致。

import (
	"context"
	"strings"
	"sync/atomic"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

// globalClaudePricingResolver 保存一个只查全局价（不查分组渠道价）的定价解析器，
// 供「Claude 真价锚点」在计费路径上取 Claude 官方价（LiteLLM → fallback）。
var (
	claudePricingFallbackResolver atomic.Value // *BillingService
	// claudeFallbackPricingTable 在尚无全局解析器时兜底：用 fallbackPrices 的
	// claude-* 官方价表（与服务内所有非 LiteLLM 价格同源）。
	claudeFallbackPricingTable atomic.Value // *BillingService
)

// claudePricingAnchorContextKey 在请求 ctx 上固定「本次请求的计费锚点分组」。
// 由网关入口在选中账号后写入；recordUsageCore / OpenAI RecordUsage 读取后覆盖
// 默认的 apiKey.GroupID / apiKey.Group。
type claudePricingAnchorContextKey struct{}

type claudePricingAnchor struct {
	// BillingGroup 是实际服务本次请求的分组（通常等于账号所属分组）。
	BillingGroup *Group
	// BillingGroupID 与 BillingGroup.ID 一致，单独保留以兼容只用 ID 的调用方。
	BillingGroupID int64
	// ClaudeFallback 表示必须优先使用 Claude 真价（跳过供应商目录的渠道档价）。
	ClaudeFallback bool
}

// WithClaudePricingAnchor 记录本次请求的计费锚点分组。
func WithClaudePricingAnchor(ctx context.Context, group *Group, claudeFallback bool) context.Context {
	if ctx == nil || group == nil || group.ID <= 0 {
		return ctx
	}
	return context.WithValue(ctx, claudePricingAnchorContextKey{}, claudePricingAnchor{
		BillingGroup:   group,
		BillingGroupID: group.ID,
		ClaudeFallback: claudeFallback,
	})
}

func claudePricingAnchorFromContext(ctx context.Context) (claudePricingAnchor, bool) {
	if ctx == nil {
		return claudePricingAnchor{}, false
	}
	anchor, ok := ctx.Value(claudePricingAnchorContextKey{}).(claudePricingAnchor)
	if !ok || anchor.BillingGroupID <= 0 {
		return claudePricingAnchor{}, false
	}
	return anchor, true
}

// claudeCatalogMispricingGroup 返回「该分组对 Claude 模型错配了供应商档价」时
// 的分组，用于判定每个请求是否必须锚定 Claude 真价。group 为空时返回 nil。
func claudeCatalogMispricingGroup(group *Group, billingModel string) *Group {
	if group == nil || group.ID <= 0 {
		return nil
	}
	if !isClaudeModel(billingModel) {
		return nil
	}
	if strings.TrimSpace(group.Platform) == PlatformAnthropic {
		return nil
	}
	if matchGroupModelPricing(group, billingModel) == nil {
		// 分组没有为这个 Claude 模型配渠道档价：走全局 Claude 价，无需校正。
		return nil
	}
	return group
}

// effectiveBillingGroupForBilling 是计费热路径专用：只有请求显式钉了锚点、
// 或存在 Claude 真价上下文时才做分组查询；其余情况直接返回 key 主组，
// 保证「正常场景零额外开销、零行为变化」。
func (s *GatewayService) effectiveBillingGroupForBilling(ctx context.Context, apiKey *APIKey) *Group {
	if anchor, ok := claudePricingAnchorFromContext(ctx); ok && anchor.BillingGroup != nil {
		return anchor.BillingGroup
	}
	if state, ok := claudeFallbackPricingStateFromContext(ctx); ok {
		return state.group
	}
	if apiKey != nil {
		return apiKey.Group
	}
	return nil
}

// resolveClaudeBillingAnchor 计算实际使用的计费分组与「是否强制 Claude 真价」。
// 无锚点/无错配时原样返回传入的 apiKey.Group 与 apiKey.GroupID。
func (s *GatewayService) resolveClaudeBillingAnchor(ctx context.Context, apiKey *APIKey, billingModel string) (billingGroup *Group, billingGroupID *int64, claudeFallback bool) {
	group := s.effectiveBillingGroupForBilling(ctx, apiKey)
	if group == nil {
		return nil, nil, false
	}
	mismatched := claudeCatalogMispricingGroup(group, billingModel)
	if mismatched == nil {
		// 没有跨目录错配：保持既有锚点（key 主组）语义不变。
		if apiKey != nil && apiKey.Group != nil {
			return apiKey.Group, apiKey.GroupID, false
		}
		id := group.ID
		return group, &id, false
	}
	return mismatched, int64PtrValue(mismatched.ID), true
}

func int64PtrValue(v int64) *int64 {
	x := v
	return &x
}

// claudeUpstreamRateGroup 为 Claude 模型挑选「承载 Claude 的上游分组」，用于
// 解析 0.12 这类供应商折扣率不应套用的问题。
//
// 优先实际渠道所属分组；否则从 key 的候选分组里挑第一个 Anthropic 系分组。
// 都找不到时返回 nil，调用方保留原倍率（准入闸门会负责拒绝这类请求）。
func (s *GatewayService) claudeUpstreamRateGroup(ctx context.Context, apiKey *APIKey, billingGroup *Group) *Group {
	if billingGroup != nil && strings.TrimSpace(billingGroup.Platform) == PlatformAnthropic {
		return billingGroup
	}
	if apiKey == nil || len(apiKey.CandidateGroupIDs()) == 0 {
		return nil
	}
	for _, groupID := range apiKey.CandidateGroupIDs() {
		gid := groupID
		var group *Group
		if apiKey.Group != nil && apiKey.Group.ID == gid {
			group = apiKey.Group
		} else {
			group = s.GroupPolicyForRequest(ctx, gid)
		}
		if group != nil && strings.TrimSpace(group.Platform) == PlatformAnthropic {
			return group
		}
	}
	return nil
}

// adjustClaudeFallbackMultiplier 在「强制 Claude 真价」时把倍率改按上游 Claude
// 分组解析；解析不到时原样返回，绝不发明倍率。
func (s *GatewayService) adjustClaudeFallbackMultiplier(ctx context.Context, userID int64, apiKey *APIKey, billingGroup *Group, multiplier float64) float64 {
	if s == nil || apiKey == nil {
		return multiplier
	}
	upstreamGroup := s.claudeUpstreamRateGroup(ctx, apiKey, billingGroup)
	if upstreamGroup == nil || upstreamGroup.ID == billingGroupIDOf(billingGroup) {
		return multiplier
	}
	resolved := s.ResolveUserGroupRateMultiplier(ctx, userID, upstreamGroup.ID, upstreamGroup.RateMultiplier)
	if resolved <= 0 {
		return multiplier
	}
	if resolved != multiplier {
		logger.LegacyPrintf("service.gateway",
			"[Billing] Claude model served by non-Claude group %d; rebasing rate multiplier from %v (billing group) to %v (claude group %d)",
			billingGroupIDOf(billingGroup), multiplier, resolved, upstreamGroup.ID)
	}
	return resolved
}

func billingGroupIDOf(group *Group) int64 {
	if group == nil {
		return 0
	}
	return group.ID
}

// claudeFallbackOverrideResolved 在「强制 Claude 真价」时用全局 Claude 价
// （pricingService/LiteLLM → fallbackPrices 的 claude-* 表）替换解析结果，
// 不再使用该分组为 Claude 模型配置的供应商档价。
func claudeFallbackOverrideResolved(state claudeFallbackPricingState, resolved *ResolvedPricing) *ResolvedPricing {
	if !state.active || resolved == nil {
		return resolved
	}
	base := resolved.BasePricing
	if pricing := claudeOfficialPricingForModel(state.model); pricing != nil {
		base = pricing
	}
	if base == nil {
		return resolved
	}
	cp := *base
	return &ResolvedPricing{
		Mode:                   BillingModeToken,
		BasePricing:            &cp,
		Source:                 PricingSourceFallback,
		SupportsCacheBreakdown: cp.SupportsCacheBreakdown,
	}
}

// adjustClaudeFallbackMultiplierForErrors 是 OpenAI 网关版本：错误路径上没有
// GatewayService，按候选分组里第一个 Anthropic 系分组解析；
// 找不到时原样返回，绝不发明倍率。
func (s *OpenAIGatewayService) adjustClaudeFallbackMultiplier(ctx context.Context, userID int64, apiKey *APIKey, billingGroup *Group, multiplier float64) float64 {
	if s == nil || apiKey == nil {
		return multiplier
	}
	if billingGroup != nil && strings.TrimSpace(billingGroup.Platform) == PlatformAnthropic {
		return multiplier
	}
	if len(apiKey.CandidateGroupIDs()) == 0 {
		return multiplier
	}
	for _, groupID := range apiKey.CandidateGroupIDs() {
		gid := groupID
		var group *Group
		if apiKey.Group != nil && apiKey.Group.ID == gid {
			group = apiKey.Group
		} else if s.schedulerSnapshot != nil {
			if loaded, err := s.schedulerSnapshot.GetGroupByIDLite(ctx, gid); err == nil {
				group = loaded
			}
		}
		if group == nil || strings.TrimSpace(group.Platform) != PlatformAnthropic {
			continue
		}
		resolved := s.ResolveUserGroupRateMultiplier(ctx, userID, group.ID, group.RateMultiplier)
		if resolved <= 0 {
			return multiplier
		}
		if resolved != multiplier {
			logger.LegacyPrintf("service.openai_gateway",
				"[Billing] Claude model served by non-Claude group %d; rebasing rate multiplier from %v to %v (claude group %d)",
				billingGroupIDOf(billingGroup), multiplier, resolved, group.ID)
		}
		return resolved
	}
	return multiplier
}

// claudeFallbackPricingGroupContextKey 在计费 ctx 上标记「本次请求必须使用
// Claude 真价」，并钉住实际计费分组。ModelPricingResolver 读到后跳过该分组的
// 供应商渠道档价 / 分组 token 卡查询，只保留全局 Claude 价。
type claudeFallbackPricingContextKey struct{}

type claudeFallbackPricingState struct {
	group  *Group
	model  string
	active bool
}

func withClaudeFallbackPricingGroup(ctx context.Context, group *Group, model string, active bool) context.Context {
	if ctx == nil || group == nil {
		return ctx
	}
	return context.WithValue(ctx, claudeFallbackPricingContextKey{}, claudeFallbackPricingState{
		group: group, model: strings.TrimSpace(model), active: active,
	})
}

func claudeFallbackPricingStateFromContext(ctx context.Context) (claudeFallbackPricingState, bool) {
	if ctx == nil {
		return claudeFallbackPricingState{}, false
	}
	state, ok := ctx.Value(claudeFallbackPricingContextKey{}).(claudeFallbackPricingState)
	if !ok || state.group == nil {
		return claudeFallbackPricingState{}, false
	}
	return state, true
}

// claudeOfficialPricingForModel 取 Claude 模型官方价：
// 优先全局定价服务（LiteLLM 目录 → 内置回退表），未装配时用内置回退表。
func claudeOfficialPricingForModel(model string) *ModelPricing {
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" {
		return nil
	}
	if r, ok := claudePricingFallbackResolver.Load().(*BillingService); ok && r != nil {
		if pricing, err := r.GetModelPricing(model); err == nil && pricing != nil {
			return pricing
		}
	}
	if table, ok := claudeFallbackPricingTable.Load().(*BillingService); ok && table != nil {
		if pricing := table.getFallbackPricing(model); pricing != nil {
			cp := *pricing
			return &cp
		}
	}
	return nil
}

// claudeBillingFallbackCandidates 收集用于查 Claude 真价的候选模型名：
// 先计费模型本身，再补充请求/上游模型，用于渠道映射把 Claude 名字改写后的场景。
func claudeBillingFallbackCandidates(models ...string) []string {
	out := make([]string, 0, len(models))
	seen := make(map[string]struct{}, len(models))
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		key := strings.ToLower(model)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, model)
	}
	return out
}

// claudeFallbackBillingModel 依次尝试 Claude 真价，返回第一个能解析到价的名字。
// 全部失败返回空串，调用方保留原计费模型（记录告警，不静默套用供应商价）。
func (s *GatewayService) claudeFallbackBillingModel(models []string) string {
	if s == nil || s.billingService == nil {
		return ""
	}
	for _, model := range models {
		if _, err := s.billingService.GetModelPricing(model); err == nil {
			return model
		}
	}
	return ""
}

// claudeBillingModelCandidatesForGroup 在 Claude 错配场景下，把计费模型候选里
// 首个能解析到 Claude 真价的名字提到最前面；都不行时原样返回候选。
// 只重排候选、不增删，保证 OpenAI 计费链路的「首个有价候选即计费模型」语义不变。
func claudeBillingModelCandidatesForGroup(group *Group, models []string) []string {
	if group == nil || len(models) == 0 {
		return models
	}
	pricing := &BillingService{}
	pricing.initFallbackPricing()
	for idx, model := range models {
		if !isClaudeModel(model) {
			continue
		}
		if _, err := pricing.GetModelPricing(model); err != nil {
			continue
		}
		if idx == 0 {
			return models
		}
		reordered := make([]string, 0, len(models))
		reordered = append(reordered, model)
		reordered = append(reordered, models[:idx]...)
		reordered = append(reordered, models[idx+1:]...)
		return reordered
	}
	return models
}
