package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"math"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/disk"
)

type OpsMetrics struct {
	WindowMinutes         int       `json:"window_minutes"`
	RequestCount          int64     `json:"request_count"`
	SuccessCount          int64     `json:"success_count"`
	ErrorCount            int64     `json:"error_count"`
	SuccessRate           float64   `json:"success_rate"`
	ErrorRate             float64   `json:"error_rate"`
	P95LatencyMs          int       `json:"p95_latency_ms"`
	P99LatencyMs          int       `json:"p99_latency_ms"`
	HTTP2Errors           int       `json:"http2_errors"`
	ActiveAlerts          int       `json:"active_alerts"`
	CPUUsagePercent       float64   `json:"cpu_usage_percent"`
	MemoryUsedMB          int64     `json:"memory_used_mb"`
	MemoryTotalMB         int64     `json:"memory_total_mb"`
	MemoryUsagePercent    float64   `json:"memory_usage_percent"`
	HeapAllocMB           int64     `json:"heap_alloc_mb"`
	GCPauseMs             float64   `json:"gc_pause_ms"`
	ConcurrencyQueueDepth int       `json:"concurrency_queue_depth"`
	UpdatedAt             time.Time `json:"updated_at,omitempty"`
}

type OpsErrorLog struct {
	ID         int64     `json:"id"`
	CreatedAt  time.Time `json:"created_at"`
	Phase      string    `json:"phase"`
	Type       string    `json:"type"`
	Severity   string    `json:"severity"`
	StatusCode int       `json:"status_code"`
	Platform   string    `json:"platform"`
	Model      string    `json:"model"`
	LatencyMs  *int      `json:"latency_ms"`
	RequestID  string    `json:"request_id"`
	Message    string    `json:"message"`

	UserID      *int64 `json:"user_id,omitempty"`
	APIKeyID    *int64 `json:"api_key_id,omitempty"`
	AccountID   *int64 `json:"account_id,omitempty"`
	GroupID     *int64 `json:"group_id,omitempty"`
	ClientIP    string `json:"client_ip,omitempty"`
	RequestPath string `json:"request_path,omitempty"`
	Stream      bool   `json:"stream"`
}

type OpsErrorLogFilters struct {
	StartTime *time.Time
	EndTime   *time.Time
	Platform  string
	Phase     string
	Severity  string
	Query     string
	Limit     int
}

type OpsWindowStats struct {
	SuccessCount int64
	ErrorCount   int64
	P95LatencyMs int
	P99LatencyMs int
	HTTP2Errors  int
}

type ProviderStats struct {
	Platform string

	RequestCount int64
	SuccessCount int64
	ErrorCount   int64

	AvgLatencyMs int
	P99LatencyMs int

	Error4xxCount int64
	Error5xxCount int64
	TimeoutCount  int64
}

type ProviderHealthErrorsByType struct {
	HTTP4xx int64 `json:"4xx"`
	HTTP5xx int64 `json:"5xx"`
	Timeout int64 `json:"timeout"`
}

type ProviderHealthData struct {
	Name         string                     `json:"name"`
	RequestCount int64                      `json:"request_count"`
	SuccessRate  float64                    `json:"success_rate"`
	ErrorRate    float64                    `json:"error_rate"`
	LatencyAvg   int                        `json:"latency_avg"`
	LatencyP99   int                        `json:"latency_p99"`
	Status       string                     `json:"status"`
	ErrorsByType ProviderHealthErrorsByType `json:"errors_by_type"`
}

type LatencyHistogramItem struct {
	Range      string  `json:"range"`
	Count      int64   `json:"count"`
	Percentage float64 `json:"percentage"`
}

type ErrorDistributionItem struct {
	Code       string  `json:"code"`
	Message    string  `json:"message"`
	Count      int64   `json:"count"`
	Percentage float64 `json:"percentage"`
}

type OpsRepository interface {
	CreateErrorLog(ctx context.Context, log *OpsErrorLog) error
	// ListErrorLogsLegacy keeps the original non-paginated query API used by the
	// existing /api/v1/admin/ops/error-logs endpoint (limit is capped at 500; for
	// stable pagination use /api/v1/admin/ops/errors).
	ListErrorLogsLegacy(ctx context.Context, filters OpsErrorLogFilters) ([]OpsErrorLog, error)

	// ListErrorLogs provides a paginated error-log query API (with total count).
	ListErrorLogs(ctx context.Context, filter *ErrorLogFilter) ([]*ErrorLog, int64, error)
	GetLatestSystemMetric(ctx context.Context) (*OpsMetrics, error)
	CreateSystemMetric(ctx context.Context, metric *OpsMetrics) error
	GetWindowStats(ctx context.Context, startTime, endTime time.Time) (*OpsWindowStats, error)
	GetProviderStats(ctx context.Context, startTime, endTime time.Time) ([]*ProviderStats, error)
	GetLatencyHistogram(ctx context.Context, startTime, endTime time.Time) ([]*LatencyHistogramItem, error)
	GetErrorDistribution(ctx context.Context, startTime, endTime time.Time) ([]*ErrorDistributionItem, error)
	ListRecentSystemMetrics(ctx context.Context, windowMinutes, limit int) ([]OpsMetrics, error)
	ListSystemMetricsRange(ctx context.Context, windowMinutes int, startTime, endTime time.Time, limit int) ([]OpsMetrics, error)
	ListAlertRules(ctx context.Context) ([]OpsAlertRule, error)
	GetActiveAlertEvent(ctx context.Context, ruleID int64) (*OpsAlertEvent, error)
	GetLatestAlertEvent(ctx context.Context, ruleID int64) (*OpsAlertEvent, error)
	CreateAlertEvent(ctx context.Context, event *OpsAlertEvent) error
	UpdateAlertEventStatus(ctx context.Context, eventID int64, status string, resolvedAt *time.Time) error
	UpdateAlertEventNotifications(ctx context.Context, eventID int64, emailSent, webhookSent bool) error
	CountActiveAlerts(ctx context.Context) (int, error)
	GetOverviewStats(ctx context.Context, startTime, endTime time.Time) (*OverviewStats, error)

	// Redis-backed cache/health (best-effort; implementation lives in repository layer).
	GetCachedLatestSystemMetric(ctx context.Context) (*OpsMetrics, error)
	SetCachedLatestSystemMetric(ctx context.Context, metric *OpsMetrics) error
	GetCachedDashboardOverview(ctx context.Context, timeRange string) (*DashboardOverviewData, error)
	SetCachedDashboardOverview(ctx context.Context, timeRange string, data *DashboardOverviewData, ttl time.Duration) error
	PingRedis(ctx context.Context) error
}

type OpsService struct {
	repo  OpsRepository
	sqlDB *sql.DB

	redisNilWarnOnce sync.Once
	dbNilWarnOnce    sync.Once
}

const opsDBQueryTimeout = 5 * time.Second

func NewOpsService(repo OpsRepository, sqlDB *sql.DB) *OpsService {
	svc := &OpsService{repo: repo, sqlDB: sqlDB}

	// Best-effort startup health checks: log warnings if Redis/DB is unavailable,
	// but never fail service startup (graceful degradation).
	log.Printf("[OpsService] Performing startup health checks...")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	redisStatus := svc.checkRedisHealth(ctx)
	dbStatus := svc.checkDatabaseHealth(ctx)

	log.Printf("[OpsService] Startup health check complete: Redis=%s, Database=%s", redisStatus, dbStatus)
	if redisStatus == "critical" || dbStatus == "critical" {
		log.Printf("[OpsService][WARN] Service starting with degraded dependencies - some features may be unavailable")
	}

	return svc
}

func (s *OpsService) RecordError(ctx context.Context, log *OpsErrorLog) error {
	if log == nil {
		return nil
	}
	if log.CreatedAt.IsZero() {
		log.CreatedAt = time.Now()
	}
	if log.Severity == "" {
		log.Severity = "P2"
	}
	if log.Phase == "" {
		log.Phase = "internal"
	}
	if log.Type == "" {
		log.Type = "unknown_error"
	}
	if log.Message == "" {
		log.Message = "Unknown error"
	}

	ctxDB, cancel := context.WithTimeout(ctx, opsDBQueryTimeout)
	defer cancel()
	return s.repo.CreateErrorLog(ctxDB, log)
}

func (s *OpsService) RecordMetrics(ctx context.Context, metric *OpsMetrics) error {
	if metric == nil {
		return nil
	}
	if metric.UpdatedAt.IsZero() {
		metric.UpdatedAt = time.Now()
	}

	ctxDB, cancel := context.WithTimeout(ctx, opsDBQueryTimeout)
	defer cancel()
	if err := s.repo.CreateSystemMetric(ctxDB, metric); err != nil {
		return err
	}

	// Latest metrics snapshot is queried frequently by the ops dashboard; keep a short-lived cache
	// to avoid unnecessary DB pressure. Only cache the default (1-minute) window metrics.
	windowMinutes := metric.WindowMinutes
	if windowMinutes == 0 {
		windowMinutes = 1
	}
	if windowMinutes == 1 {
		if repo := s.repo; repo != nil {
			_ = repo.SetCachedLatestSystemMetric(ctx, metric)
		}
	}
	return nil
}

func (s *OpsService) ListErrorLogs(ctx context.Context, filters OpsErrorLogFilters) ([]OpsErrorLog, int, error) {
	ctxDB, cancel := context.WithTimeout(ctx, opsDBQueryTimeout)
	defer cancel()
	logs, err := s.repo.ListErrorLogsLegacy(ctxDB, filters)
	if err != nil {
		return nil, 0, err
	}
	return logs, len(logs), nil
}

func (s *OpsService) GetWindowStats(ctx context.Context, startTime, endTime time.Time) (*OpsWindowStats, error) {
	ctxDB, cancel := context.WithTimeout(ctx, opsDBQueryTimeout)
	defer cancel()
	return s.repo.GetWindowStats(ctxDB, startTime, endTime)
}

func (s *OpsService) GetLatestMetrics(ctx context.Context) (*OpsMetrics, error) {
	// Cache first (best-effort): cache errors should not break the dashboard.
	if s != nil {
		if repo := s.repo; repo != nil {
			if cached, err := repo.GetCachedLatestSystemMetric(ctx); err == nil && cached != nil {
				if cached.WindowMinutes == 0 {
					cached.WindowMinutes = 1
				}
				return cached, nil
			}
		}
	}

	ctxDB, cancel := context.WithTimeout(ctx, opsDBQueryTimeout)
	defer cancel()
	metric, err := s.repo.GetLatestSystemMetric(ctxDB)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return &OpsMetrics{WindowMinutes: 1}, nil
		}
		return nil, err
	}
	if metric == nil {
		return &OpsMetrics{WindowMinutes: 1}, nil
	}
	if metric.WindowMinutes == 0 {
		metric.WindowMinutes = 1
	}

	// Backfill cache (best-effort).
	if s != nil {
		if repo := s.repo; repo != nil {
			_ = repo.SetCachedLatestSystemMetric(ctx, metric)
		}
	}
	return metric, nil
}

func (s *OpsService) ListMetricsHistory(ctx context.Context, windowMinutes int, startTime, endTime time.Time, limit int) ([]OpsMetrics, error) {
	if s == nil || s.repo == nil {
		return nil, nil
	}
	if windowMinutes <= 0 {
		windowMinutes = 1
	}
	if limit <= 0 || limit > 5000 {
		limit = 300
	}
	if endTime.IsZero() {
		endTime = time.Now()
	}
	if startTime.IsZero() {
		startTime = endTime.Add(-time.Duration(limit) * opsMetricsInterval)
	}
	if startTime.After(endTime) {
		startTime, endTime = endTime, startTime
	}

	ctxDB, cancel := context.WithTimeout(ctx, opsDBQueryTimeout)
	defer cancel()
	return s.repo.ListSystemMetricsRange(ctxDB, windowMinutes, startTime, endTime, limit)
}

// DashboardOverviewData represents aggregated metrics for the ops dashboard overview.
type DashboardOverviewData struct {
	Timestamp    time.Time        `json:"timestamp"`
	HealthScore  int              `json:"health_score"`
	SLA          SLAData          `json:"sla"`
	QPS          QPSData          `json:"qps"`
	TPS          TPSData          `json:"tps"`
	Latency      LatencyData      `json:"latency"`
	Errors       ErrorData        `json:"errors"`
	Resources    ResourceData     `json:"resources"`
	SystemStatus SystemStatusData `json:"system_status"`
}

type SLAData struct {
	Current   float64 `json:"current"`
	Threshold float64 `json:"threshold"`
	Status    string  `json:"status"`
	Trend     string  `json:"trend"`
	Change24h float64 `json:"change_24h"`
}

type QPSData struct {
	Current           float64 `json:"current"`
	Peak1h            float64 `json:"peak_1h"`
	Avg1h             float64 `json:"avg_1h"`
	ChangeVsYesterday float64 `json:"change_vs_yesterday"`
}

type TPSData struct {
	Current float64 `json:"current"`
	Peak1h  float64 `json:"peak_1h"`
	Avg1h   float64 `json:"avg_1h"`
}

type LatencyData struct {
	P50          int    `json:"p50"`
	P95          int    `json:"p95"`
	P99          int    `json:"p99"`
	P999         int    `json:"p999"`
	Avg          int    `json:"avg"`
	Max          int    `json:"max"`
	ThresholdP99 int    `json:"threshold_p99"`
	Status       string `json:"status"`
}

type ErrorData struct {
	TotalCount   int64     `json:"total_count"`
	ErrorRate    float64   `json:"error_rate"`
	Count4xx     int64     `json:"4xx_count"`
	Count5xx     int64     `json:"5xx_count"`
	TimeoutCount int64     `json:"timeout_count"`
	TopError     *TopError `json:"top_error,omitempty"`
}

type TopError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Count   int64  `json:"count"`
}

type ResourceData struct {
	CPUUsage      float64           `json:"cpu_usage"`
	MemoryUsage   float64           `json:"memory_usage"`
	DiskUsage     float64           `json:"disk_usage"`
	Goroutines    int               `json:"goroutines"`
	DBConnections DBConnectionsData `json:"db_connections"`
}

type DBConnectionsData struct {
	Active  int `json:"active"`
	Idle    int `json:"idle"`
	Waiting int `json:"waiting"`
	Max     int `json:"max"`
}

type SystemStatusData struct {
	Redis          string `json:"redis"`
	Database       string `json:"database"`
	BackgroundJobs string `json:"background_jobs"`
}

type OverviewStats struct {
	RequestCount          int64
	SuccessCount          int64
	ErrorCount            int64
	Error4xxCount         int64
	Error5xxCount         int64
	TimeoutCount          int64
	LatencyP50            int
	LatencyP95            int
	LatencyP99            int
	LatencyP999           int
	LatencyAvg            int
	LatencyMax            int
	TopErrorCode          string
	TopErrorMsg           string
	TopErrorCount         int64
	CPUUsage              float64
	MemoryUsage           float64
	MemoryUsedMB          int64
	MemoryTotalMB         int64
	ConcurrencyQueueDepth int
}

func (s *OpsService) GetDashboardOverview(ctx context.Context, timeRange string) (*DashboardOverviewData, error) {
	if s == nil {
		return nil, errors.New("ops service not initialized")
	}
	repo := s.repo
	if repo == nil {
		return nil, errors.New("ops repository not initialized")
	}
	if s.sqlDB == nil {
		return nil, errors.New("ops service not initialized")
	}
	if strings.TrimSpace(timeRange) == "" {
		timeRange = "1h"
	}

	duration, err := parseTimeRange(timeRange)
	if err != nil {
		return nil, err
	}

	if cached, err := repo.GetCachedDashboardOverview(ctx, timeRange); err == nil && cached != nil {
		return cached, nil
	}

	now := time.Now().UTC()
	startTime := now.Add(-duration)

	ctxStats, cancelStats := context.WithTimeout(ctx, opsDBQueryTimeout)
	stats, err := repo.GetOverviewStats(ctxStats, startTime, now)
	cancelStats()
	if err != nil {
		return nil, fmt.Errorf("get overview stats: %w", err)
	}
	if stats == nil {
		return nil, errors.New("get overview stats returned nil")
	}

	var statsYesterday *OverviewStats
	{
		yesterdayEnd := now.Add(-24 * time.Hour)
		yesterdayStart := yesterdayEnd.Add(-duration)
		ctxYesterday, cancelYesterday := context.WithTimeout(ctx, opsDBQueryTimeout)
		ys, err := repo.GetOverviewStats(ctxYesterday, yesterdayStart, yesterdayEnd)
		cancelYesterday()
		if err != nil {
			// Best-effort: overview should still work when historical comparison fails.
			log.Printf("[OpsOverview] get yesterday overview stats failed: %v", err)
		} else {
			statsYesterday = ys
		}
	}

	totalReqs := stats.SuccessCount + stats.ErrorCount
	successRate, errorRate := calculateRates(stats.SuccessCount, stats.ErrorCount, totalReqs)

	successRateYesterday := 0.0
	totalReqsYesterday := int64(0)
	if statsYesterday != nil {
		totalReqsYesterday = statsYesterday.SuccessCount + statsYesterday.ErrorCount
		successRateYesterday, _ = calculateRates(statsYesterday.SuccessCount, statsYesterday.ErrorCount, totalReqsYesterday)
	}

	slaThreshold := 99.9
	slaChange24h := roundTo2DP(successRate - successRateYesterday)
	slaTrend := classifyTrend(slaChange24h, 0.05)
	slaStatus := classifySLAStatus(successRate, slaThreshold)

	latencyThresholdP99 := 1000
	latencyStatus := classifyLatencyStatus(stats.LatencyP99, latencyThresholdP99)

	qpsCurrent := 0.0
	{
		ctxWindow, cancelWindow := context.WithTimeout(ctx, opsDBQueryTimeout)
		windowStats, err := repo.GetWindowStats(ctxWindow, now.Add(-1*time.Minute), now)
		cancelWindow()
		if err == nil && windowStats != nil {
			qpsCurrent = roundTo1DP(float64(windowStats.SuccessCount+windowStats.ErrorCount) / 60)
		} else if err != nil {
			log.Printf("[OpsOverview] get realtime qps failed: %v", err)
		}
	}

	qpsAvg := roundTo1DP(safeDivide(float64(totalReqs), duration.Seconds()))
	qpsPeak := qpsAvg
	{
		limit := int(duration.Minutes()) + 5
		if limit < 10 {
			limit = 10
		}
		if limit > 5000 {
			limit = 5000
		}
		ctxMetrics, cancelMetrics := context.WithTimeout(ctx, opsDBQueryTimeout)
		items, err := repo.ListSystemMetricsRange(ctxMetrics, 1, startTime, now, limit)
		cancelMetrics()
		if err != nil {
			log.Printf("[OpsOverview] get metrics range for peak qps failed: %v", err)
		} else {
			maxQPS := 0.0
			for _, item := range items {
				v := float64(item.RequestCount) / 60
				if v > maxQPS {
					maxQPS = v
				}
			}
			if maxQPS > 0 {
				qpsPeak = roundTo1DP(maxQPS)
			}
		}
	}

	qpsAvgYesterday := 0.0
	if duration.Seconds() > 0 && totalReqsYesterday > 0 {
		qpsAvgYesterday = float64(totalReqsYesterday) / duration.Seconds()
	}
	qpsChangeVsYesterday := roundTo1DP(percentChange(qpsAvgYesterday, float64(totalReqs)/duration.Seconds()))

	tpsCurrent, tpsPeak, tpsAvg := 0.0, 0.0, 0.0
	if current, peak, avg, err := s.getTokenTPS(ctx, now, startTime, duration); err != nil {
		log.Printf("[OpsOverview] get token tps failed: %v", err)
	} else {
		tpsCurrent, tpsPeak, tpsAvg = roundTo1DP(current), roundTo1DP(peak), roundTo1DP(avg)
	}

	diskUsage := 0.0
	if v, err := getDiskUsagePercent(ctx, "/"); err != nil {
		log.Printf("[OpsOverview] get disk usage failed: %v", err)
	} else {
		diskUsage = roundTo1DP(v)
	}

	redisStatus := s.checkRedisHealth(ctx)
	dbStatus := s.checkDatabaseHealth(ctx)
	healthScore := calculateHealthScore(successRate, stats.LatencyP99, errorRate, redisStatus, dbStatus)

	data := &DashboardOverviewData{
		Timestamp:   now,
		HealthScore: healthScore,
		SLA: SLAData{
			Current:   successRate,
			Threshold: slaThreshold,
			Status:    slaStatus,
			Trend:     slaTrend,
			Change24h: slaChange24h,
		},
		QPS: QPSData{
			Current:           qpsCurrent,
			Peak1h:            qpsPeak,
			Avg1h:             qpsAvg,
			ChangeVsYesterday: qpsChangeVsYesterday,
		},
		TPS: TPSData{
			Current: tpsCurrent,
			Peak1h:  tpsPeak,
			Avg1h:   tpsAvg,
		},
		Latency: LatencyData{
			P50:          stats.LatencyP50,
			P95:          stats.LatencyP95,
			P99:          stats.LatencyP99,
			P999:         stats.LatencyP999,
			Avg:          stats.LatencyAvg,
			Max:          stats.LatencyMax,
			ThresholdP99: latencyThresholdP99,
			Status:       latencyStatus,
		},
		Errors: ErrorData{
			TotalCount:   stats.ErrorCount,
			ErrorRate:    errorRate,
			Count4xx:     stats.Error4xxCount,
			Count5xx:     stats.Error5xxCount,
			TimeoutCount: stats.TimeoutCount,
		},
		Resources: ResourceData{
			CPUUsage:      roundTo1DP(stats.CPUUsage),
			MemoryUsage:   roundTo1DP(stats.MemoryUsage),
			DiskUsage:     diskUsage,
			Goroutines:    runtime.NumGoroutine(),
			DBConnections: s.getDBConnections(),
		},
		SystemStatus: SystemStatusData{
			Redis:          redisStatus,
			Database:       dbStatus,
			BackgroundJobs: "healthy",
		},
	}

	if stats.TopErrorCount > 0 {
		data.Errors.TopError = &TopError{
			Code:    stats.TopErrorCode,
			Message: stats.TopErrorMsg,
			Count:   stats.TopErrorCount,
		}
	}

	_ = repo.SetCachedDashboardOverview(ctx, timeRange, data, 10*time.Second)

	return data, nil
}

func (s *OpsService) GetProviderHealth(ctx context.Context, timeRange string) ([]*ProviderHealthData, error) {
	if s == nil || s.repo == nil {
		return nil, nil
	}

	if strings.TrimSpace(timeRange) == "" {
		timeRange = "1h"
	}
	window, err := parseTimeRange(timeRange)
	if err != nil {
		return nil, err
	}

	endTime := time.Now()
	startTime := endTime.Add(-window)

	ctxDB, cancel := context.WithTimeout(ctx, opsDBQueryTimeout)
	stats, err := s.repo.GetProviderStats(ctxDB, startTime, endTime)
	cancel()
	if err != nil {
		return nil, err
	}

	results := make([]*ProviderHealthData, 0, len(stats))
	for _, item := range stats {
		if item == nil {
			continue
		}

		successRate, errorRate := calculateRates(item.SuccessCount, item.ErrorCount, item.RequestCount)

		results = append(results, &ProviderHealthData{
			Name:         formatPlatformName(item.Platform),
			RequestCount: item.RequestCount,
			SuccessRate:  successRate,
			ErrorRate:    errorRate,
			LatencyAvg:   item.AvgLatencyMs,
			LatencyP99:   item.P99LatencyMs,
			Status:       classifyProviderStatus(successRate, item.P99LatencyMs, item.TimeoutCount, item.RequestCount),
			ErrorsByType: ProviderHealthErrorsByType{
				HTTP4xx: item.Error4xxCount,
				HTTP5xx: item.Error5xxCount,
				Timeout: item.TimeoutCount,
			},
		})
	}

	return results, nil
}

func (s *OpsService) GetLatencyHistogram(ctx context.Context, timeRange string) ([]*LatencyHistogramItem, error) {
	if s == nil || s.repo == nil {
		return nil, nil
	}
	duration, err := parseTimeRange(timeRange)
	if err != nil {
		return nil, err
	}
	endTime := time.Now()
	startTime := endTime.Add(-duration)
	ctxDB, cancel := context.WithTimeout(ctx, opsDBQueryTimeout)
	defer cancel()
	return s.repo.GetLatencyHistogram(ctxDB, startTime, endTime)
}

func (s *OpsService) GetErrorDistribution(ctx context.Context, timeRange string) ([]*ErrorDistributionItem, error) {
	if s == nil || s.repo == nil {
		return nil, nil
	}
	duration, err := parseTimeRange(timeRange)
	if err != nil {
		return nil, err
	}
	endTime := time.Now()
	startTime := endTime.Add(-duration)
	ctxDB, cancel := context.WithTimeout(ctx, opsDBQueryTimeout)
	defer cancel()
	return s.repo.GetErrorDistribution(ctxDB, startTime, endTime)
}

func parseTimeRange(timeRange string) (time.Duration, error) {
	value := strings.TrimSpace(timeRange)
	if value == "" {
		return 0, errors.New("invalid time range")
	}

	// Support "7d" style day ranges for convenience.
	if strings.HasSuffix(value, "d") {
		numberPart := strings.TrimSuffix(value, "d")
		if numberPart == "" {
			return 0, errors.New("invalid time range")
		}
		days := 0
		for _, ch := range numberPart {
			if ch < '0' || ch > '9' {
				return 0, errors.New("invalid time range")
			}
			days = days*10 + int(ch-'0')
		}
		if days <= 0 {
			return 0, errors.New("invalid time range")
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}

	dur, err := time.ParseDuration(value)
	if err != nil || dur <= 0 {
		return 0, errors.New("invalid time range")
	}

	// Cap to avoid unbounded queries.
	const maxWindow = 30 * 24 * time.Hour
	if dur > maxWindow {
		dur = maxWindow
	}

	return dur, nil
}

func calculateHealthScore(successRate float64, p99Latency int, errorRate float64, redisStatus, dbStatus string) int {
	score := 100.0

	// SLA impact (max -45 points)
	if successRate < 99.9 {
		score -= math.Min(45, (99.9-successRate)*12)
	}

	// Latency impact (max -35 points)
	if p99Latency > 1000 {
		score -= math.Min(35, float64(p99Latency-1000)/80)
	}

	// Error rate impact (max -20 points)
	if errorRate > 0.1 {
		score -= math.Min(20, (errorRate-0.1)*60)
	}

	// Infra status impact
	if redisStatus != "healthy" {
		score -= 15
	}
	if dbStatus != "healthy" {
		score -= 20
	}

	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}

	return int(math.Round(score))
}

func calculateRates(successCount, errorCount, requestCount int64) (successRate float64, errorRate float64) {
	if requestCount <= 0 {
		return 0, 0
	}
	successRate = (float64(successCount) / float64(requestCount)) * 100
	errorRate = (float64(errorCount) / float64(requestCount)) * 100
	return roundTo2DP(successRate), roundTo2DP(errorRate)
}

func roundTo2DP(v float64) float64 {
	return math.Round(v*100) / 100
}

func roundTo1DP(v float64) float64 {
	return math.Round(v*10) / 10
}

func safeDivide(numerator float64, denominator float64) float64 {
	if denominator <= 0 {
		return 0
	}
	return numerator / denominator
}

func percentChange(previous float64, current float64) float64 {
	if previous == 0 {
		if current > 0 {
			return 100.0
		}
		return 0
	}
	return (current - previous) / previous * 100
}

func classifyTrend(delta float64, deadband float64) string {
	if delta > deadband {
		return "up"
	}
	if delta < -deadband {
		return "down"
	}
	return "stable"
}

func classifySLAStatus(successRate float64, threshold float64) string {
	if successRate >= threshold {
		return "healthy"
	}
	if successRate >= threshold-0.5 {
		return "warning"
	}
	return "critical"
}

func classifyLatencyStatus(p99LatencyMs int, thresholdP99 int) string {
	if thresholdP99 <= 0 {
		return "healthy"
	}
	if p99LatencyMs <= thresholdP99 {
		return "healthy"
	}
	if p99LatencyMs <= thresholdP99*2 {
		return "warning"
	}
	return "critical"
}

func getDiskUsagePercent(ctx context.Context, path string) (float64, error) {
	usage, err := disk.UsageWithContext(ctx, path)
	if err != nil {
		return 0, err
	}
	if usage == nil {
		return 0, nil
	}
	return usage.UsedPercent, nil
}

func (s *OpsService) checkRedisHealth(ctx context.Context) string {
	if s == nil {
		log.Printf("[OpsOverview][WARN] ops service is nil; redis health check skipped")
		return "critical"
	}
	if s.repo == nil {
		s.redisNilWarnOnce.Do(func() {
			log.Printf("[OpsOverview][WARN] ops repository is nil; redis health check skipped")
		})
		return "critical"
	}

	ctxPing, cancel := context.WithTimeout(ctx, 800*time.Millisecond)
	defer cancel()

	if err := s.repo.PingRedis(ctxPing); err != nil {
		log.Printf("[OpsOverview][WARN] redis ping failed: %v", err)
		return "critical"
	}
	return "healthy"
}

func (s *OpsService) checkDatabaseHealth(ctx context.Context) string {
	if s == nil {
		log.Printf("[OpsOverview][WARN] ops service is nil; db health check skipped")
		return "critical"
	}
	if s.sqlDB == nil {
		s.dbNilWarnOnce.Do(func() {
			log.Printf("[OpsOverview][WARN] database is nil; db health check skipped")
		})
		return "critical"
	}

	ctxPing, cancel := context.WithTimeout(ctx, 800*time.Millisecond)
	defer cancel()

	if err := s.sqlDB.PingContext(ctxPing); err != nil {
		log.Printf("[OpsOverview][WARN] db ping failed: %v", err)
		return "critical"
	}
	return "healthy"
}

func (s *OpsService) getDBConnections() DBConnectionsData {
	if s == nil || s.sqlDB == nil {
		return DBConnectionsData{}
	}

	stats := s.sqlDB.Stats()
	maxOpen := stats.MaxOpenConnections
	if maxOpen < 0 {
		maxOpen = 0
	}

	return DBConnectionsData{
		Active:  stats.InUse,
		Idle:    stats.Idle,
		Waiting: 0,
		Max:     maxOpen,
	}
}

func (s *OpsService) getTokenTPS(ctx context.Context, endTime time.Time, startTime time.Time, duration time.Duration) (current float64, peak float64, avg float64, err error) {
	if s == nil || s.sqlDB == nil {
		return 0, 0, 0, nil
	}

	if duration <= 0 {
		return 0, 0, 0, nil
	}

	// Current TPS: last 1 minute.
	var tokensLastMinute int64
	{
		lastMinuteStart := endTime.Add(-1 * time.Minute)
		ctxQuery, cancel := context.WithTimeout(ctx, opsDBQueryTimeout)
		row := s.sqlDB.QueryRowContext(ctxQuery, `
			SELECT COALESCE(SUM(input_tokens + output_tokens), 0)
			FROM usage_logs
			WHERE created_at >= $1 AND created_at < $2
		`, lastMinuteStart, endTime)
		scanErr := row.Scan(&tokensLastMinute)
		cancel()
		if scanErr != nil {
			return 0, 0, 0, scanErr
		}
	}

	var totalTokens int64
	var maxTokensPerMinute int64
	{
		ctxQuery, cancel := context.WithTimeout(ctx, opsDBQueryTimeout)
		row := s.sqlDB.QueryRowContext(ctxQuery, `
			WITH buckets AS (
				SELECT
					date_trunc('minute', created_at) AS bucket,
					SUM(input_tokens + output_tokens) AS tokens
				FROM usage_logs
				WHERE created_at >= $1 AND created_at < $2
				GROUP BY 1
			)
			SELECT
				COALESCE(SUM(tokens), 0) AS total_tokens,
				COALESCE(MAX(tokens), 0) AS max_tokens_per_minute
			FROM buckets
		`, startTime, endTime)
		scanErr := row.Scan(&totalTokens, &maxTokensPerMinute)
		cancel()
		if scanErr != nil {
			return 0, 0, 0, scanErr
		}
	}

	current = safeDivide(float64(tokensLastMinute), 60)
	peak = safeDivide(float64(maxTokensPerMinute), 60)
	avg = safeDivide(float64(totalTokens), duration.Seconds())
	return current, peak, avg, nil
}

func formatPlatformName(platform string) string {
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case PlatformOpenAI:
		return "OpenAI"
	case PlatformAnthropic:
		return "Anthropic"
	case PlatformGemini:
		return "Gemini"
	case PlatformAntigravity:
		return "Antigravity"
	default:
		if platform == "" {
			return "Unknown"
		}
		if len(platform) == 1 {
			return strings.ToUpper(platform)
		}
		return strings.ToUpper(platform[:1]) + platform[1:]
	}
}

func classifyProviderStatus(successRate float64, p99LatencyMs int, timeoutCount int64, requestCount int64) string {
	if requestCount <= 0 {
		return "healthy"
	}

	if successRate < 98 {
		return "critical"
	}
	if successRate < 99.5 {
		return "warning"
	}

	// Heavy timeout volume should be highlighted even if the overall success rate is okay.
	if timeoutCount >= 10 && requestCount >= 100 {
		return "warning"
	}

	if p99LatencyMs > 0 && p99LatencyMs >= 5000 {
		return "warning"
	}

	return "healthy"
}
