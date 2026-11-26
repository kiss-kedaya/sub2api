//go:build unit

package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// 开关关闭时 wire 构造出的 worker 就是改动前的 worker：不采样、不退回、不接低余额观察者。
func TestProvideUsageBalanceSettlementWorker_SwitchOffIsTheLegacyWorker(t *testing.T) {
	resetSettlementProcessState(t)
	repo := &guardTestRepo{}
	notify, _ := newBalanceNotifyServiceForTest()

	for name, cfg := range map[string]*config.Config{"nil config": nil, "default config": {}} {
		t.Run(name, func(t *testing.T) {
			worker := ProvideUsageBalanceSettlementWorker(repo, cfg, notify)
			t.Cleanup(worker.Stop)
			require.False(t, worker.opts.MergeEnabled)
			require.Nil(t, worker.opts.Observer)
			require.Nil(t, worker.backlogReader)
			require.Nil(t, worker.observeCh)
			require.True(t, UsageBalanceSettlementConsumerRunning())
			require.False(t, UsageBalanceSettlementNotifierActive())

			time.Sleep(30 * time.Millisecond)
			require.Zero(t, repo.backlogCalls.Load(), "no sampling query with the switch off")
			worker.Stop()
		})
	}
}

func TestProvideUsageBalanceSettlementWorker_SwitchOnArmsTheGuardFromConfig(t *testing.T) {
	resetSettlementProcessState(t)
	repo := &guardTestRepo{}
	notify, _ := newBalanceNotifyServiceForTest()
	cfg := &config.Config{}
	cfg.Billing.BalanceMergeSettlementEnabled = true
	cfg.Billing.BalanceMergeSettlementMaxBacklog = 1234
	cfg.Billing.BalanceMergeSettlementMaxAgeSeconds = 17

	worker := ProvideUsageBalanceSettlementWorker(repo, cfg, notify)
	t.Cleanup(worker.Stop)
	require.True(t, worker.opts.MergeEnabled)
	require.Equal(t, int64(1234), worker.opts.MaxBacklog)
	require.Equal(t, 17*time.Second, worker.opts.MaxAge)
	require.Same(t, notify, worker.opts.Observer)
	require.NotNil(t, worker.backlogReader)
	require.NotNil(t, worker.observeCh)
	require.True(t, UsageBalanceSettlementNotifierActive())
	require.Eventually(t, func() bool { return repo.backlogCalls.Load() > 0 }, 3*time.Second, 5*time.Millisecond,
		"the monitor samples the queue right after start")
	worker.Stop()

	// Switch on but nobody to notify: the guard still runs, the request path keeps the alerts.
	resetSettlementProcessState(t)
	noNotify := ProvideUsageBalanceSettlementWorker(repo, cfg, nil)
	t.Cleanup(noNotify.Stop)
	require.NotNil(t, noNotify.backlogReader)
	require.Nil(t, noNotify.opts.Observer)
	require.Nil(t, noNotify.observeCh)
	require.False(t, UsageBalanceSettlementNotifierActive())
}

func TestProvideUsageBalanceSettlementWorker_ZeroLimitsDisableTheTriggersNotTheObservation(t *testing.T) {
	resetSettlementProcessState(t)
	repo := &guardTestRepo{}
	repo.setBacklog(UsageBalanceSettlementBacklog{Pending: 10_000_000, OldestAge: 24 * time.Hour})
	cfg := &config.Config{}
	cfg.Billing.BalanceMergeSettlementEnabled = true

	worker := ProvideUsageBalanceSettlementWorker(repo, cfg, nil)
	t.Cleanup(worker.Stop)
	require.Eventually(t, func() bool { return repo.backlogCalls.Load() >= 3 }, 3*time.Second, 5*time.Millisecond)
	require.False(t, UsageBalanceSettlementBacklogFallbackActive(), "0/0 means no trigger: a huge backlog never forces synchronous charging")
	health := worker.Health(context.Background())
	require.Equal(t, int64(10_000_000), health.Pending, "but the backlog is still observable")
}

type notifyUserRepoStub struct{ UserRepository }

func (*notifyUserRepoStub) GetByID(context.Context, int64) (*User, error) { return nil, ErrUserNotFound }

func TestProvideBalanceNotifyService_GetsTheUserReader(t *testing.T) {
	repo := &notifyUserRepoStub{}
	svc := ProvideBalanceNotifyService(nil, nil, nil, nil, repo)
	require.Same(t, repo, svc.userReader)

	withoutRepo := ProvideBalanceNotifyService(nil, nil, nil, nil, nil)
	require.Nil(t, withoutRepo.userReader, "no user repository, no settlement alerts (and no nil-interface trap)")
}

// 运维健康接口：结算队列状态作为 balance_settlement 加进现有的 live-balance-outbox 响应。
func TestOpsHealthExposesTheBalanceSettlementQueue(t *testing.T) {
	resetSettlementProcessState(t)
	repo := &guardTestRepo{}
	repo.setBacklog(UsageBalanceSettlementBacklog{Pending: 42, OldestAge: 1500 * time.Millisecond})
	worker := newGuardWorker(repo, 100, 10*time.Second)
	worker.applyBacklog(UsageBalanceSettlementBacklog{Pending: 500})
	ops := &OpsService{balanceSettlementWorker: worker}

	health := ops.GetLiveBalanceAdjustmentOutboxHealth(context.Background())
	require.NotNil(t, health.BalanceSettlement)
	require.Equal(t, int64(42), health.BalanceSettlement.Pending)
	require.Equal(t, int64(1500), health.BalanceSettlement.OldestAgeMs)
	require.True(t, health.BalanceSettlement.FallbackActive)

	raw, err := json.Marshal(health)
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))
	settlement, ok := decoded["balance_settlement"].(map[string]any)
	require.True(t, ok, "balance_settlement is part of the response: %s", raw)
	for _, key := range []string{"merge_enabled", "consumer_running", "pending", "oldest_age_ms", "max_backlog", "max_age_seconds",
		"fallback_active", "fallback_since", "fallback_transitions", "fallback_charges", "last_sample_at", "flushes", "events", "failures"} {
		require.Contains(t, settlement, key)
	}
	require.EqualValues(t, 42, settlement["pending"])
	require.Equal(t, true, settlement["fallback_active"])
	require.Contains(t, decoded, "running", "the existing live-balance-outbox fields are untouched")
}

// 不接结算 worker（或根本没有 ops 服务）时，响应和改动前一模一样。
func TestOpsHealthWithoutASettlementWorkerKeepsTheOldResponse(t *testing.T) {
	health := (&OpsService{}).GetLiveBalanceAdjustmentOutboxHealth(context.Background())
	require.Nil(t, health.BalanceSettlement)
	raw, err := json.Marshal(health)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "balance_settlement")

	var nilOps *OpsService
	require.Equal(t, LiveBalanceAdjustmentOutboxHealth{}, nilOps.GetLiveBalanceAdjustmentOutboxHealth(context.Background()))
}
