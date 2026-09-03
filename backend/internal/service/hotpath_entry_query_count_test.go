package service

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type countingAccountGetByIDRepo struct {
	AccountRepository
	account Account
	calls   atomic.Int64
}

func (r *countingAccountGetByIDRepo) GetByID(_ context.Context, id int64) (*Account, error) {
	r.calls.Add(1)
	if r.account.ID != id {
		return nil, nil
	}
	clone := r.account
	return &clone, nil
}

func TestGeminiResolvePlatform_SnapshotHitDoesNotQueryGroupRepo(t *testing.T) {
	t.Parallel()
	groupID := int64(71)
	repo := &groupLookupHotpathRepoStub{group: &Group{ID: groupID, Platform: PlatformGemini, Status: StatusActive}}
	cfg := &config.Config{}
	snapshot := NewSchedulerSnapshotService(nil, nil, nil, repo, cfg)
	snapshot.seedCachedGroup(repo.group)
	svc := &GeminiMessagesCompatService{groupRepo: repo, schedulerSnapshot: snapshot}

	platform, mixed, force, err := svc.resolvePlatformAndSchedulingMode(withSchedulerSnapshotOnly(context.Background()), &groupID)
	require.NoError(t, err)
	require.Equal(t, PlatformGemini, platform)
	require.True(t, mixed)
	require.False(t, force)
	require.Zero(t, repo.calls.Load(), "snapshot group hit must not query PostgreSQL")
}

func TestGeminiResolvePlatform_SnapshotColdDoesNotBlockOnGroupRepo(t *testing.T) {
	t.Parallel()
	groupID := int64(72)
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	repo := &groupLookupHotpathRepoStub{
		group:   &Group{ID: groupID, Platform: PlatformGemini, Status: StatusActive},
		started: started,
		release: release,
	}
	cfg := &config.Config{}
	snapshot := NewSchedulerSnapshotService(nil, nil, nil, repo, cfg)
	svc := &GeminiMessagesCompatService{groupRepo: repo, schedulerSnapshot: snapshot}

	done := make(chan error, 1)
	go func() {
		_, _, _, err := svc.resolvePlatformAndSchedulingMode(withSchedulerSnapshotOnly(context.Background()), &groupID)
		done <- err
	}()
	select {
	case err := <-done:
		require.ErrorIs(t, err, ErrSchedulerCacheNotReady)
	case <-time.After(250 * time.Millisecond):
		t.Fatal("gemini snapshot-only cold group lookup blocked on PostgreSQL")
	}
	close(release)
}

func TestOpenAIWSSticky_SnapshotOnlyDoesNotRequeryAccount(t *testing.T) {
	t.Parallel()
	account := Account{
		ID:          8101,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 2,
		Extra: map[string]any{
			"openai_apikey_responses_websockets_v2_enabled": true,
		},
	}
	repo := &countingAccountGetByIDRepo{account: account}
	snapshotCache := &openAISnapshotCacheStub{accountsByID: map[int64]*Account{account.ID: &account}}
	cfg := newOpenAIWSV2TestConfig()
	cfg.Gateway.Scheduling.RequestFreshnessEnabled = false
	svc := &OpenAIGatewayService{
		accountRepo:       repo,
		cfg:               cfg,
		schedulerSnapshot: &SchedulerSnapshotService{cache: snapshotCache, cfg: cfg},
	}

	got, err := svc.getSchedulableAccount(withSchedulerSnapshotOnly(context.Background()), account.ID)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, account.ID, got.ID)
	require.Zero(t, repo.calls.Load(), "snapshot-only WS hydration must not GetByID")
}
