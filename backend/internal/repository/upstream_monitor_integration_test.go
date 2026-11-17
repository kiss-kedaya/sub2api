//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

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
