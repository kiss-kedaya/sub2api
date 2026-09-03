package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
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

func TestRefreshSchedulerRequestContextUsesSnapshotWithoutProjection(t *testing.T) {
	snapshotAccount := &Account{ID: 43, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Credentials: map[string]any{"openai_apikey_responses_websockets_v2_enabled": true}}
	repo := &schedulerFreshnessRepoStub{projection: map[int64]SchedulerFreshness{
		43: {ID: 43, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusDisabled, Schedulable: false},
	}}
	svc := &OpenAIGatewayService{accountRepo: repo, schedulerSnapshot: &SchedulerSnapshotService{cache: &openAISnapshotCacheStub{accountsByID: map[int64]*Account{43: snapshotAccount}}, cfg: &config.Config{Gateway: config.GatewayConfig{Scheduling: config.GatewaySchedulingConfig{RequestFreshnessEnabled: false}}}}}
	ctx := svc.PrepareSchedulerRequestContext(context.Background())
	ctx = svc.RefreshSchedulerRequestContext(ctx)
	account := &Account{ID: 43, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true}

	refreshed, ok := svc.RefreshSchedulerAccountFreshness(ctx, account, "gpt-5.4")
	require.True(t, ok)
	require.NotSame(t, account, refreshed)
	require.Equal(t, snapshotAccount.ID, refreshed.ID)
	repo.mu.Lock()
	defer repo.mu.Unlock()
	require.Zero(t, repo.projectionCalls, "snapshot-only WS turn must not query durable freshness")
}
