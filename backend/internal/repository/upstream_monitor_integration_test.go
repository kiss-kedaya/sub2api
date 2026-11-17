//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUpstreamMonitorOverviewExecutesAllQueries(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	account := mustCreateAccount(t, integrationEntClient, &service.Account{
		Name: "000-upstream-overview",
		Extra: map[string]any{
			"upstream_billing_rate_sync_enabled": true,
			"upstream_billing_probe": map[string]any{
				"status": "ok", "received_at": now.Format(time.RFC3339), "synced_rate_multiplier": 0.7,
				"data": map[string]any{"effective_rate_multiplier": 0.75},
				"balance": map[string]any{
					"status": "failed", "received_at": now.Format(time.RFC3339), "last_error": "unavailable",
					"data": map[string]any{"remaining": 7.25, "unit": "MONITOR"},
				},
			},
		},
	})
	t.Cleanup(func() { require.NoError(t, integrationEntClient.Account.DeleteOneID(account.ID).Exec(ctx)) })
	repo := NewUpstreamMonitorRepository(integrationDB)
	overview, err := repo.Overview(ctx, now)
	require.NoError(t, err, "the real PostgreSQL parser must accept every overview query")
	require.GreaterOrEqual(t, overview.TotalAccounts, int64(1))
	require.GreaterOrEqual(t, overview.Failed, int64(1))
	currencyFound := false
	for _, currency := range overview.Currencies {
		if currency.Unit == "MONITOR" {
			currencyFound = true
			require.Equal(t, 7.25, currency.Total)
			require.EqualValues(t, 1, currency.Accounts)
		}
	}
	require.True(t, currencyFound)
	found := false
	for _, item := range overview.Attention {
		if item.AccountID == account.ID {
			found = true
			require.Equal(t, 7.25, *item.Balance)
			require.Equal(t, 0.75, *item.Rate)
			require.Equal(t, 0.7, *item.RateSynced)
			require.True(t, item.RateSyncEnabled, "attention rows must retain the explicit SELECT/Scan order")
		}
	}
	require.True(t, found)
}

func TestUpstreamMonitorRateChangesFractionalPercentage(t *testing.T) {
	ctx := context.Background()
	account := mustCreateAccount(t, integrationEntClient, &service.Account{Name: "upstream-fractional-rate"})
	t.Cleanup(func() { require.NoError(t, integrationEntClient.Account.DeleteOneID(account.ID).Exec(ctx)) })
	now := time.Now().UTC().Truncate(time.Microsecond)
	_, err := integrationDB.ExecContext(ctx, `
INSERT INTO upstream_rate_changes(account_id, old_rate, new_rate, changed_at)
VALUES ($1, 1, 1.006, $2), ($1, 1, 1.004, $2)`, account.ID, now)
	require.NoError(t, err)
	repo := NewUpstreamMonitorRepository(integrationDB)
	for _, step := range []struct {
		minPct float64
		total  int64
	}{{0, 2}, {0.5, 1}, {0.7, 0}} {
		items, total, err := repo.RateChanges(ctx, service.UpstreamRateFilter{
			AccountID: account.ID, From: now.Add(-time.Minute), MinPct: step.minPct, Page: 1, PageSize: 20,
		})
		require.NoError(t, err)
		require.Equal(t, step.total, total)
		require.Len(t, items, int(step.total))
	}
}

func TestUpstreamMonitorRecordAlertsCooldown(t *testing.T) {
	for _, kind := range []string{service.UpstreamAlertKindBalanceLow, service.UpstreamAlertKindRateJump} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			account := mustCreateAccount(t, integrationEntClient, &service.Account{Name: "upstream-alert-" + kind})
			t.Cleanup(func() {
				require.NoError(t, integrationEntClient.Account.DeleteOneID(account.ID).Exec(ctx))
			})
			repo := NewUpstreamMonitorRepository(integrationDB)
			now := time.Now().UTC().Truncate(time.Microsecond)
			balance, threshold, oldRate, newRate := 5.0, 10.0, 1.0, 1.5
			alert := service.UpstreamAlert{
				AccountID: account.ID,
				Kind:      kind,
				Level:     "warning",
				Balance:   &balance,
				Threshold: &threshold,
				Unit:      "USD",
				OldRate:   &oldRate,
				NewRate:   &newRate,
				Message:   "Upstream monitor test alert",
				CreatedAt: now,
			}
			for _, step := range []struct {
				after    time.Duration
				recorded int
				total    int64
			}{
				{after: 0, recorded: 1, total: 1},
				{after: time.Minute, recorded: 0, total: 1},
				{after: 7 * time.Hour, recorded: 1, total: 2},
			} {
				alert.CreatedAt = now.Add(step.after)
				recorded, err := repo.RecordAlerts(ctx, []service.UpstreamAlert{alert}, alert.CreatedAt.Add(-6*time.Hour))
				require.NoError(t, err)
				require.Equal(t, step.recorded, recorded)

				items, total, err := repo.Alerts(ctx, service.UpstreamAlertFilter{
					AccountID: account.ID, Kind: kind, From: now.Add(-time.Second), Page: 1, PageSize: 10,
				})
				require.NoError(t, err)
				require.Equal(t, step.total, total)
				require.Len(t, items, int(step.total))
				require.Equal(t, kind, items[0].Kind)
				require.Equal(t, alert.Level, items[0].Level)
				require.Equal(t, alert.Balance, items[0].Balance)
				require.Equal(t, alert.Threshold, items[0].Threshold)
				require.Equal(t, alert.Unit, items[0].Unit)
				require.Equal(t, alert.OldRate, items[0].OldRate)
				require.Equal(t, alert.NewRate, items[0].NewRate)
				require.Equal(t, alert.Message, items[0].Message)
			}
		})
	}
}
