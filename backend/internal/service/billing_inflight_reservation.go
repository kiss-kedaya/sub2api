package service

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/google/uuid"
)

// InflightBalanceReservationCache 余额在途预留的缓存能力（可选）。
// BillingCache 的 Redis 实现同时实现此接口；未实现时在途预留自动关闭（fail-open）。
type InflightBalanceReservationCache interface {
	// ReserveInflightBalance 原子地：清理过期预留；若用户已有在途预留且
	// balance - sum(在途) < amount 则拒绝；否则登记 requestID 的预留（ttl 后自动失效）。
	// 返回是否放行以及登记前的在途合计。
	ReserveInflightBalance(ctx context.Context, userID int64, requestID string, amount, balance float64, ttl time.Duration) (bool, float64, error)
	// ReleaseInflightBalance 释放 requestID 的预留（幂等）。
	ReleaseInflightBalance(ctx context.Context, userID int64, requestID string) error
}

const (
	defaultInflightReservationTTL      = 15 * time.Minute
	defaultInflightDefaultMaxTokens    = 8192
	inflightReservationReleaseTimeout  = 2 * time.Second
	inflightReservationReserveTimeout  = 2 * time.Second
	inflightInputBytesPerTokenEstimate = 4
)

func noopRelease() {}

func (s *BillingCacheService) inflightReservationConfig() (config.InflightReservationConfig, bool) {
	if s == nil || s.cfg == nil {
		return config.InflightReservationConfig{}, false
	}
	cfg := s.cfg.Billing.InflightReservation
	if !cfg.Enabled || s.cfg.RunMode == config.RunModeSimple {
		return cfg, false
	}
	return cfg, true
}

// InflightReservationEnabled 是否启用余额在途预留。
func (s *BillingCacheService) InflightReservationEnabled() bool {
	_, ok := s.inflightReservationConfig()
	return ok
}

// ReserveInflightBalance 在余额模式下为本次请求登记在途预留。
//
// 必须在 CheckBillingEligibility 通过后调用。返回的 release 必须在请求结束时调用
// （所有路径，建议 defer），多次调用安全。
//
// 以下情况直接放行且不登记预留（fail-open，保持旧行为）：
// 开关关闭 / 简易模式 / 订阅模式 / estimate <= 0 / 缓存不支持 / 余额读取失败 / Redis 执行失败。
// 仅当 Redis 明确判定 缓存余额 - 在途合计 < estimate（且已有在途请求）时返回 ErrInsufficientBalance。
func (s *BillingCacheService) ReserveInflightBalance(ctx context.Context, user *User, group *Group, subscription *UserSubscription, estimate float64) (func(), error) {
	cfg, ok := s.inflightReservationConfig()
	if !ok || user == nil {
		return noopRelease, nil
	}
	if group != nil && group.IsSubscriptionType() && subscription != nil {
		return noopRelease, nil
	}
	if cfg.MaxReservationUSD > 0 && estimate > cfg.MaxReservationUSD {
		estimate = cfg.MaxReservationUSD
	}
	if estimate <= 0 || math.IsNaN(estimate) || math.IsInf(estimate, 0) {
		return noopRelease, nil
	}
	rc, ok := s.cache.(InflightBalanceReservationCache)
	if !ok || rc == nil {
		return noopRelease, nil
	}

	balance, err := s.GetUserBalance(ctx, user.ID)
	if err != nil {
		logger.LegacyPrintf("service.billing_cache", "Warning: inflight reservation balance read failed for user %d (fail-open): %v", user.ID, err)
		return noopRelease, nil
	}

	ttl := defaultInflightReservationTTL
	if cfg.TTLSeconds > 0 {
		ttl = time.Duration(cfg.TTLSeconds) * time.Second
	}
	requestID := uuid.NewString()
	reserveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), inflightReservationReserveTimeout)
	allowed, inflight, err := rc.ReserveInflightBalance(reserveCtx, user.ID, requestID, estimate, balance, ttl)
	cancel()
	if err != nil {
		logger.LegacyPrintf("service.billing_cache", "Warning: inflight reservation failed for user %d (fail-open): %v", user.ID, err)
		return noopRelease, nil
	}
	if !allowed {
		logger.LegacyPrintf("service.billing_cache", "inflight reservation rejected: user=%d balance=%.6f inflight=%.6f estimate=%.6f", user.ID, balance, inflight, estimate)
		return noopRelease, ErrInsufficientBalance
	}

	var once sync.Once
	userID := user.ID
	return func() {
		once.Do(func() {
			relCtx, relCancel := context.WithTimeout(context.Background(), inflightReservationReleaseTimeout)
			defer relCancel()
			if err := rc.ReleaseInflightBalance(relCtx, userID, requestID); err != nil {
				logger.LegacyPrintf("service.billing_cache", "Warning: inflight reservation release failed for user %d (expires by ttl): %v", userID, err)
			}
		})
	}, nil
}

// EstimateInflightReservationCost 估算单请求的保守费用（USD，已乘倍率）。
//
//	input_tokens  = min(bodyBytes / 4, max_input_tokens)
//	output_tokens = min(max_tokens 或 default_max_tokens, max_output_tokens)
//	cost = (input_tokens × 输入单价 + output_tokens × 输出单价) × rateMultiplier
//
// 无法取得定价时返回 (0, false)，调用方应 fail-open。
func EstimateInflightReservationCost(billing *BillingService, cfg config.InflightReservationConfig, model string, bodyBytes, maxTokens int, rateMultiplier float64) (float64, bool) {
	if billing == nil || model == "" || rateMultiplier <= 0 {
		return 0, false
	}
	pricing, err := billing.GetModelPricing(model)
	if err != nil || pricing == nil {
		return 0, false
	}
	inputTokens := 0
	if bodyBytes > 0 {
		inputTokens = bodyBytes / inflightInputBytesPerTokenEstimate
	}
	if cfg.MaxInputTokens > 0 && inputTokens > cfg.MaxInputTokens {
		inputTokens = cfg.MaxInputTokens
	}
	outputTokens := maxTokens
	if outputTokens <= 0 {
		outputTokens = cfg.DefaultMaxTokens
		if outputTokens <= 0 {
			outputTokens = defaultInflightDefaultMaxTokens
		}
	}
	if cfg.MaxOutputTokens > 0 && outputTokens > cfg.MaxOutputTokens {
		outputTokens = cfg.MaxOutputTokens
	}
	cost := (float64(inputTokens)*pricing.InputPricePerToken + float64(outputTokens)*pricing.OutputPricePerToken) * rateMultiplier
	if cost <= 0 || math.IsNaN(cost) || math.IsInf(cost, 0) {
		return 0, false
	}
	return cost, true
}

func inflightReservationCfg(cfg *config.Config) config.InflightReservationConfig {
	if cfg == nil {
		return config.InflightReservationConfig{}
	}
	return cfg.Billing.InflightReservation
}

func groupRateMultiplierOrOne(group *Group) float64 {
	if group == nil {
		return 1
	}
	return group.RateMultiplier
}

// EstimateInflightReservation 使用网关计费倍率估算在途预留金额；0 表示不预留。
func (s *GatewayService) EstimateInflightReservation(ctx context.Context, apiKey *APIKey, model string, bodyBytes, maxTokens int) float64 {
	if s == nil || apiKey == nil || apiKey.User == nil {
		return 0
	}
	rate := groupRateMultiplierOrOne(apiKey.Group)
	if apiKey.GroupID != nil && apiKey.Group != nil {
		rate = s.getUserGroupRateMultiplier(ctx, apiKey.User.ID, *apiKey.GroupID, rate)
	}
	cost, ok := EstimateInflightReservationCost(s.billingService, inflightReservationCfg(s.cfg), model, bodyBytes, maxTokens, rate)
	if !ok {
		return 0
	}
	return cost
}

// EstimateInflightReservation 使用 OpenAI 网关计费倍率估算在途预留金额；0 表示不预留。
func (s *OpenAIGatewayService) EstimateInflightReservation(ctx context.Context, apiKey *APIKey, model string, bodyBytes, maxTokens int) float64 {
	if s == nil || apiKey == nil || apiKey.User == nil {
		return 0
	}
	rate := groupRateMultiplierOrOne(apiKey.Group)
	if apiKey.GroupID != nil && apiKey.Group != nil && s.userGroupRateResolver != nil {
		rate = s.userGroupRateResolver.Resolve(ctx, apiKey.User.ID, *apiKey.GroupID, rate)
	}
	cost, ok := EstimateInflightReservationCost(s.billingService, inflightReservationCfg(s.cfg), model, bodyBytes, maxTokens, rate)
	if !ok {
		return 0
	}
	return cost
}
