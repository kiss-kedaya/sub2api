// Adapted from ranxi2001/sub2api @ 9620d315, LGPL-3.0-or-later.
package service

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"strings"
	"time"
)

// recordUsageBillingModel 按计费模型来源、composite 规则与无价兜底确定计费模型。
func (s *GatewayService) recordUsageBillingModel(ctx context.Context, apiKey *APIKey, result *ForwardResult, fields ChannelUsageFields) string {
	concreteBillingModel := forwardResultBillingModel(result.Model, result.UpstreamModel)
	billingModel := concreteBillingModel
	if fields.BillingModelSource == BillingModelSourceChannelMapped && fields.ChannelMappedModel != "" {
		billingModel = fields.ChannelMappedModel
	}
	if fields.BillingModelSource == BillingModelSourceRequested && fields.OriginalModel != "" {
		billingModel = fields.OriginalModel
	}
	// composite 分组的公开别名（如 all/claude）会经 OriginalModel/ChannelMappedModel
	// 进入上面的来源覆盖：任意别名查无价会静默落 $0，含家族词的别名则被价格表的
	// 家族模糊匹配错计（如 Opus 流量按 Sonnet 兜底价）。除非管理员为别名显式配置了
	// 渠道定价（OpenRouter 式自定价），composite 请求一律按实际转发的具体模型计费。
	if apiKey.Group != nil && apiKey.Group.Platform == PlatformComposite {
		billingModel = s.compositeBillableModel(ctx, apiKey, billingModel, concreteBillingModel)
	}
	// 通用兜底（与 OpenAI 路径的 usageBillingModelCandidates 语义对齐）：
	// 选定模型查不到任何价格时回退到实际转发的具体模型。已定价流量不受影响。
	return s.billableModelWithFallback(ctx, apiKey, billingModel, result.UpstreamModel, result.Model)
}

// cacheStoragePrice 返回计费模型的显式缓存存储单价；计费模型未配置时依次回退到实际转发模型与请求模型。
func (s *GatewayService) cacheStoragePrice(result *ForwardResult, billingModel string) float64 {
	if s.billingService == nil {
		return 0
	}
	for _, model := range []string{billingModel, result.UpstreamModel, result.Model} {
		if strings.TrimSpace(model) == "" {
			continue
		}
		if price := s.billingService.CacheStoragePricePerTokenHour(model); price > 0 {
			return price
		}
	}
	return 0
}

// cacheStorageCost 返回 result 的显式缓存存储费（USD，倍率前）。
func (s *GatewayService) cacheStorageCost(result *ForwardResult, billingModel string) float64 {
	if result.CacheStorageTokenHours <= 0 {
		return 0
	}
	return result.CacheStorageTokenHours * s.cacheStoragePrice(result, billingModel)
}

// isCacheStorageOnlyResult 判断 result 是否只有显式缓存存储费（延长有效期）：不含 token 用量，不算一次模型请求。
func isCacheStorageOnlyResult(result *ForwardResult) bool {
	u := result.Usage
	return result.CacheStorageTokenHours > 0 && u.InputTokens == 0 && u.OutputTokens == 0 &&
		u.CacheCreationInputTokens == 0 && u.CacheReadInputTokens == 0 && u.ImageOutputTokens == 0 &&
		result.ImageCount == 0 && result.AudioUsage == nil && result.SearchCount == 0
}

// CacheStoragePriced 判断 RecordUsage 对 input.Result 采用的计费模型（含回退候选）是否配置了显式缓存存储单价。
func (s *GatewayService) CacheStoragePriced(ctx context.Context, input *RecordUsageInput) bool {
	billingModel := s.recordUsageBillingModel(ctx, input.APIKey, input.Result, input.ChannelUsageFields)
	return s.cacheStoragePrice(input.Result, billingModel) > 0
}

// QuoteUsageCost 按与 RecordUsage 相同的倍率、计费模型与定价计算 input.Result 的应付金额（倍率后 USD），不记录也不扣费。
func (s *GatewayService) QuoteUsageCost(ctx context.Context, input *RecordUsageInput) float64 {
	user := input.User
	if user == nil {
		user = input.APIKey.User
	}
	multiplier, imageMultiplier, pricingAt := s.geminiCacheUsageMultipliers(ctx, input.APIKey, user, input.PricingAt)
	billingModel := s.recordUsageBillingModel(ctx, input.APIKey, input.Result, input.ChannelUsageFields)
	cost := s.calculateRecordUsageCost(ctx, input.Result, input.APIKey, billingModel, multiplier, imageMultiplier, pricingAt)
	if cost == nil {
		return 0
	}
	return cost.ActualCost
}

// addCacheStorageToAccountStatsCost 把显式缓存存储费按官方价叠加到账号统计成本。
// 账号统计定价只覆盖 token 部分；统计成本为空时成本统计回落 total_cost，其中已含存储费。
func addCacheStorageToAccountStatsCost(usageLog *UsageLog, storageCost float64) {
	if usageLog == nil || usageLog.AccountStatsCost == nil || storageCost <= 0 {
		return
	}
	cost := *usageLog.AccountStatsCost + storageCost
	usageLog.AccountStatsCost = &cost
}

func (s *GatewayService) geminiCacheUsageMultipliers(ctx context.Context, key *APIKey, user *User, pricingAt time.Time) (float64, float64, time.Time) {
	multiplier := 1.0
	if s.cfg != nil {
		multiplier = s.cfg.Default.RateMultiplier
	}
	if key.GroupID != nil && key.Group != nil {
		multiplier = key.Group.RateMultiplier
		if user != nil {
			multiplier = s.ResolveUserGroupRateMultiplier(ctx, user.ID, *key.GroupID, multiplier)
		}
	}
	if pricingAt.IsZero() {
		pricingAt = timezone.Now()
	}
	multiplier, imageMultiplier := computePeakAwareMultipliers(key, multiplier, pricingAt)
	return multiplier, imageMultiplier, pricingAt
}

func (s *BillingService) CacheStoragePricePerTokenHour(model string) float64 {
	if s != nil && s.pricingService != nil {
		if pricing := s.pricingService.GetIdentifiedModelPricing(model); pricing != nil && pricing.CacheStorageCostPerTokenPerHour > 0 {
			return pricing.CacheStorageCostPerTokenPerHour
		}
	}
	return builtinGeminiCacheStoragePrice(model)
}
