package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRefreshSchedulerAccountFreshnessRejectsDurablePause(t *testing.T) {
	repo := &schedulerFreshnessRepoStub{projection: map[int64]SchedulerFreshness{
		41: schedulerFreshnessTestValue(41, nil),
	}}
	repo.projection[41] = SchedulerFreshness{
		ID: 41, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Status: StatusDisabled, Schedulable: false,
	}
	svc := &OpenAIGatewayService{accountRepo: repo, schedulerSnapshot: &SchedulerSnapshotService{}}
	ctx := withSchedulerFreshness(context.Background(), repo, svc.schedulerSnapshot, 41)
	account := &Account{ID: 41, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true}

	refreshed, ok := svc.RefreshSchedulerAccountFreshness(ctx, account, "gpt-5.4")
	require.False(t, ok)
	require.Nil(t, refreshed)
}

func TestRefreshSchedulerAccountFreshnessOverlaysLatestRoutingFields(t *testing.T) {
	rate := 0.4
	repo := &schedulerFreshnessRepoStub{projection: map[int64]SchedulerFreshness{
		42: {
			ID: 42, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
			Status: StatusActive, Schedulable: true,
		},
	}}
	svc := &OpenAIGatewayService{accountRepo: repo, schedulerSnapshot: &SchedulerSnapshotService{}}
	ctx := withSchedulerFreshness(context.Background(), repo, svc.schedulerSnapshot, 42)
	account := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, RateMultiplier: &rate}

	refreshed, ok := svc.RefreshSchedulerAccountFreshness(ctx, account, "gpt-5.4")
	require.True(t, ok)
	require.NotNil(t, refreshed)
	require.Equal(t, int64(42), refreshed.ID)
	require.Same(t, &rate, refreshed.RateMultiplier)
}
