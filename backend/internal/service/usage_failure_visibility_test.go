package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/stretchr/testify/require"
)

type usageFailureVisibilityRepoStub struct {
	UsageLogRepository
	hydrations int
	failure    error
}

func (r *usageFailureVisibilityRepoStub) GetByID(context.Context, int64) (*UsageLog, error) {
	return &UsageLog{ID: 1, ActualCost: .125}, nil
}

func (r *usageFailureVisibilityRepoStub) ListWithFilters(context.Context, pagination.PaginationParams, usagestats.UsageLogFilters) ([]UsageLog, *pagination.PaginationResult, error) {
	return []UsageLog{{ID: 1, ActualCost: .125}}, nil, nil
}

func (r *usageFailureVisibilityRepoStub) HydrateUsageLogFailures(ctx context.Context, logs []UsageLog) error {
	r.hydrations++
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > 2*time.Second {
		return errors.New("unbounded failure lookup")
	}
	if r.failure != nil {
		return r.failure
	}
	status := 502
	for i := range logs {
		logs[i].FailureStatusCode = &status
	}
	return nil
}

func TestUsageFailureVisibilityHydratesDetailAndOnePageWithoutChangingCharges(t *testing.T) {
	repo := &usageFailureVisibilityRepoStub{}
	svc := &UsageService{usageRepo: repo}
	log, err := svc.GetByID(context.Background(), 1)
	require.NoError(t, err)
	require.Equal(t, 502, *log.FailureStatusCode)
	require.Equal(t, .125, log.ActualCost)
	logs, _, err := svc.ListWithFilters(context.Background(), pagination.PaginationParams{}, usagestats.UsageLogFilters{})
	require.NoError(t, err)
	require.Equal(t, 502, *logs[0].FailureStatusCode)
	require.Equal(t, .125, logs[0].ActualCost)
	require.Equal(t, 2, repo.hydrations)
}

func TestUsageFailureVisibilityKeepsBillingPageAvailableWhenLookupFails(t *testing.T) {
	svc := &UsageService{usageRepo: &usageFailureVisibilityRepoStub{failure: errors.New("ops unavailable")}}
	log, err := svc.GetByID(context.Background(), 1)
	require.NoError(t, err)
	require.NotNil(t, log)
	require.Nil(t, log.FailureStatusCode)
	require.Equal(t, .125, log.ActualCost)
}
