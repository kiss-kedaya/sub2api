package service

import (
	"context"
	"time"
)

// GroupQualityCheckSettings is the per-group degradation (降智) detection config.
type GroupQualityCheckSettings struct {
	GroupID         int64      `json:"group_id"`
	Enabled         bool       `json:"enabled"`
	IntervalMinutes int        `json:"interval_minutes"`
	LastRunAt       *time.Time `json:"last_run_at"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// GroupQualityCheckResult is one account probe inside a group quality check run.
type GroupQualityCheckResult struct {
	ID           int64     `json:"id"`
	GroupID      int64     `json:"group_id"`
	AccountID    int64     `json:"account_id"`
	Status       string    `json:"status"` // success | failed | degraded
	ErrorMessage string    `json:"error_message"`
	LatencyMs    int64     `json:"latency_ms"`
	CreatedAt    time.Time `json:"created_at"`
}

// GroupQualityCheckRepository persists group quality check settings/results.
type GroupQualityCheckRepository interface {
	UpsertSettings(ctx context.Context, settings *GroupQualityCheckSettings) (*GroupQualityCheckSettings, error)
	GetSettings(ctx context.Context, groupID int64) (*GroupQualityCheckSettings, error)
	ListEnabledSettings(ctx context.Context) ([]*GroupQualityCheckSettings, error)
	ListAllSettings(ctx context.Context) ([]*GroupQualityCheckSettings, error)
	UpdateLastRun(ctx context.Context, groupID int64, lastRunAt time.Time) error
	CreateResult(ctx context.Context, result *GroupQualityCheckResult) (*GroupQualityCheckResult, error)
	ListRecentResults(ctx context.Context, groupID int64, since time.Time, limit int) ([]*GroupQualityCheckResult, error)
}
