package handler

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/tidwall/gjson"
)

// inflightReservationEstimator 估算单请求在途预留金额（USD）。
type inflightReservationEstimator interface {
	EstimateInflightReservation(ctx context.Context, apiKey *service.APIKey, model string, bodyBytes, maxTokens int) float64
}

// requestMaxOutputTokens 从请求体中提取输出 token 上限（兼容 Anthropic / OpenAI Chat / Responses / Gemini）。
func requestMaxOutputTokens(body []byte) int {
	for _, path := range []string{"max_tokens", "max_completion_tokens", "max_output_tokens", "generationConfig.maxOutputTokens", "generation_config.max_output_tokens"} {
		if v := gjson.GetBytes(body, path); v.Exists() && v.Type == gjson.Number && v.Int() > 0 {
			return int(v.Int())
		}
	}
	return 0
}

// reserveInflightBalance 在 CheckBillingEligibility 之后为余额模式请求登记在途预留。
// 返回的 release 必须 defer 调用（覆盖成功/错误/客户端断开/failover 等所有路径）；
// 开关关闭、订阅模式、估算失败或 Redis 故障时返回 no-op（fail-open）。
func reserveInflightBalance(
	ctx context.Context,
	billing *service.BillingCacheService,
	estimator inflightReservationEstimator,
	apiKey *service.APIKey,
	subscription *service.UserSubscription,
	model string,
	body []byte,
) (func(), error) {
	noop := func() {}
	if billing == nil || estimator == nil || apiKey == nil || apiKey.User == nil || !billing.InflightReservationEnabled() {
		return noop, nil
	}
	if apiKey.Group != nil && apiKey.Group.IsSubscriptionType() && subscription != nil {
		return noop, nil
	}
	estimate := estimator.EstimateInflightReservation(ctx, apiKey, model, len(body), requestMaxOutputTokens(body))
	if estimate <= 0 {
		return noop, nil
	}
	return billing.ReserveInflightBalance(ctx, apiKey.User, apiKey.Group, subscription, estimate)
}
