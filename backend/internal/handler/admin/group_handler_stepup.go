package admin

import (
	"math"
	"reflect"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// 分组「改价」与搜索凭据写入的 step-up 2FA 门控。
//
// 背景：分组是计费倍率与各模态单价的落点（rate_multiplier、model_pricing、
// image/video/audio 单价、高峰倍率、利润控制阈值、search_provider_api_key）。
// 这些字段的 PUT 与专用 rate-multipliers 接口都能直接改用户实付价格或写入
// 搜索服务凭据，属资金/凭据敏感写操作；而分组配置本身（名称/平台/路由）不是
// 敏感数据，所以不能用「整个 /groups 前缀一刀切」的方式要求 2FA。
//
// 语义与路由级 step-up 保持一致：
//  1. step_up_enabled 关闭时放行（恢复门控引入前的行为）；
//  2. 真人 JWT 会话需最近完成 TOTP step-up；
//  3. 机器凭据（admin API key）一律 403 STEP_UP_ADMIN_API_KEY_FORBIDDEN；
//  4. 依赖缺失 fail-closed 返回 503，避免装配遗漏把门控静默关掉。

// SetStepUpDependencies 注入 step-up 2FA 门控依赖（由 wire 在装配后调用）。
func (h *GroupHandler) SetStepUpDependencies(
	totpService *service.TotpService,
	userService *service.UserService,
	settingService *service.SettingService,
) {
	if h == nil {
		return
	}
	h.totpService = totpService
	h.userService = userService
	h.settingService = settingService
}

func (h *GroupHandler) enforceGroupPricingStepUp(c *gin.Context) bool {
	if h == nil || h.totpService == nil || h.userService == nil {
		middleware.AbortWithError(c, 503, "STEP_UP_UNAVAILABLE", "Step-up verification service unavailable")
		return false
	}
	return middleware.EnforceStepUp(c, h.totpService, h.userService, h.settingService)
}

func groupRateAlmostEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}

// differentFloat64 reports whether the request carries a scalar price that differs
// from the stored value.
func differentFloat64(ptr *float64, current float64) bool {
	return ptr != nil && !groupRateAlmostEqual(*ptr, current)
}

// differentFloat64Ptr compares two optional prices ("nil = 使用默认价").
func differentFloat64Ptr(ptr *float64, current *float64) bool {
	if ptr == nil {
		return false
	}
	if *ptr < 0 {
		return current != nil
	}
	if current == nil {
		return true
	}
	return !groupRateAlmostEqual(*ptr, *current)
}

// differentOptionalLimit compares a tri-state optional price field (omitted / null / number).
func differentOptionalLimit(field optionalLimitField, current *float64) bool {
	if !field.set {
		return false
	}
	if field.value == nil || *field.value < 0 {
		return current != nil
	}
	if current == nil {
		return true
	}
	return !groupRateAlmostEqual(*field.value, *current)
}

func differentStringPtr(ptr *string, current *string) bool {
	if ptr == nil {
		return false
	}
	if current == nil {
		return strings.TrimSpace(*ptr) != ""
	}
	return *ptr != *current
}

// createGroupRequestTouchesPricing 判断建组请求是否写入非默认价格/倍率或写只读凭据。
func createGroupRequestTouchesPricing(req *CreateGroupRequest) bool {
	if req == nil {
		return false
	}
	// 默认倍率为 1；0 表示请求未携带该字段（服务端会拒绝 <=0），两者都不算改价。
	if req.RateMultiplier != 0 && !groupRateAlmostEqual(req.RateMultiplier, 1) {
		return true
	}
	if req.PeakRateEnabled || req.PeakRateMultiplier != nil {
		return true
	}
	if req.LongContextPricingEnabled || len(req.ModelPricing) > 0 {
		return true
	}
	if req.ImageRateIndependent || req.ImageRateMultiplier != nil || req.BatchImageDiscountMultiplier != nil || req.BatchImageHoldMultiplier != nil {
		return true
	}
	if req.VideoRateIndependent || req.VideoRateMultiplier != nil || len(req.VideoModelPrices) > 0 {
		return true
	}
	if req.ToolRateIndependent || (req.ToolRateMultiplier != nil && !groupRateAlmostEqual(*req.ToolRateMultiplier, 1)) {
		return true
	}
	if req.ImagePrice1K != nil || req.ImagePrice2K != nil || req.ImagePrice4K != nil {
		return true
	}
	if req.VideoPrice480P != nil || req.VideoPrice720P != nil || req.VideoPrice1080P != nil {
		return true
	}
	if req.WebSearchPricePerCall != nil || req.SearchPricePer1k != nil || req.FetchPricePer1k != nil || req.OCRPricePer1kPages != nil {
		return true
	}
	if req.GrokModerationRefusalPrice != nil {
		return true
	}
	if req.AudioRealtimePricePerMin != nil || req.AudioTtsPricePerMillionChars != nil || req.AudioSttPricePerHour != nil {
		return true
	}
	if req.ProfitControlEnabled || req.ProfitMinMargin != nil || req.ProfitSafetyBuffer != nil {
		return true
	}
	if req.SearchProviderAPIKey != nil && strings.TrimSpace(*req.SearchProviderAPIKey) != "" {
		return true
	}
	if req.SearchProviderBaseURL != nil && strings.TrimSpace(*req.SearchProviderBaseURL) != "" {
		return true
	}
	return false
}

// updateGroupRequestTouchesPricing 判断编辑请求是否真的改动价格/倍率或写入搜索凭据。
//
// 前端编辑表单会回传未修改的字段，所以这里与存储值逐项比较：只有真正改变
// 计费参数（或写入 search_provider_api_key）才要求 step-up，日常改名称/描述/
// 路由不打断。
func updateGroupRequestTouchesPricing(target *service.Group, req *UpdateGroupRequest) bool {
	if target == nil || req == nil {
		return false
	}
	if differentFloat64(req.RateMultiplier, target.RateMultiplier) {
		return true
	}
	if req.PeakRateEnabled != nil && *req.PeakRateEnabled != target.PeakRateEnabled {
		return true
	}
	if req.PeakStart != nil && *req.PeakStart != target.PeakStart {
		return true
	}
	if req.PeakEnd != nil && *req.PeakEnd != target.PeakEnd {
		return true
	}
	if differentFloat64(req.PeakRateMultiplier, target.PeakRateMultiplier) {
		return true
	}
	if req.LongContextPricingEnabled != nil && *req.LongContextPricingEnabled != target.LongContextPricingEnabled {
		return true
	}
	if req.ModelPricing != nil && !reflect.DeepEqual(*req.ModelPricing, target.ModelPricing) {
		return true
	}
	if (req.ImageRateIndependent != nil && *req.ImageRateIndependent != target.ImageRateIndependent) ||
		(req.VideoRateIndependent != nil && *req.VideoRateIndependent != target.VideoRateIndependent) ||
		(req.ToolRateIndependent != nil && *req.ToolRateIndependent != target.ToolRateIndependent) ||
		differentFloat64(req.ImageRateMultiplier, target.ImageRateMultiplier) ||
		differentFloat64(req.BatchImageDiscountMultiplier, target.BatchImageDiscountMultiplier) ||
		differentFloat64(req.BatchImageHoldMultiplier, target.BatchImageHoldMultiplier) ||
		differentFloat64(req.VideoRateMultiplier, target.VideoRateMultiplier) ||
		differentFloat64(req.ToolRateMultiplier, target.ToolRateMultiplier) {
		return true
	}
	if differentFloat64Ptr(req.ImagePrice1K, target.ImagePrice1K) ||
		differentFloat64Ptr(req.ImagePrice2K, target.ImagePrice2K) ||
		differentFloat64Ptr(req.ImagePrice4K, target.ImagePrice4K) ||
		differentFloat64Ptr(req.VideoPrice480P, target.VideoPrice480P) ||
		differentFloat64Ptr(req.VideoPrice720P, target.VideoPrice720P) ||
		differentFloat64Ptr(req.VideoPrice1080P, target.VideoPrice1080P) {
		return true
	}
	if req.VideoModelPrices != nil && !reflect.DeepEqual(req.VideoModelPrices, target.VideoModelPrices) {
		return true
	}
	effectivePlatform := target.Platform
	if strings.TrimSpace(req.Platform) != "" {
		effectivePlatform = strings.TrimSpace(req.Platform)
	}
	differentSharedOptionalPrice := func(field optionalLimitField, current *float64) bool {
		if effectivePlatform == service.PlatformDeepseek {
			return differentOptionalLimit(field, current)
		}
		return differentFloat64Ptr(field.value, current)
	}
	if differentFloat64Ptr(req.WebSearchPricePerCall, target.WebSearchPricePerCall) ||
		differentSharedOptionalPrice(req.SearchPricePer1k, target.SearchPricePer1k) ||
		differentSharedOptionalPrice(req.FetchPricePer1k, target.FetchPricePer1k) ||
		differentOptionalLimit(req.OCRPricePer1kPages, target.OCRPricePer1kPages) ||
		differentFloat64Ptr(req.GrokModerationRefusalPrice, target.GrokModerationRefusalPrice) {
		return true
	}
	if differentFloat64Ptr(req.AudioRealtimePricePerMin, target.AudioRealtimePricePerMin) ||
		differentSharedOptionalPrice(req.AudioTtsPricePerMillionChars, target.AudioTTSPricePerMillionChars) ||
		differentSharedOptionalPrice(req.AudioSttPricePerHour, target.AudioSTTPricePerHour) {
		return true
	}
	if req.ProfitControlEnabled != nil && *req.ProfitControlEnabled != target.ProfitControlEnabled {
		return true
	}
	if differentFloat64(req.ProfitMinMargin, target.ProfitMinMargin) ||
		differentFloat64(req.ProfitSafetyBuffer, target.ProfitSafetyBuffer) {
		return true
	}
	// search_provider_api_key 是只写不读的凭据：只要请求体带了非空值就要求 2FA。
	if req.SearchProviderAPIKey != nil && strings.TrimSpace(*req.SearchProviderAPIKey) != "" {
		return true
	}
	if differentStringPtr(req.SearchProviderBaseURL, target.SearchProviderBaseURL) {
		return true
	}
	return false
}
