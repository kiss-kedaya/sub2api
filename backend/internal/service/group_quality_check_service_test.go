package service

import (
	"context"
	"errors"
	"testing"
	"time"
)

type stubGroupQualityCheckRepo struct {
	settings map[int64]*GroupQualityCheckSettings
	results  map[int64][]*GroupQualityCheckResult
	lastRuns map[int64]time.Time
}

func newStubGroupQualityCheckRepo() *stubGroupQualityCheckRepo {
	return &stubGroupQualityCheckRepo{
		settings: make(map[int64]*GroupQualityCheckSettings),
		results:  make(map[int64][]*GroupQualityCheckResult),
		lastRuns: make(map[int64]time.Time),
	}
}

func (r *stubGroupQualityCheckRepo) UpsertSettings(ctx context.Context, s *GroupQualityCheckSettings) (*GroupQualityCheckSettings, error) {
	now := time.Now()
	stored := &GroupQualityCheckSettings{
		GroupID:         s.GroupID,
		Enabled:         s.Enabled,
		IntervalMinutes: s.IntervalMinutes,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if existing, ok := r.settings[s.GroupID]; ok {
		stored.CreatedAt = existing.CreatedAt
		stored.LastRunAt = existing.LastRunAt
	}
	r.settings[s.GroupID] = stored
	return stored, nil
}

func (r *stubGroupQualityCheckRepo) GetSettings(ctx context.Context, groupID int64) (*GroupQualityCheckSettings, error) {
	if s, ok := r.settings[groupID]; ok {
		return s, nil
	}
	return nil, errors.New("not found")
}

func (r *stubGroupQualityCheckRepo) ListEnabledSettings(ctx context.Context) ([]*GroupQualityCheckSettings, error) {
	var out []*GroupQualityCheckSettings
	for _, s := range r.settings {
		if s.Enabled {
			out = append(out, s)
		}
	}
	return out, nil
}

func (r *stubGroupQualityCheckRepo) ListAllSettings(ctx context.Context) ([]*GroupQualityCheckSettings, error) {
	out := make([]*GroupQualityCheckSettings, 0, len(r.settings))
	for _, s := range r.settings {
		out = append(out, s)
	}
	return out, nil
}

func (r *stubGroupQualityCheckRepo) UpdateLastRun(ctx context.Context, groupID int64, lastRunAt time.Time) error {
	r.lastRuns[groupID] = lastRunAt
	if s, ok := r.settings[groupID]; ok {
		s.LastRunAt = &lastRunAt
	}
	return nil
}

func (r *stubGroupQualityCheckRepo) CreateResult(ctx context.Context, result *GroupQualityCheckResult) (*GroupQualityCheckResult, error) {
	result.ID = int64(len(r.results[result.GroupID]) + 1)
	result.CreatedAt = time.Now()
	r.results[result.GroupID] = append(r.results[result.GroupID], result)
	return result, nil
}

func (r *stubGroupQualityCheckRepo) ListRecentResults(ctx context.Context, groupID int64, since time.Time, limit int) ([]*GroupQualityCheckResult, error) {
	all := r.results[groupID]
	out := make([]*GroupQualityCheckResult, 0, limit)
	for i := len(all) - 1; i >= 0 && len(out) < limit; i-- {
		if !all[i].CreatedAt.Before(since) {
			out = append(out, all[i])
		}
	}
	return out, nil
}

func TestGroupQualityCheckService_GetGroupStatus_NeverConfigured(t *testing.T) {
	svc := NewGroupQualityCheckService(newStubGroupQualityCheckRepo(), nil)
	status, err := svc.GetGroupStatus(context.Background(), 7)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.Enabled || status.Status != "unknown" {
		t.Fatalf("never-configured group must report disabled/unknown, got %+v", status)
	}
}

func TestGroupQualityCheckService_GetGroupStatus_Healthy(t *testing.T) {
	repo := newStubGroupQualityCheckRepo()
	svc := NewGroupQualityCheckService(repo, nil)
	if _, err := svc.SetGroupEnabled(context.Background(), 1, true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	for _, st := range []string{"success", "success", "failed"} {
		if _, err := repo.CreateResult(context.Background(), &GroupQualityCheckResult{GroupID: 1, AccountID: 10, Status: st}); err != nil {
			t.Fatalf("create result: %v", err)
		}
	}
	status, err := svc.GetGroupStatus(context.Background(), 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.Status != "healthy" || status.CheckedAccounts != 3 || status.DegradedAccounts != 0 {
		t.Fatalf("expected healthy 3/0, got %+v", status)
	}
}

func TestGroupQualityCheckService_GetGroupStatus_Suspect(t *testing.T) {
	repo := newStubGroupQualityCheckRepo()
	svc := NewGroupQualityCheckService(repo, nil)
	if _, err := svc.SetGroupEnabled(context.Background(), 2, true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	for _, st := range []string{"degraded", "degraded", "success"} {
		if _, err := repo.CreateResult(context.Background(), &GroupQualityCheckResult{GroupID: 2, AccountID: 20, Status: st}); err != nil {
			t.Fatalf("create result: %v", err)
		}
	}
	status, err := svc.GetGroupStatus(context.Background(), 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.Status != "suspect" || status.DegradedAccounts != 2 {
		t.Fatalf("expected suspect 2 degraded, got %+v", status)
	}
}

func TestGroupQualityCheckService_GetGroupStatus_Disabled(t *testing.T) {
	repo := newStubGroupQualityCheckRepo()
	svc := NewGroupQualityCheckService(repo, nil)
	if _, err := svc.SetGroupEnabled(context.Background(), 3, true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if _, err := svc.SetGroupEnabled(context.Background(), 3, false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	status, err := svc.GetGroupStatus(context.Background(), 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.Enabled || status.Status != "unknown" {
		t.Fatalf("disabled group must report disabled/unknown, got %+v", status)
	}
}

func TestGroupQualityCheckService_SetGroupEnabled_DefaultsInterval(t *testing.T) {
	repo := newStubGroupQualityCheckRepo()
	svc := NewGroupQualityCheckService(repo, nil)
	settings, err := svc.SetGroupEnabled(context.Background(), 4, true)
	if err != nil {
		t.Fatalf("enable: %v", err)
	}
	if settings.IntervalMinutes != defaultGroupQualityCheckIntervalMinutes {
		t.Fatalf("expected default interval %d, got %d", defaultGroupQualityCheckIntervalMinutes, settings.IntervalMinutes)
	}
}
