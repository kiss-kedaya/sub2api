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
	buckets  map[int64][]GroupQualityBucketRow
}

func newStubGroupQualityCheckRepo() *stubGroupQualityCheckRepo {
	return &stubGroupQualityCheckRepo{
		settings: make(map[int64]*GroupQualityCheckSettings),
		results:  make(map[int64][]*GroupQualityCheckResult),
		buckets:  make(map[int64][]GroupQualityBucketRow),
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

func (r *stubGroupQualityCheckRepo) ListAllSettings(ctx context.Context) ([]*GroupQualityCheckSettings, error) {
	out := make([]*GroupQualityCheckSettings, 0, len(r.settings))
	for _, s := range r.settings {
		out = append(out, s)
	}
	return out, nil
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

func (r *stubGroupQualityCheckRepo) ListGroupBuckets(ctx context.Context, groupIDs []int64, since time.Time, bucketSeconds int64) ([]GroupQualityBucketRow, error) {
	var out []GroupQualityBucketRow
	for _, id := range groupIDs {
		out = append(out, r.buckets[id]...)
	}
	return out, nil
}

func (r *stubGroupQualityCheckRepo) addResult(groupID, accountID int64, status string, at time.Time) {
	r.results[groupID] = append(r.results[groupID], &GroupQualityCheckResult{
		GroupID:   groupID,
		AccountID: accountID,
		Status:    status,
		CreatedAt: at,
	})
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
	now := time.Now()
	for i, st := range []string{"success", "success", "failed"} {
		repo.addResult(1, int64(10+i), st, now)
	}
	status, err := svc.GetGroupStatus(context.Background(), 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.Status != "healthy" || status.CheckedAccounts != 2 || status.DegradedAccounts != 0 {
		t.Fatalf("expected healthy 2/0 (transport failure excluded), got %+v", status)
	}
}

func TestGroupQualityCheckService_GetGroupStatus_Inconclusive(t *testing.T) {
	repo := newStubGroupQualityCheckRepo()
	svc := NewGroupQualityCheckService(repo, nil)
	if _, err := svc.SetGroupEnabled(context.Background(), 1, true); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	repo.addResult(1, 1, "unknown", now)
	repo.addResult(1, 2, "failed", now)
	status, err := svc.GetGroupStatus(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != "unknown" || status.CheckedAccounts != 0 || status.LastRunAt == nil {
		t.Fatalf("inconclusive probes must not imply healthy: %+v", status)
	}
	repo.addResult(1, 3, "degraded", now)
	status, err = svc.GetGroupStatus(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != "suspect" || status.CheckedAccounts != 1 || status.DegradedAccounts != 1 {
		t.Fatalf("inconclusive probes must not dilute confirmed defects: %+v", status)
	}
}

func TestGroupQualityCheckService_GetGroupStatus_Suspect(t *testing.T) {
	repo := newStubGroupQualityCheckRepo()
	svc := NewGroupQualityCheckService(repo, nil)
	if _, err := svc.SetGroupEnabled(context.Background(), 2, true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	now := time.Now()
	for i, st := range []string{"degraded", "degraded", "success"} {
		repo.addResult(2, int64(20+i), st, now)
	}
	status, err := svc.GetGroupStatus(context.Background(), 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.Status != "suspect" || status.DegradedAccounts != 2 {
		t.Fatalf("expected suspect 2 degraded, got %+v", status)
	}
}

func TestGroupQualityCheckService_GetGroupStatus_SingleDegradedOfManyIsHealthy(t *testing.T) {
	repo := newStubGroupQualityCheckRepo()
	svc := NewGroupQualityCheckService(repo, nil)
	if _, err := svc.SetGroupEnabled(context.Background(), 5, true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	now := time.Now()
	repo.addResult(5, 50, "degraded", now)
	for i := 0; i < 9; i++ {
		repo.addResult(5, int64(51+i), "success", now)
	}
	status, err := svc.GetGroupStatus(context.Background(), 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.Status != "healthy" || status.DegradedAccounts != 1 {
		t.Fatalf("1 degraded of 10 must stay healthy, got %+v", status)
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

func TestGroupQualityCheckService_ListGroupSeries_OnlyEnabledGroups(t *testing.T) {
	repo := newStubGroupQualityCheckRepo()
	svc := NewGroupQualityCheckService(repo, nil)
	if _, err := svc.SetGroupEnabled(context.Background(), 43, true); err != nil {
		t.Fatalf("enable 43: %v", err)
	}
	if _, err := svc.SetGroupEnabled(context.Background(), 48, false); err != nil {
		t.Fatalf("disable 48: %v", err)
	}
	now := time.Now()
	repo.buckets[43] = []GroupQualityBucketRow{{GroupID: 43, BucketStart: now, Checked: 4, Degraded: 2}}
	repo.buckets[48] = []GroupQualityBucketRow{{GroupID: 48, BucketStart: now, Checked: 4, Degraded: 4}}

	series, err := svc.ListGroupSeries(context.Background(), []int64{43, 48, 99}, time.Now().Add(-time.Hour), 300)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(series) != 1 {
		t.Fatalf("only enabled groups may return series, got %d entries", len(series))
	}
	got, ok := series[43]
	if !ok {
		t.Fatalf("enabled group 43 missing from series")
	}
	if len(got.Buckets) != 1 || got.Buckets[0].Checked != 4 || got.Buckets[0].Degraded != 2 {
		t.Fatalf("unexpected series payload: %+v", got)
	}
}

func TestGroupQualityCheckService_ListGroupSeries_NoEnabledGroups(t *testing.T) {
	svc := NewGroupQualityCheckService(newStubGroupQualityCheckRepo(), nil)
	series, err := svc.ListGroupSeries(context.Background(), []int64{1, 2}, time.Now().Add(-time.Hour), 300)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(series) != 0 {
		t.Fatalf("expected empty series, got %+v", series)
	}
}
