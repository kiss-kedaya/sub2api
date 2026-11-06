package service

import (
	"context"
	"math"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/google/uuid"
)

// APIKeyQuotaReservationCache API Key 级 USD 配额的实时用量镜像 + 在途预占（可选能力）。
//
// 与余额在途预留（InflightBalanceReservationCache）相互独立：key 配额是硬上限，
// 不受「余额在途预留」开关影响；BillingCache 的 Redis 实现同时实现本接口，
// 未实现时 key 配额退回「只看鉴权快照」的旧行为（fail-open）。
type APIKeyQuotaReservationCache interface {
	// SeedAPIKeyQuotaUsage 用数据库里的 quota_used 播种实时用量镜像（只在字段不存在时写入）。
	SeedAPIKeyQuotaUsage(ctx context.Context, keyID int64, quotaUsed float64, ttl time.Duration) error
	// ReserveAPIKeyQuota 原子地：清理过期预留；若 quota_used + 在途 + estimate > quota 则拒绝；
	// 否则登记 requestID 的预估金额（ttl 后自动失效）。
	// ready=false 表示计数器还没有播种，调用方应先 SeedAPIKeyQuotaUsage 再重试。
	ReserveAPIKeyQuota(ctx context.Context, keyID int64, requestID string, estimate, quota float64, ttl time.Duration) (ready, allowed bool, used, inflight float64, err error)
	// ReleaseAPIKeyQuota 释放 requestID 的在途预留（幂等）。
	ReleaseAPIKeyQuota(ctx context.Context, keyID int64, requestID string) error
	// IncrementAPIKeyQuotaUsage 结算后把实际扣费累加到实时用量镜像；镜像不存在时跳过
	// （下一次准入会从数据库重新播种，数据库已经包含这笔费用）。
	IncrementAPIKeyQuotaUsage(ctx context.Context, keyID int64, cost float64) error
	// InvalidateAPIKeyQuotaUsage 丢弃实时用量镜像（管理员重置/调整配额后调用）。
	InvalidateAPIKeyQuotaUsage(ctx context.Context, keyID int64) error
	// RenewAPIKeyQuota 续期仍存活的在途预留（长请求期间防止 TTL 过期）。
	RenewAPIKeyQuota(ctx context.Context, keyID int64, requestID string, ttl time.Duration) (bool, error)
}

const (
	// apiKeyQuotaUsageTTL 实时用量镜像的存活时间。故意不长续期：一代 TTL 之后
	// 重新从数据库播种，镜像写入丢失（Redis 故障、进程崩溃）或管理端重置未清到
	// 镜像时都能在一个窗口内自愈。
	apiKeyQuotaUsageTTL = 10 * time.Minute
	// apiKeyQuotaReserveTimeout 单次准入预占的 Redis 超时（与余额在途预留一致）。
	apiKeyQuotaReserveTimeout = inflightReservationReserveTimeout
)

// ReserveAdmission 准入处的统一预占：key 级配额（quota>0 时始终生效）+
// 余额在途预留（billing.inflight_reservation.enabled 开启时）。
//
// 必须在 CheckBillingEligibility 通过后调用。返回的句柄可能为 nil（未预占）；
// 调用方须在 handler 结束时调用 HandlerDone（nil 安全），并在提交计费任务时
// 通过 Acquire 移交引用，使预留一直保持到实际扣费落地之后。
//
// key 配额维度的取舍（fail-open）：Redis 不可用、无法播种、无法定价时放行，
// 仅退回到鉴权快照比较（即修复前的旧行为）。理由：key 配额属于计费限额，
// 不应把一次 Redis 抖动放大成全站 429；余额/限流等其它门禁同样按 fail-open 处理。
// 代价是在 Redis 故障窗口内并发绕过重新出现，审计风险已在 PR 中说明。
func (s *BillingCacheService) ReserveAdmission(ctx context.Context, user *User, apiKey *APIKey, group *Group, subscription *UserSubscription, estimate float64) (*InflightReservation, error) {
	return s.reserveAdmission(ctx, user, apiKey, group, subscription, estimate, true)
}

// APIKeyQuotaReservationEnabled 当前部署是否支持 key 级配额预占（cache 实现了可选接口）。
func (s *BillingCacheService) APIKeyQuotaReservationEnabled() bool {
	if s == nil || s.cache == nil {
		return false
	}
	if s.cfg != nil && s.cfg.RunMode == config.RunModeSimple {
		return false
	}
	rc, ok := s.cache.(APIKeyQuotaReservationCache)
	return ok && rc != nil
}

// reserveAdmission 实现两种维度的预占。renew 控制是否启动续期协程。
func (s *BillingCacheService) reserveAdmission(
	ctx context.Context,
	user *User,
	apiKey *APIKey,
	group *Group,
	subscription *UserSubscription,
	estimate float64,
	renew bool,
) (*InflightReservation, error) {
	if s == nil || user == nil {
		return nil, nil
	}
	if s.cfg != nil && s.cfg.RunMode == config.RunModeSimple {
		return nil, nil
	}
	if !validCost(estimate) {
		return nil, nil
	}
	ttl := defaultInflightReservationTTL
	if s.cfg != nil && s.cfg.Billing.InflightReservation.TTLSeconds > 0 {
		ttl = time.Duration(s.cfg.Billing.InflightReservation.TTLSeconds) * time.Second
	}
	requestID := uuid.NewString()

	// ── 1. key 级 USD 配额：独立于余额在途开关，只有 key 显式设了 quota 才生效 ──
	var quotaCache APIKeyQuotaReservationCache
	if apiKey != nil && apiKey.ID > 0 && apiKey.Quota > 0 {
		rc, err := s.reserveAPIKeyQuota(ctx, apiKey, estimate, requestID, ttl)
		if err != nil {
			return nil, err
		}
		quotaCache = rc
	}

	// ── 2. 余额在途预留：沿用原来的开关、上限与 fail-open/fail-closed 语义 ──
	balanceCache, balanceAmount, balanceErr := s.reserveBalanceInflight(ctx, user, group, subscription, estimate, requestID, ttl)
	if balanceErr != nil {
		// 余额预占失败要把已经登记的 key 配额释放掉，避免泄漏到 TTL 过期。
		if quotaCache != nil {
			releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), inflightReservationReleaseTimeout)
			if relErr := quotaCache.ReleaseAPIKeyQuota(releaseCtx, apiKey.ID, requestID); relErr != nil {
				logger.LegacyPrintf("service.billing_cache",
					"Warning: release api key quota after failed balance reservation failed for key %d: %v", apiKey.ID, relErr)
			}
			cancel()
		}
		return nil, balanceErr
	}
	if balanceCache == nil && quotaCache == nil {
		return nil, nil
	}

	r := &InflightReservation{
		cache:      balanceCache,
		userID:     user.ID,
		requestID:  requestID,
		amount:     estimate,
		ttl:        ttl,
		quotaCache: quotaCache,
	}
	r.refs.Store(1)
	if balanceAmount > 0 {
		r.amount = balanceAmount
	}
	if apiKey != nil {
		r.apiKeyID = apiKey.ID
	}
	if renew {
		if balanceCache != nil {
			if renewer, ok := s.cache.(InflightBalanceReservationRenewer); ok && renewer != nil {
				r.renewers = append(r.renewers, func(ctx context.Context) (bool, error) {
					return renewer.RenewInflightBalance(ctx, user.ID, requestID, ttl)
				})
			}
		}
		if quotaCache != nil {
			keyID := apiKey.ID
			r.renewers = append(r.renewers, func(ctx context.Context) (bool, error) {
				return quotaCache.RenewAPIKeyQuota(ctx, keyID, requestID, ttl)
			})
		}
		r.startRenewal()
	}
	return r, nil
}

// reserveBalanceInflight 保留原 reserveInflight 的全部行为（开关、订阅豁免、MaxReservationUSD、失败策略）。
// 返回登记成功的缓存实现（nil 表示未登记）。
func (s *BillingCacheService) reserveBalanceInflight(
	ctx context.Context,
	user *User,
	group *Group,
	subscription *UserSubscription,
	estimate float64,
	requestID string,
	ttl time.Duration,
) (InflightBalanceReservationCache, float64, error) {
	cfg, ok := s.inflightReservationConfig()
	if !ok || user == nil {
		return nil, 0, nil
	}
	if group != nil && group.IsSubscriptionType() && subscription != nil {
		return nil, 0, nil
	}
	amount := estimate
	if cfg.MaxReservationUSD > 0 && amount > cfg.MaxReservationUSD {
		amount = cfg.MaxReservationUSD
	}
	if !validCost(amount) {
		return nil, 0, nil
	}
	rc, ok := s.cache.(InflightBalanceReservationCache)
	if !ok || rc == nil {
		return nil, 0, nil
	}

	balance, err := s.GetUserBalance(ctx, user.ID)
	if err != nil {
		if cfg.FailClosedOnReserveError {
			logger.LegacyPrintf("service.billing_cache", "Error: inflight reservation balance read failed for user %d (fail-closed): %v", user.ID, err)
			return nil, 0, ErrInsufficientBalance
		}
		logger.LegacyPrintf("service.billing_cache", "Warning: inflight reservation balance read failed for user %d (fail-open): %v", user.ID, err)
		return nil, 0, nil
	}

	reserveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), inflightReservationReserveTimeout)
	allowed, inflight, err := rc.ReserveInflightBalance(reserveCtx, user.ID, requestID, amount, balance, ttl)
	cancel()
	if err != nil {
		if cfg.FailClosedOnReserveError {
			logger.LegacyPrintf("service.billing_cache", "Error: inflight reservation failed for user %d (fail-closed): %v", user.ID, err)
			return nil, 0, ErrInsufficientBalance
		}
		logger.LegacyPrintf("service.billing_cache", "Warning: inflight reservation failed for user %d (fail-open): %v", user.ID, err)
		return nil, 0, nil
	}
	if !allowed {
		logger.LegacyPrintf("service.billing_cache", "inflight reservation rejected: user=%d balance=%.6f inflight=%.6f estimate=%.6f", user.ID, balance, inflight, amount)
		return nil, 0, ErrInsufficientBalance
	}
	return rc, amount, nil
}

// reserveAPIKeyQuota 为 quota>0 的 key 登记在途预占。
// 返回 nil, nil 表示未预占（Redis 不可用/无法播种，fail-open）；
// 返回 ErrAPIKeyQuotaExhausted 表示 quota_used + 在途 + estimate 已超过上限。
func (s *BillingCacheService) reserveAPIKeyQuota(
	ctx context.Context,
	apiKey *APIKey,
	estimate float64,
	requestID string,
	ttl time.Duration,
) (APIKeyQuotaReservationCache, error) {
	if apiKey == nil || apiKey.ID <= 0 || apiKey.Quota <= 0 {
		return nil, nil
	}
	rc, ok := s.cache.(APIKeyQuotaReservationCache)
	if !ok || rc == nil {
		return nil, nil
	}
	for attempt := 0; attempt < 2; attempt++ {
		reserveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), apiKeyQuotaReserveTimeout)
		ready, allowed, used, inflight, err := rc.ReserveAPIKeyQuota(reserveCtx, apiKey.ID, requestID, estimate, apiKey.Quota, ttl)
		cancel()
		if err != nil {
			logger.LegacyPrintf("service.billing_cache",
				"Warning: api key quota reservation failed for key %d (fail-open, snapshot gate still applies): %v", apiKey.ID, err)
			return nil, nil
		}
		if !ready {
			if err := s.seedAPIKeyQuotaUsage(ctx, apiKey); err != nil {
				logger.LegacyPrintf("service.billing_cache",
					"Warning: api key quota usage seed failed for key %d (fail-open, snapshot gate still applies): %v", apiKey.ID, err)
				return nil, nil
			}
			continue
		}
		if !allowed {
			logger.LegacyPrintf("service.billing_cache",
				"api key quota reservation rejected: key=%d quota=%.6f used=%.6f inflight=%.6f estimate=%.6f",
				apiKey.ID, apiKey.Quota, used, inflight, estimate)
			return nil, ErrAPIKeyQuotaExhausted
		}
		return rc, nil
	}
	logger.LegacyPrintf("service.billing_cache",
		"Warning: api key quota counter still missing after seed for key %d (fail-open)", apiKey.ID)
	return nil, nil
}

// seedAPIKeyQuotaUsage 用数据库里的 quota_used 播种实时用量镜像。
// 数据库读取失败时退回鉴权快照里的 quota_used（可能滞后，但不会低估已用量）。
func (s *BillingCacheService) seedAPIKeyQuotaUsage(ctx context.Context, apiKey *APIKey) error {
	if apiKey == nil {
		return nil
	}
	rc, ok := s.cache.(APIKeyQuotaReservationCache)
	if !ok || rc == nil {
		return nil
	}
	quotaUsed := math.Max(0, apiKey.QuotaUsed)
	if s.apiKeyRateLimitLoader != nil {
		data, err := s.loadAPIKeyRateLimitFromDB(ctx, apiKey.ID)
		if err != nil {
			logger.LegacyPrintf("service.billing_cache",
				"Warning: seed api key quota usage from DB failed for key %d, falling back to auth snapshot: %v", apiKey.ID, err)
		} else if data != nil {
			quotaUsed = math.Max(0, data.QuotaUsed)
		}
	}
	seedCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), apiKeyQuotaReserveTimeout)
	defer cancel()
	return rc.SeedAPIKeyQuotaUsage(seedCtx, apiKey.ID, quotaUsed, apiKeyQuotaUsageTTL)
}

// IncrementAPIKeyQuotaUsage 结算后把实际扣费累加到 key 的实时用量镜像。
// 由计费 worker（不是请求热路径）同步调用，保证下一次准入立即看到已消费金额；
// 失败只记日志：数据库在同一个事务里已经加过这笔费用，镜像过期后会重新播种。
func (s *BillingCacheService) IncrementAPIKeyQuotaUsage(apiKeyID int64, cost float64) {
	if s == nil || apiKeyID <= 0 || cost <= 0 || math.IsNaN(cost) || math.IsInf(cost, 0) {
		return
	}
	rc, ok := s.cache.(APIKeyQuotaReservationCache)
	if !ok || rc == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), cacheWriteTimeout)
	defer cancel()
	if err := rc.IncrementAPIKeyQuotaUsage(ctx, apiKeyID, cost); err != nil {
		logger.LegacyPrintf("service.billing_cache",
			"Warning: increment api key quota usage cache failed for key %d: %v", apiKeyID, err)
	}
}

// InvalidateAPIKeyQuotaUsage 丢弃 key 的实时用量镜像（管理员重置/调整配额后调用）。
func (s *BillingCacheService) InvalidateAPIKeyQuotaUsage(ctx context.Context, keyID int64) error {
	if s == nil || keyID <= 0 {
		return nil
	}
	rc, ok := s.cache.(APIKeyQuotaReservationCache)
	if !ok || rc == nil {
		return nil
	}
	if err := rc.InvalidateAPIKeyQuotaUsage(ctx, keyID); err != nil {
		logger.LegacyPrintf("service.billing_cache",
			"Warning: invalidate api key quota usage cache failed for key %d: %v", keyID, err)
		return err
	}
	return nil
}
