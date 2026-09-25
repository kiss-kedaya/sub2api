package service

import (
	"context"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

const (
	defaultGroupQualityCheckIntervalMinutes = 15
	// groupQualityStatusWindow is the recent-results window used to compute a
	// group's degradation status. Degraded probes older than this stop counting.
	groupQualityStatusWindow = 1 * time.Hour
	// groupQualitySuspectRatio is the degraded ratio at or above which a group
	// is reported as "suspect" (疑似降智) to users and admins.
	groupQualitySuspectRatio = 0.5
)

// GroupQualityStatus is the aggregated degradation state of one group.
type GroupQualityStatus struct {
	GroupID          int64      `json:"group_id"`
	Enabled          bool       `json:"enabled"`
	Status           string     `json:"status"` // unknown | healthy | suspect
	CheckedAccounts  int        `json:"checked_accounts"`
	DegradedAccounts int        `json:"degraded_accounts"`
	LastRunAt        *time.Time `json:"last_run_at"`
}

// GroupQualityBucket is one time bucket of a group's degradation series.
type GroupQualityBucket struct {
	BucketStart time.Time `json:"bucket_start"`
	Checked     int       `json:"checked"`
	Degraded    int       `json:"degraded"`
}

// GroupQualitySeries is a group's degradation history aligned to the caller's
// selected channel-monitor range.
type GroupQualitySeries struct {
	GroupID       int64                `json:"group_id"`
	BucketSeconds int64                `json:"bucket_seconds"`
	Buckets       []GroupQualityBucket `json:"buckets"`
}

// GroupQualityCheckService manages per-group degradation (降智) detection config
// and exposes aggregated status. Probes are produced by the per-account
// scheduled test plans; this service only toggles visibility and aggregates.
type GroupQualityCheckService struct {
	repo        GroupQualityCheckRepository
	accountRepo AccountRepository
}

// NewGroupQualityCheckService creates the service.
func NewGroupQualityCheckService(repo GroupQualityCheckRepository, accountRepo AccountRepository) *GroupQualityCheckService {
	return &GroupQualityCheckService{repo: repo, accountRepo: accountRepo}
}

// SetGroupEnabled enables/disables degradation detection for a group.
// Disabling also clears quality-check pauses on the group's accounts so they
// are not left permanently unschedulable with no checker to recover them.
func (s *GroupQualityCheckService) SetGroupEnabled(ctx context.Context, groupID int64, enabled bool) (*GroupQualityCheckSettings, error) {
	settings, err := s.repo.UpsertSettings(ctx, &GroupQualityCheckSettings{
		GroupID:         groupID,
		Enabled:         enabled,
		IntervalMinutes: defaultGroupQualityCheckIntervalMinutes,
	})
	if err != nil {
		return nil, err
	}

	if !enabled {
		s.clearGroupQualityPauses(ctx, groupID)
	}
	return settings, nil
}

func (s *GroupQualityCheckService) clearGroupQualityPauses(ctx context.Context, groupID int64) {
	if s.accountRepo == nil {
		return
	}
	accounts, err := s.accountRepo.ListByGroup(ctx, groupID)
	if err != nil {
		logger.LegacyPrintf("service.group_quality_check", "[GroupQualityCheck] group=%d pause cleanup list failed: %v", groupID, err)
		return
	}
	cleared := 0
	for _, account := range accounts {
		if strings.HasPrefix(account.TempUnschedulableReason, scheduledQualityReasonPrefix) {
			if err := s.accountRepo.ClearTempUnschedulable(ctx, account.ID); err == nil {
				cleared++
			}
		}
	}
	if cleared > 0 {
		logger.LegacyPrintf("service.group_quality_check", "[GroupQualityCheck] group=%d disabled: cleared quality pauses on %d accounts", groupID, cleared)
	}
}

// GetGroupStatus returns the aggregated degradation status for one group.
// Groups that never enabled detection report Enabled=false/Status=unknown.
func (s *GroupQualityCheckService) GetGroupStatus(ctx context.Context, groupID int64) (*GroupQualityStatus, error) {
	settings, err := s.repo.GetSettings(ctx, groupID)
	if err != nil {
		// No row: detection was never enabled for this group.
		return &GroupQualityStatus{GroupID: groupID, Enabled: false, Status: "unknown"}, nil
	}

	status := &GroupQualityStatus{
		GroupID:   groupID,
		Enabled:   settings.Enabled,
		Status:    "unknown",
		LastRunAt: settings.LastRunAt,
	}
	if !settings.Enabled {
		return status, nil
	}

	results, err := s.repo.ListRecentResults(ctx, groupID, time.Now().Add(-groupQualityStatusWindow), 200)
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return status, nil
	}

	status.CheckedAccounts = len(results)
	newest := results[0].CreatedAt
	for _, result := range results {
		if result.Status == "degraded" {
			status.DegradedAccounts++
		}
		if result.CreatedAt.After(newest) {
			newest = result.CreatedAt
		}
	}
	status.LastRunAt = &newest

	if status.DegradedAccounts == 0 {
		status.Status = "healthy"
	} else if float64(status.DegradedAccounts)/float64(len(results)) >= groupQualitySuspectRatio {
		status.Status = "suspect"
	} else {
		status.Status = "healthy"
	}
	return status, nil
}

// ListRecentResults returns the most recent probe results for a group.
func (s *GroupQualityCheckService) ListRecentResults(ctx context.Context, groupID int64, limit int) ([]*GroupQualityCheckResult, error) {
	if limit <= 0 {
		limit = 50
	}
	return s.repo.ListRecentResults(ctx, groupID, time.Now().Add(-24*time.Hour), limit)
}

// ListGroupStatuses returns statuses for all groups that have a settings row.
func (s *GroupQualityCheckService) ListGroupStatuses(ctx context.Context) (map[int64]*GroupQualityStatus, error) {
	settings, err := s.repo.ListAllSettings(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]*GroupQualityStatus, len(settings))
	for _, setting := range settings {
		status, err := s.GetGroupStatus(ctx, setting.GroupID)
		if err != nil {
			return nil, err
		}
		out[setting.GroupID] = status
	}
	return out, nil
}

// EnabledGroupIDs returns the group ids that currently have detection enabled.
func (s *GroupQualityCheckService) EnabledGroupIDs(ctx context.Context) ([]int64, error) {
	settings, err := s.repo.ListAllSettings(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]int64, 0, len(settings))
	for _, setting := range settings {
		if setting.Enabled {
			out = append(out, setting.GroupID)
		}
	}
	return out, nil
}

// ListGroupSeries returns each requested group's degradation buckets over the
// given window, bucketed by bucketSeconds. Only groups with detection enabled
// are returned; callers restrict groupIDs to what the viewer may see.
func (s *GroupQualityCheckService) ListGroupSeries(
	ctx context.Context,
	groupIDs []int64,
	since time.Time,
	bucketSeconds int64,
) (map[int64]*GroupQualitySeries, error) {
	if len(groupIDs) == 0 || bucketSeconds <= 0 {
		return map[int64]*GroupQualitySeries{}, nil
	}
	enabled, err := s.EnabledGroupIDs(ctx)
	if err != nil {
		return nil, err
	}
	if len(enabled) == 0 {
		return map[int64]*GroupQualitySeries{}, nil
	}
	wanted := make([]int64, 0, len(enabled))
	enabledSet := make(map[int64]struct{}, len(enabled))
	for _, id := range enabled {
		enabledSet[id] = struct{}{}
	}
	for _, id := range groupIDs {
		if _, ok := enabledSet[id]; ok {
			wanted = append(wanted, id)
		}
	}
	if len(wanted) == 0 {
		return map[int64]*GroupQualitySeries{}, nil
	}

	buckets, err := s.repo.ListGroupBuckets(ctx, wanted, since, bucketSeconds)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]*GroupQualitySeries, len(wanted))
	for _, id := range wanted {
		out[id] = &GroupQualitySeries{GroupID: id, BucketSeconds: bucketSeconds, Buckets: []GroupQualityBucket{}}
	}
	for _, bucket := range buckets {
		series, ok := out[bucket.GroupID]
		if !ok {
			continue
		}
		series.Buckets = append(series.Buckets, GroupQualityBucket{
			BucketStart: bucket.BucketStart,
			Checked:     bucket.Checked,
			Degraded:    bucket.Degraded,
		})
	}
	return out, nil
}
