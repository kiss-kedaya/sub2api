package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/stretchr/testify/require"
)

type usageDashboardStatsProbe struct {
	UsageLogRepository
	calls int
	err   error
}

func (r *usageDashboardStatsProbe) GetAPIKeyDashboardStats(context.Context, int64) (*usagestats.UserDashboardStats, error) {
	r.calls++
	return &usagestats.UserDashboardStats{TotalRequests: int64(r.calls)}, r.err
}

func TestUsageStatsCacheServiceUsesRepositorySnapshotPolicy(t *testing.T) {
	repo := &usageDashboardStatsProbe{}
	svc := NewUsageService(repo, nil, nil, nil)
	for _, want := range []int64{1, 2} {
		stats, err := svc.GetAPIKeyDashboardStats(context.Background(), 7)
		require.NoError(t, err)
		require.Equal(t, want, stats.TotalRequests, "the repository, not a second service TTL, owns freshness")
	}
	repo.err = errors.New("database timeout")
	_, err := svc.GetAPIKeyDashboardStats(context.Background(), 7)
	require.ErrorIs(t, err, repo.err)
	_, err = svc.GetAPIKeyDashboardStats(context.Background(), 0)
	require.Error(t, err)
	require.Equal(t, 3, repo.calls)
}
