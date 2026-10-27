//go:build integration

package repository

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type onceObserver interface {
	ObserveThreshold(context.Context, service.AccountOpsEvent, string, time.Time, bool, bool) error
}

func TestAccountOpsOnceThresholdCriteriaTransitionsNotify(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name, initialObservation, nextObservation, wantPhase string
	}{
		{name: "compliant to adverse", initialObservation: "resolved", nextObservation: "active", wantPhase: "alert"},
		{name: "adverse to compliant", initialObservation: "active", nextObservation: "resolved", wantPhase: "recovery"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var id int64
			require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO accounts(name,platform,type,status,schedulable) VALUES($1,'openai','apikey','active',true) RETURNING id`, "criteria-transition-"+tc.name).Scan(&id))
			defer cleanupOnce(t, id)
			repo := NewAccountOpsRepository(integrationDB).(*accountOpsRepository)
			now := time.Now().UTC().Truncate(time.Microsecond)
			e := service.AccountOpsEvent{AccountID: id, Kind: "balance_threshold", Identity: "same", Criteria: "old-rule"}
			require.NoError(t, repo.ObserveThreshold(ctx, e, tc.initialObservation, now, true, true))
			e.Criteria = "new-rule"
			require.NoError(t, repo.ObserveThreshold(ctx, e, tc.nextObservation, now.Add(time.Second), true, true))
			claimed, err := repo.Claim(ctx)
			require.NoError(t, err)
			require.NotNil(t, claimed)
			require.Equal(t, tc.wantPhase, claimed.Phase)
			require.Equal(t, "new-rule", claimed.Criteria)
			require.NoError(t, repo.Complete(ctx, claimed, "sent", time.Hour))
			require.NoError(t, repo.ObserveThreshold(ctx, e, tc.nextObservation, now.Add(2*time.Second), true, true))
			claimed, err = repo.Claim(ctx)
			require.NoError(t, err)
			require.Nil(t, claimed)
		})
	}
}

func TestAccountOpsOnceEpisodeConcurrentRestartRecovery(t *testing.T) {
	ctx := context.Background()
	var id int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO accounts(name,platform,type,status,schedulable) VALUES('once-fixture','openai','apikey','active',true) RETURNING id`).Scan(&id))
	defer func() {
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM account_ops_threshold_events WHERE account_id=$1`, id)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM account_ops_threshold_monitors WHERE account_id=$1`, id)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM accounts WHERE id=$1`, id)
	}()
	repo := NewAccountOpsRepository(integrationDB)
	observe, ok := repo.(onceObserver)
	require.True(t, ok, "durable episode observer required")
	e := service.AccountOpsEvent{AccountID: id, Kind: "balance_threshold", AccountName: "once-fixture", Identity: "same"}
	now := time.Now().UTC()
	require.NoError(t, observe.ObserveThreshold(ctx, e, "resolved", now.Add(-time.Second), true, true))
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- observe.ObserveThreshold(ctx, e, "active", now, true, true) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	count := func(want int) {
		var n int
		require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM account_ops_threshold_events WHERE account_id=$1`, id).Scan(&n))
		require.Equal(t, want, n)
	}
	count(1)
	restarted := NewAccountOpsRepository(integrationDB).(onceObserver)
	require.NoError(t, restarted.ObserveThreshold(ctx, e, "active", now.Add(2*time.Hour), true, true))
	count(1)
	require.NoError(t, restarted.ObserveThreshold(ctx, e, "resolved", now.Add(2*time.Hour+time.Second), true, true))
	count(1)
	require.NoError(t, restarted.ObserveThreshold(ctx, e, "resolved", now.Add(2*time.Hour+10*time.Second), true, true))
	count(1)
	require.NoError(t, restarted.ObserveThreshold(ctx, e, "unknown", now.Add(2*time.Hour+16*time.Second), true, true))
	count(1)
	require.NoError(t, restarted.ObserveThreshold(ctx, e, "resolved", now.Add(2*time.Hour+17*time.Second), true, true))
	count(1)
	require.NoError(t, restarted.ObserveThreshold(ctx, e, "resolved", now.Add(2*time.Hour+32*time.Second), true, true))
	count(2)
	require.NoError(t, restarted.ObserveThreshold(ctx, e, "active", now.Add(2*time.Hour+33*time.Second), true, true))
	count(3)
}

func TestAccountOpsOnceIndependentFlagsHistoryAndReceipts(t *testing.T) {
	ctx := context.Background()
	for _, flags := range [][2]bool{{true, true}, {false, true}, {true, false}, {false, false}} {
		t.Run(fmt.Sprint(flags), func(t *testing.T) {
			var id int64
			require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO accounts(name,platform,type,status,schedulable) VALUES('flags-fixture','openai','apikey','active',true) RETURNING id`).Scan(&id))
			defer cleanupOnce(t, id)
			repo := NewAccountOpsRepository(integrationDB).(*accountOpsRepository)
			now := time.Now().UTC().Truncate(time.Microsecond)
			amount, threshold := 4.0, 5.0
			e := service.AccountOpsEvent{AccountID: id, Kind: "balance_threshold", Identity: "same", Criteria: "criteria", Details: &service.AccountOpsDetails{Balance: &amount, Threshold: &threshold, Unit: "USD"}}
			require.NoError(t, repo.ObserveThreshold(ctx, e, "active", now, flags[0], flags[1]))
			claimed, err := repo.Claim(ctx)
			require.NoError(t, err)
			if flags[0] {
				require.NotNil(t, claimed)
				require.Equal(t, "alert", claimed.Phase)
				owned, err := repo.SaveDelivery(ctx, claimed, "email:fake", service.AccountOpsDelivery{Provider: "email", Status: "sent", Attempts: 1})
				require.NoError(t, err)
				require.True(t, owned)
				require.NoError(t, repo.Complete(ctx, claimed, "failed", 0))
				retry, err := repo.Claim(ctx)
				require.NoError(t, err)
				require.NotNil(t, retry)
				require.Equal(t, "sent", retry.Deliveries["email:fake"].Status)
				owned, err = repo.SaveDelivery(ctx, claimed, "email:fake", service.AccountOpsDelivery{Status: "failed"})
				require.NoError(t, err)
				require.False(t, owned)
				require.NoError(t, repo.Complete(ctx, retry, "sent", time.Hour))
			} else {
				require.Nil(t, claimed)
			}
			amount = 8
			require.NoError(t, repo.ObserveThreshold(ctx, e, "resolved", now.Add(time.Second), flags[0], flags[1]))
			require.NoError(t, repo.ObserveThreshold(ctx, e, "resolved", now.Add(16*time.Second), flags[0], flags[1]))
			recovered, err := repo.Claim(ctx)
			require.NoError(t, err)
			if flags[1] {
				require.NotNil(t, recovered)
				require.Equal(t, "recovery", recovered.Phase)
				require.Equal(t, 8.0, *recovered.Details.Balance)
				amount = 3
				require.NoError(t, repo.ObserveThreshold(ctx, e, "active", now.Add(17*time.Second), flags[0], flags[1]))
				valid, err := repo.ThresholdEventEligible(ctx, recovered)
				require.NoError(t, err)
				require.True(t, valid, "historical recovery remains valid after a new episode")
				require.NoError(t, repo.Complete(ctx, recovered, "sent", time.Hour))
			} else {
				require.Nil(t, recovered)
			}
			amount = 3
			require.NoError(t, repo.ObserveThreshold(ctx, e, "active", now.Add(17*time.Second), flags[0], flags[1]))
			items, err := repo.List(ctx, 0, 100)
			require.NoError(t, err)
			events := []service.AccountOpsEvent{}
			for _, item := range items {
				if item.AccountID == id {
					events = append(events, item)
				}
			}
			require.Len(t, events, 3)
			require.Equal(t, 3.0, *events[0].Details.Balance)
			require.Equal(t, 8.0, *events[1].Details.Balance)
			require.Equal(t, 4.0, *events[2].Details.Balance)
			require.NotEqual(t, events[0].EpisodeID, events[2].EpisodeID)
			require.Equal(t, events[1].EpisodeID, events[2].EpisodeID)
		})
	}
}
func cleanupOnce(t *testing.T, id int64) {
	t.Helper()
	for _, table := range []string{"account_ops_threshold_events", "account_ops_threshold_monitors", "account_ops_alerts"} {
		_, err := integrationDB.Exec(`DELETE FROM `+table+` WHERE account_id=$1`, id)
		require.NoError(t, err)
	}
	_, err := integrationDB.Exec(`DELETE FROM accounts WHERE id=$1`, id)
	require.NoError(t, err)
}
