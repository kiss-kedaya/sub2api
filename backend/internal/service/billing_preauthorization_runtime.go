package service

import (
	"context"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

// 两套预扣费系统的运行时开关。
//
// 系统里有两条互不相干的「提前占钱」机制，它们在不同层面拦截请求：
//
//	1) 余额预扣 hold（billing.balance_preauthorization_enabled）
//	   在入口按「预测花费」冻结一份余额（Redis live balance + 持久 hold），
//	   请求结束按实际用量结算，多退少补。作用范围覆盖所有余额模式计费请求。
//
//	2) 在途余额预留（billing.inflight_reservation.enabled）
//	   只在准入时原子校验「缓存余额 - 在途预留合计 >= 本请求估算」后登记一条
//	   带 TTL 的预留，请求结束释放。它不改余额，只防并发请求集体透支。
//
// 两套可以叠加，但估算口径不同（hold 用预测花费，预留用 max_tokens 上限），
// 同时打开会把同一份可用余额算两遍，所以默认都关，由管理员按需选择。
//
// 取值优先级：设置页（settings 表）> 部署配置（yaml/env）。
// 设置键缺失或为空串时回退部署配置，升级后存量环境行为完全不变。

// BillingPreauthorizationRuntimeSettings 是两套预扣费系统的当前生效状态。
type BillingPreauthorizationRuntimeSettings struct {
	// BalanceHold 余额预扣 hold 是否开启。
	BalanceHold bool
	// InflightReservation 在途余额预留是否开启。
	InflightReservation bool
}

// billingPreauthorizationRuntimeCacheTTL 运行时开关的进程内缓存时长。
// 管理员改完设置最多 5 秒后全量生效，且热路径不会每请求打库。
const billingPreauthorizationRuntimeCacheTTL = 5 * time.Second

type cachedBillingPreauthorizationRuntime struct {
	settings  BillingPreauthorizationRuntimeSettings
	expiresAt time.Time
}

// billingPreauthorizationRuntime 读取两套预扣费开关（带短 TTL 缓存）。
//
// 任何读取失败或键缺失都回退部署配置，因此不会因为设置表不可用而改变
// 存量环境的行为。RunModeSimple 下恒为关闭。
func (s *SettingService) billingPreauthorizationRuntime(ctx context.Context) BillingPreauthorizationRuntimeSettings {
	fallback := BillingPreauthorizationRuntimeSettings{}
	if s != nil && s.cfg != nil {
		if s.cfg.RunMode == config.RunModeSimple {
			return fallback
		}
		fallback.BalanceHold = s.cfg.Billing.BalancePreauthorizationEnabled
		fallback.InflightReservation = s.cfg.Billing.InflightReservation.Enabled
	}
	if s == nil || s.settingRepo == nil {
		return fallback
	}
	if ctx == nil {
		ctx = context.Background()
	}
	now := time.Now()
	if cached, _ := s.billingPreauthRuntimeCache.Load().(*cachedBillingPreauthorizationRuntime); cached != nil {
		if cached.expiresAt.After(now) {
			return cached.settings
		}
	}
	values, err := s.settingRepo.GetMultiple(ctx, []string{
		SettingKeyBillingBalancePreauthorizationEnabled,
		SettingKeyBillingInflightReservationEnabled,
	})
	if err != nil {
		// 读失败：短缓存 fallback，避免失败放大成每请求一次查询。
		s.storeBillingPreauthorizationRuntime(fallback, now)
		return fallback
	}
	out := fallback
	if v, ok := values[SettingKeyBillingBalancePreauthorizationEnabled]; ok {
		if parsed, valid := parseBoolSetting(v); valid {
			out.BalanceHold = parsed
		}
	}
	if v, ok := values[SettingKeyBillingInflightReservationEnabled]; ok {
		if parsed, valid := parseBoolSetting(v); valid {
			out.InflightReservation = parsed
		}
	}
	s.storeBillingPreauthorizationRuntime(out, now)
	return out
}

func (s *SettingService) storeBillingPreauthorizationRuntime(
	settings BillingPreauthorizationRuntimeSettings,
	now time.Time,
) {
	if s == nil {
		return
	}
	s.billingPreauthRuntimeCache.Store(&cachedBillingPreauthorizationRuntime{
		settings:  settings,
		expiresAt: now.Add(billingPreauthorizationRuntimeCacheTTL),
	})
}

// parseBoolSetting 只接受明确的 true/false；其他值（含空串）视为未配置。
func parseBoolSetting(raw string) (bool, bool) {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "true":
		return true, true
	case "false":
		return false, true
	default:
		return false, false
	}
}

// BillingPreauthorizationRuntime 返回两套预扣费系统的当前生效状态。
//
// 热路径调用点：入口资格校验与在途预留登记。SettingService 未注入时回退部署配置。
func (s *BillingCacheService) BillingPreauthorizationRuntime(ctx context.Context) BillingPreauthorizationRuntimeSettings {
	if s == nil || s.cfg == nil {
		return BillingPreauthorizationRuntimeSettings{}
	}
	if s.cfg.RunMode == config.RunModeSimple {
		return BillingPreauthorizationRuntimeSettings{}
	}
	if s.settingService != nil {
		return s.settingService.billingPreauthorizationRuntime(ctx)
	}
	return BillingPreauthorizationRuntimeSettings{
		BalanceHold:         s.cfg.Billing.BalancePreauthorizationEnabled,
		InflightReservation: s.cfg.Billing.InflightReservation.Enabled,
	}
}

// BalanceHoldEnabled 报告余额预扣 hold 是否开启（读设置页，回退部署配置）。
func (s *BillingCacheService) BalanceHoldEnabled(ctx context.Context) bool {
	return s.BillingPreauthorizationRuntime(ctx).BalanceHold
}

// SetSettingServiceForBilling 注入设置服务，供运行时开关读取设置页。
func (s *BillingCacheService) SetSettingServiceForBilling(svc *SettingService) {
	if s == nil {
		return
	}
	s.settingService = svc
}
