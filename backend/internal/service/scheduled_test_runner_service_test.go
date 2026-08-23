package service

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type scheduledTestPlanRepoStub struct {
	listDueCalls atomic.Int32
	plans        []*ScheduledTestPlan
}

func (r *scheduledTestPlanRepoStub) Create(context.Context, *ScheduledTestPlan) (*ScheduledTestPlan, error) {
	return nil, nil
}

func (r *scheduledTestPlanRepoStub) GetByID(context.Context, int64) (*ScheduledTestPlan, error) {
	return nil, nil
}

func (r *scheduledTestPlanRepoStub) ListByAccountID(context.Context, int64) ([]*ScheduledTestPlan, error) {
	return nil, nil
}

func (r *scheduledTestPlanRepoStub) ListDue(context.Context, time.Time) ([]*ScheduledTestPlan, error) {
	r.listDueCalls.Add(1)
	return r.plans, nil
}

func (r *scheduledTestPlanRepoStub) Update(context.Context, *ScheduledTestPlan) (*ScheduledTestPlan, error) {
	return nil, nil
}

func (r *scheduledTestPlanRepoStub) Delete(context.Context, int64) error {
	return nil
}

func (r *scheduledTestPlanRepoStub) UpdateAfterRun(context.Context, int64, time.Time, time.Time) error {
	return nil
}

func TestScheduledTestRunnerService_SkipsWhenPeerIsLeader(t *testing.T) {
	cache := &fakeLeaderLockCache{}
	_, err := cache.TryAcquireLeaderLock(context.Background(), scheduledTestRunnerLeaderLockKey, "peer", time.Minute)
	require.NoError(t, err)

	repo := &scheduledTestPlanRepoStub{}
	runner := NewScheduledTestRunnerService(repo, nil, nil, nil, nil)
	runner.alignmentDelay = 0
	runner.SetLeaderLock(cache, nil)

	runner.runScheduled()

	require.Zero(t, repo.listDueCalls.Load(), "a non-leader must not scan due plans")
	require.Equal(t, "peer", cache.heldBy(scheduledTestRunnerLeaderLockKey))
}

func TestScheduledTestRunnerService_RunsAndReleasesLeaderLock(t *testing.T) {
	cache := &fakeLeaderLockCache{}
	repo := &scheduledTestPlanRepoStub{}
	runner := NewScheduledTestRunnerService(repo, nil, nil, nil, nil)
	runner.alignmentDelay = 0
	runner.SetLeaderLock(cache, nil)

	runner.runScheduled()

	require.Equal(t, int32(1), repo.listDueCalls.Load(), "the leader should scan due plans once")
	require.Empty(t, cache.heldBy(scheduledTestRunnerLeaderLockKey), "the lock must be released after the tick")
}
