package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// AdminAuthProbeSettings 管理端 401/403 探测告警配置。
//
// 计数本身走 Redis（按来源 IP 维度），这里只控制开关和阈值：
//   - Enabled：默认开启。关闭后中间件仍然不做任何 Redis 写入。
//   - Threshold：窗口内同一 IP 的 401/403 总数达到该值（并且不同路径数也达到
//     DistinctPaths）才告警，默认 30。
//   - DistinctPaths：窗口内同一 IP 命中的不同 admin 路由数达到该值（并且总失败
//     数也达到 Threshold）才告警，默认 10。两个条件是 AND，避免管理员 token
//     过期后一次页面加载或少量路径探测就误报。
//   - WindowMinutes / CooldownMinutes：计数窗口与告警去重窗口，默认 5 分钟。
//
// 阈值故意做成设置项而不是常量：正常管理员敲错路径/会话过期也会产生 401/403，
// 阈值要能跟着线上误报率调整，不需要重新发版。
type AdminAuthProbeSettings struct {
	Enabled         bool `json:"enabled"`
	Threshold       int  `json:"threshold"`
	DistinctPaths   int  `json:"distinct_paths"`
	WindowMinutes   int  `json:"window_minutes"`
	CooldownMinutes int  `json:"cooldown_minutes"`
}

const (
	// adminAuthProbeThresholdMax 防止配置异常大的值让告警永远不触发。
	adminAuthProbeThresholdMax     = 100000
	adminAuthProbeDistinctPathsMax = 10000
	adminAuthProbeWindowMaxMinutes = 24 * 60

	adminAuthProbeSettingsCacheTTL = 60 * time.Second
	adminAuthProbeSettingsDBTMO    = 2 * time.Second
)

// cachedAdminAuthProbeSettings 进程内缓存条目（60s TTL），鉴权失败路径禁止每请求读 DB。
type cachedAdminAuthProbeSettings struct {
	settings  AdminAuthProbeSettings
	expiresAt int64 // unix nano
}

// DefaultAdminAuthProbeSettings 返回默认配置：开启，5 分钟内 401/403 总数 ≥30
// 且不同路径 ≥10 才告警。
func DefaultAdminAuthProbeSettings() *AdminAuthProbeSettings {
	return &AdminAuthProbeSettings{
		Enabled:         true,
		Threshold:       30,
		DistinctPaths:   10,
		WindowMinutes:   5,
		CooldownMinutes: 5,
	}
}

func normalizeAdminAuthProbeSettings(s *AdminAuthProbeSettings) {
	if s == nil {
		return
	}
	if s.Threshold <= 0 {
		s.Threshold = DefaultAdminAuthProbeSettings().Threshold
	}
	if s.Threshold > adminAuthProbeThresholdMax {
		s.Threshold = adminAuthProbeThresholdMax
	}
	if s.DistinctPaths <= 0 {
		s.DistinctPaths = DefaultAdminAuthProbeSettings().DistinctPaths
	}
	if s.DistinctPaths > adminAuthProbeDistinctPathsMax {
		s.DistinctPaths = adminAuthProbeDistinctPathsMax
	}
	if s.WindowMinutes <= 0 {
		s.WindowMinutes = DefaultAdminAuthProbeSettings().WindowMinutes
	}
	if s.WindowMinutes > adminAuthProbeWindowMaxMinutes {
		s.WindowMinutes = adminAuthProbeWindowMaxMinutes
	}
	if s.CooldownMinutes <= 0 {
		s.CooldownMinutes = DefaultAdminAuthProbeSettings().CooldownMinutes
	}
	if s.CooldownMinutes > adminAuthProbeWindowMaxMinutes {
		s.CooldownMinutes = adminAuthProbeWindowMaxMinutes
	}
}

// GetAdminAuthProbeSettings 直读 DB（管理端读写路径使用）。缺失/解析失败回退默认值。
func (s *SettingService) GetAdminAuthProbeSettings(ctx context.Context) (*AdminAuthProbeSettings, error) {
	if s == nil || s.settingRepo == nil {
		return DefaultAdminAuthProbeSettings(), nil
	}
	value, err := s.settingRepo.GetValue(ctx, SettingKeyAdminAuthProbeSettings)
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			return DefaultAdminAuthProbeSettings(), nil
		}
		return nil, fmt.Errorf("get admin auth probe settings: %w", err)
	}
	if strings.TrimSpace(value) == "" {
		return DefaultAdminAuthProbeSettings(), nil
	}

	settings := &AdminAuthProbeSettings{}
	if err := json.Unmarshal([]byte(value), settings); err != nil {
		slog.Warn("failed to unmarshal admin auth probe settings, falling back to defaults",
			"error", err, "key", SettingKeyAdminAuthProbeSettings)
		return DefaultAdminAuthProbeSettings(), nil
	}
	normalizeAdminAuthProbeSettings(settings)
	return settings, nil
}

// SetAdminAuthProbeSettings 保存配置并立即刷新进程内缓存。
func (s *SettingService) SetAdminAuthProbeSettings(ctx context.Context, settings *AdminAuthProbeSettings) error {
	if s == nil || s.settingRepo == nil {
		return fmt.Errorf("setting service not available")
	}
	if settings == nil {
		return fmt.Errorf("settings cannot be nil")
	}
	if settings.Threshold < 0 || settings.DistinctPaths < 0 || settings.WindowMinutes < 0 || settings.CooldownMinutes < 0 {
		return fmt.Errorf("admin auth probe settings cannot be negative")
	}
	normalized := *settings
	normalizeAdminAuthProbeSettings(&normalized)
	data, err := json.Marshal(normalized)
	if err != nil {
		return fmt.Errorf("marshal admin auth probe settings: %w", err)
	}
	if err := s.settingRepo.Set(ctx, SettingKeyAdminAuthProbeSettings, string(data)); err != nil {
		return fmt.Errorf("save admin auth probe settings: %w", err)
	}
	s.storeAdminAuthProbeSettingsCache(normalized)
	return nil
}

func (s *SettingService) storeAdminAuthProbeSettingsCache(settings AdminAuthProbeSettings) {
	if s == nil {
		return
	}
	s.adminAuthProbeCache.Store(&cachedAdminAuthProbeSettings{
		settings:  settings,
		expiresAt: time.Now().Add(adminAuthProbeSettingsCacheTTL).UnixNano(),
	})
}

// GetAdminAuthProbeSettingsCached 返回进程内缓存的配置，供 401/403 计数路径使用。
// 缓存过期时同步读一次 DB（singleflight 合并并发），DB 失败回退默认值，绝不因为
// 配置读取失败而阻断鉴权响应，也绝不让失败路径每请求打库。
func (s *SettingService) GetAdminAuthProbeSettingsCached(ctx context.Context) AdminAuthProbeSettings {
	defaults := *DefaultAdminAuthProbeSettings()
	if s == nil || s.settingRepo == nil {
		return defaults
	}
	if cached, ok := s.adminAuthProbeCache.Load().(*cachedAdminAuthProbeSettings); ok && cached != nil {
		if time.Now().UnixNano() < cached.expiresAt {
			return cached.settings
		}
	}
	result, _, _ := s.adminAuthProbeSF.Do("admin_auth_probe_settings", func() (any, error) {
		if cached, ok := s.adminAuthProbeCache.Load().(*cachedAdminAuthProbeSettings); ok && cached != nil {
			if time.Now().UnixNano() < cached.expiresAt {
				return cached.settings, nil
			}
		}
		loadCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), adminAuthProbeSettingsDBTMO)
		defer cancel()
		settings, err := s.GetAdminAuthProbeSettings(loadCtx)
		if err != nil || settings == nil {
			// 读失败回退默认值，同时短缓存，避免故障期间每个失败请求都打库。
			settings = DefaultAdminAuthProbeSettings()
			s.storeAdminAuthProbeSettingsCache(*settings)
			return *settings, nil
		}
		s.storeAdminAuthProbeSettingsCache(*settings)
		return *settings, nil
	})
	if settings, ok := result.(AdminAuthProbeSettings); ok {
		return settings
	}
	return defaults
}
