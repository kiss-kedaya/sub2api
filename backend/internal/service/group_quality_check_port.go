package service

import (
	"context"
	"time"
)

// GroupQualityCheckSettings is the per-group degradation (降智) detection config.
// Enabling a group only makes its aggregated status visible to admins and end
// users; the probes themselves come from the group accounts' scheduled test
// plans.
type GroupQualityCheckSettings struct {
	GroupID         int64      `json:"group_id"`
	Enabled         bool       `json:"enabled"`
	IntervalMinutes int        `json:"interval_minutes"`
	LastRunAt       *time.Time `json:"last_run_at"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// GroupQualityCheckResult is one account probe aggregated for a group, sourced
// from the per-account scheduled test results.
type GroupQualityCheckResult struct {
	ID           int64     `json:"id"`
	GroupID      int64     `json:"group_id"`
	AccountID    int64     `json:"account_id"`
	Status       string    `json:"status"` // success | failed | degraded
	ErrorMessage string    `json:"error_message"`
	LatencyMs    int64     `json:"latency_ms"`
	CreatedAt    time.Time `json:"created_at"`
}

// GroupQualityBucketRow is one aggregated bucket of scheduled test results for
// a group: how many accounts were checked and how many were degraded.
type GroupQualityBucketRow struct {
	GroupID     int64
	BucketStart time.Time
	Checked     int
	Degraded    int
}

// GroupQualityCheckRepository persists the per-group toggle and reads the
// aggregated probe history produced by scheduled test plans.
type GroupQualityCheckRepository interface {
	UpsertSettings(ctx context.Context, settings *GroupQualityCheckSettings) (*GroupQualityCheckSettings, error)
	GetSettings(ctx context.Context, groupID int64) (*GroupQualityCheckSettings, error)
	ListAllSettings(ctx context.Context) ([]*GroupQualityCheckSettings, error)
	// ListRecentResults returns the most recent scheduled test result per
	// account in the group, limited to the given time window.
	ListRecentResults(ctx context.Context, groupID int64, since time.Time, limit int) ([]*GroupQualityCheckResult, error)
	// ListGroupBuckets aggregates scheduled test results per group and time
	// bucket for the given window.
	ListGroupBuckets(ctx context.Context, groupIDs []int64, since time.Time, bucketSeconds int64) ([]GroupQualityBucketRow, error)
}
