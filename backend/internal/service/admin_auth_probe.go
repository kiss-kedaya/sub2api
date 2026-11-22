package service

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// 管理端 401/403 探测告警。
//
// 背景：授权渗透测试中大量探测 /api/v1/admin/* 只会得到 401/403，过去完全静默。
// 这里在 admin 鉴权失败路径上做一层**轻量**计数，命中阈值后复用 ops_alert 的
// 规则/事件表和审计日志写一条事件，不新造告警体系。
//
// 约束：
//   - 计数只写 Redis，窗口内每 IP 一个 hash + 一个 path set，TTL 自动过期；
//   - 计数/告警失败绝不影响鉴权响应（fail-open，仅进程日志告警）；
//   - 事件里只记录 IP、UA 前 64 字符、命中路径、状态码统计，绝不记录任何
//     token/header 值；
//   - 告警写入放在有界 goroutine 池里，避免攻击者用大量 IP 把告警写库拖垮。
const (
	adminAuthProbeRedisPrefix = "ops:admin_auth_probe:"
	adminAuthProbeAlertRule   = "admin_auth_probe_detected"
	adminAuthProbeAlertSem    = 8

	adminAuthProbeCountTimeout   = 500 * time.Millisecond
	adminAuthProbeAlertTimeout   = 5 * time.Second
	adminAuthProbeAlertPathLimit = 20
	adminAuthProbeUAMaxRunes     = 64
)

// AdminAuthProbeInput 一次被观察到的鉴权失败。只允许放非敏感字段：
// 调用方必须传原始 path（不含 query）与 UA，禁止传任何 header/token。
type AdminAuthProbeInput struct {
	ClientIP   string
	Method     string
	Path       string
	StatusCode int
	UserAgent  string
}

// AdminAuthProbeSettingsSource 只读设置，便于测试注入。
type AdminAuthProbeSettingsSource interface {
	GetAdminAuthProbeSettingsCached(ctx context.Context) AdminAuthProbeSettings
}

// AdminAuthProbeAlertWriter 复用现有 ops_alert 规则/事件写入路径。
type AdminAuthProbeAlertWriter interface {
	ListAlertRules(ctx context.Context) ([]*OpsAlertRule, error)
	CreateAlertRule(ctx context.Context, rule *OpsAlertRule) (*OpsAlertRule, error)
	CreateAlertEvent(ctx context.Context, event *OpsAlertEvent) (*OpsAlertEvent, error)
}

// AdminAuthProbeAuditRecorder 复用现有审计日志异步队列。
type AdminAuthProbeAuditRecorder interface {
	Record(entry *AuditLog)
}

type adminAuthProbeAlert struct {
	clientIP   string
	method     string
	path       string
	statusCode int
	userAgent  string
	total      int64
	pathCount  int64
	status401  int64
	status403  int64
	paths      []string
	window     time.Duration
	threshold  int
	cooldown   time.Duration
	detectedAt time.Time
}

// AdminAuthProbeMonitor 轻量计数 + 阈值告警。
type AdminAuthProbeMonitor struct {
	redis    *redis.Client
	settings AdminAuthProbeSettingsSource
	alerts   AdminAuthProbeAlertWriter
	audit    AdminAuthProbeAuditRecorder

	alertSem chan struct{}
}

func NewAdminAuthProbeMonitor(
	redisClient *redis.Client,
	settings AdminAuthProbeSettingsSource,
	alerts AdminAuthProbeAlertWriter,
	audit AdminAuthProbeAuditRecorder,
) *AdminAuthProbeMonitor {
	return &AdminAuthProbeMonitor{
		redis:    redisClient,
		settings: settings,
		alerts:   alerts,
		audit:    audit,
		alertSem: make(chan struct{}, adminAuthProbeAlertSem),
	}
}

func (m *AdminAuthProbeMonitor) enabled() bool {
	return m != nil && m.redis != nil && m.settings != nil
}

// RecordAdminAuthFailure 记录一次 401/403，返回本次是否写入了告警事件。
// 该函数不返回错误：鉴权失败路径不能因为观测组件故障而改变响应。
func (m *AdminAuthProbeMonitor) RecordAdminAuthFailure(ctx context.Context, input AdminAuthProbeInput) bool {
	if !m.enabled() {
		return false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	settings := m.settings.GetAdminAuthProbeSettingsCached(ctx)
	if !settings.Enabled {
		return false
	}
	clientIP, ok := normalizeAdminProbeIP(input.ClientIP)
	if !ok {
		return false
	}
	statusCode := input.StatusCode
	if statusCode != 401 && statusCode != 403 {
		return false
	}
	path := normalizeAdminProbePath(input.Path)
	if path == "" {
		path = "/"
	}

	window := time.Duration(settings.WindowMinutes) * time.Minute
	if window <= 0 {
		window = 5 * time.Minute
	}
	cooldown := time.Duration(settings.CooldownMinutes) * time.Minute
	if cooldown <= 0 {
		cooldown = window
	}

	countCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), adminAuthProbeCountTimeout)
	defer cancel()

	windowKey := adminAuthProbeRedisPrefix + "win:" + clientIP
	pathsKey := adminAuthProbeRedisPrefix + "paths:" + clientIP

	pipe := m.redis.Pipeline()
	totalCmd := pipe.HIncrBy(countCtx, windowKey, "total", 1)
	statusCmd := pipe.HIncrBy(countCtx, windowKey, "s"+strconv.Itoa(statusCode), 1)
	pipe.Expire(countCtx, windowKey, window)
	pipe.SAdd(countCtx, pathsKey, path)
	pipe.Expire(countCtx, pathsKey, window)
	pathCountCmd := pipe.SCard(countCtx, pathsKey)
	if _, err := pipe.Exec(countCtx); err != nil {
		slog.Warn("admin_auth_probe_redis_failed", "error", err)
		return false
	}

	total := totalCmd.Val()
	pathCount := pathCountCmd.Val()
	status401, status403 := int64(0), int64(0)
	if statusCode == 401 {
		status401 = statusCmd.Val()
	} else {
		status403 = statusCmd.Val()
	}
	// 触发条件是「AND」：窗口内 401/403 总数达到阈值 **且** 命中不同路径数达到
	// 阈值。只看总量会被管理员 token 过期后一次页面加载（同时打多个 admin 接口）
	// 误触发；只看路径数则「扫 5 个路径」就报警，同样噪声太大。两个维度同时满足
	// 才像真实的批量探测。
	if total < int64(settings.Threshold) || pathCount < int64(settings.DistinctPaths) {
		return false
	}

	// 同一 IP 一个冷却窗口只告警一次，避免攻击流量打出一条事件洪水。
	alertKey := adminAuthProbeRedisPrefix + "alert:" + clientIP
	claimed, err := m.redis.SetNX(countCtx, alertKey, "1", cooldown).Result()
	if err != nil {
		slog.Warn("admin_auth_probe_dedupe_failed", "error", err)
		return false
	}
	if !claimed {
		return false
	}

	// 补齐状态码统计与命中路径样本（仍然只读 Redis，超时即放弃明细）。
	statsCtx, statsCancel := context.WithTimeout(context.WithoutCancel(ctx), adminAuthProbeCountTimeout)
	defer statsCancel()
	if values, err := m.redis.HGetAll(statsCtx, windowKey).Result(); err == nil {
		if parsed, err := strconv.ParseInt(values["s401"], 10, 64); err == nil {
			status401 = parsed
		}
		if parsed, err := strconv.ParseInt(values["s403"], 10, 64); err == nil {
			status403 = parsed
		}
	}
	paths := []string{}
	if members, err := m.redis.SMembers(statsCtx, pathsKey).Result(); err == nil {
		if len(members) > adminAuthProbeAlertPathLimit {
			members = members[:adminAuthProbeAlertPathLimit]
		}
		paths = members
	}

	payload := &adminAuthProbeAlert{
		clientIP:   clientIP,
		method:     strings.ToUpper(strings.TrimSpace(input.Method)),
		path:       path,
		statusCode: statusCode,
		userAgent:  truncateRunes(strings.TrimSpace(input.UserAgent), adminAuthProbeUAMaxRunes),
		total:      total,
		pathCount:  pathCount,
		status401:  status401,
		status403:  status403,
		paths:      paths,
		window:     window,
		threshold:  settings.Threshold,
		cooldown:   cooldown,
		detectedAt: time.Now().UTC(),
	}

	m.recordAudit(payload)
	slog.Warn("admin_auth_probe_detected",
		"client_ip", payload.clientIP,
		"failure_count", payload.total,
		"distinct_paths", payload.pathCount,
		"status_401", payload.status401,
		"status_403", payload.status403,
		"window", payload.window.String(),
	)
	m.enqueueAlert(payload)
	return true
}

func (m *AdminAuthProbeMonitor) enqueueAlert(payload *adminAuthProbeAlert) {
	if m.alerts == nil {
		return
	}
	select {
	case m.alertSem <- struct{}{}:
		go func() {
			defer func() { <-m.alertSem }()
			ctx, cancel := context.WithTimeout(context.Background(), adminAuthProbeAlertTimeout)
			defer cancel()
			m.writeAlertEvent(ctx, payload)
		}()
	default:
		// 告警写库已经饱和：审计与进程日志仍然保底，丢的是 ops 事件。
		slog.Warn("admin_auth_probe_alert_dropped", "client_ip", payload.clientIP, "reason", "saturated")
	}
}

func (m *AdminAuthProbeMonitor) recordAudit(payload *adminAuthProbeAlert) {
	if m.audit == nil {
		return
	}
	extra := map[string]any{
		"window_minutes":    int(payload.window.Minutes()),
		"failure_count":     payload.total,
		"distinct_paths":    payload.pathCount,
		"status_401":        payload.status401,
		"status_403":        payload.status403,
		"threshold":         payload.threshold,
		"paths":             payload.paths,
		"user_agent_prefix": payload.userAgent,
	}
	m.audit.Record(&AuditLog{
		CreatedAt:  payload.detectedAt,
		Action:     AuditActionAdminAuthProbeAlert,
		Method:     payload.method,
		Path:       payload.path,
		ClientIP:   payload.clientIP,
		UserAgent:  payload.userAgent,
		StatusCode: payload.statusCode,
		Extra:      extra,
	})
}

func (m *AdminAuthProbeMonitor) writeAlertEvent(ctx context.Context, payload *adminAuthProbeAlert) {
	ruleID, err := m.ensureAlertRule(ctx, payload)
	if err != nil {
		slog.Warn("admin_auth_probe_rule_failed", "error", err)
		return
	}
	metricValue := float64(payload.total)
	thresholdValue := float64(payload.threshold)
	dimensions := map[string]any{
		"client_ip":         payload.clientIP,
		"failure_count":     payload.total,
		"distinct_paths":    payload.pathCount,
		"status_401":        payload.status401,
		"status_403":        payload.status403,
		"window_minutes":    int(payload.window.Minutes()),
		"threshold":         payload.threshold,
		"paths":             payload.paths,
		"user_agent_prefix": payload.userAgent,
	}
	event := &OpsAlertEvent{
		RuleID:         ruleID,
		Severity:       "P2",
		Status:         OpsAlertStatusFiring,
		Title:          "Admin 401/403 probe detected",
		Description:    adminAuthProbeAlertDescription(payload),
		MetricValue:    &metricValue,
		ThresholdValue: &thresholdValue,
		Dimensions:     dimensions,
		FiredAt:        payload.detectedAt,
	}
	if _, err := m.alerts.CreateAlertEvent(ctx, event); err != nil {
		slog.Warn("admin_auth_probe_event_failed", "error", err, "client_ip", payload.clientIP)
	}
}

func adminAuthProbeAlertDescription(payload *adminAuthProbeAlert) string {
	if payload == nil {
		return ""
	}
	return fmt.Sprintf(
		"admin 401/403 probe from %s: %d failures across %d paths in %d minutes (401=%d, 403=%d, threshold=%d)",
		payload.clientIP,
		payload.total,
		payload.pathCount,
		int(payload.window.Minutes()),
		payload.status401,
		payload.status403,
		payload.threshold,
	)
}

func (m *AdminAuthProbeMonitor) ensureAlertRule(ctx context.Context, payload *adminAuthProbeAlert) (int64, error) {
	rules, err := m.alerts.ListAlertRules(ctx)
	if err != nil {
		return 0, err
	}
	for _, rule := range rules {
		if rule != nil && rule.Name == adminAuthProbeAlertRule {
			return rule.ID, nil
		}
	}
	created, err := m.alerts.CreateAlertRule(ctx, &OpsAlertRule{
		Name:             adminAuthProbeAlertRule,
		Description:      "Detects repeated 401/403 responses on admin routes from one source IP (written by admin auth probe monitor)",
		Enabled:          true,
		Severity:         "P2",
		MetricType:       "admin_auth_probe_failures",
		Operator:         ">=",
		Threshold:        float64(payload.threshold),
		WindowMinutes:    int(payload.window.Minutes()),
		SustainedMinutes: 0,
		CooldownMinutes:  int(payload.cooldown.Minutes()),
		NotifyEmail:      false,
		Filters:          map[string]any{"source": "admin_auth_probe"},
	})
	if err == nil && created != nil {
		return created.ID, nil
	}
	// 多节点并发创建时唯一索引会报冲突：重新读一次即可。
	if rules, listErr := m.alerts.ListAlertRules(ctx); listErr == nil {
		for _, rule := range rules {
			if rule != nil && rule.Name == adminAuthProbeAlertRule {
				return rule.ID, nil
			}
		}
	}
	return 0, err
}

func normalizeAdminProbeIP(raw string) (string, bool) {
	addr, err := netip.ParseAddr(strings.TrimSpace(raw))
	if err != nil {
		return "", false
	}
	addr = addr.Unmap()
	if addr.Is6() {
		// 与入口登记保持一致：IPv6 按 /64 聚合，避免同一来源随机后缀刷爆 key。
		return netip.PrefixFrom(addr, 64).Masked().Addr().String(), true
	}
	return addr.String(), true
}

func normalizeAdminProbePath(raw string) string {
	path := strings.TrimSpace(raw)
	if path == "" {
		return ""
	}
	if idx := strings.IndexByte(path, '?'); idx >= 0 {
		path = path[:idx]
	}
	if idx := strings.IndexByte(path, '#'); idx >= 0 {
		path = path[:idx]
	}
	if len(path) > 512 {
		path = path[:512]
	}
	return path
}

// truncateRunes 按字符（而非字节）截断，避免把多字节 UTF-8 截成乱码。
func truncateRunes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}
