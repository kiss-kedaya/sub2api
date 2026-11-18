package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestR4BalanceBecomingReadableAlertsOncePerEpisode(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := &accountOpsRepository{db: db}
	now := time.Now().UTC()
	for _, tc := range []struct {
		state   string
		adverse bool
		insert  bool
	}{
		{"unknown", false, false},
		{"active", false, true},
		{"active", true, false},
	} {
		mock.ExpectBegin()
		mock.ExpectExec(`INSERT INTO account_ops_threshold_monitors`).WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectQuery(`SELECT criteria,identity,episode_id,adverse,eligible,healthy_since,checked_at`).WillReturnRows(sqlmock.NewRows([]string{"criteria", "identity", "episode_id", "adverse", "eligible", "healthy_since", "checked_at"}).AddRow("synthetic-criteria", "manual-r4-wallet", "synthetic-episode", tc.adverse, false, nil, nil))
		mock.ExpectExec(`UPDATE account_ops_threshold_monitors SET`).WillReturnResult(sqlmock.NewResult(0, 1))
		if tc.insert {
			mock.ExpectExec(`INSERT INTO account_ops_threshold_events`).WillReturnResult(sqlmock.NewResult(0, 1))
		}
		mock.ExpectCommit()
		event := service.AccountOpsEvent{AccountID: 9101, GroupID: "manual-r4-wallet", Identity: "manual-r4-wallet", Criteria: "synthetic-criteria", Kind: "balance_threshold"}
		require.NoError(t, repo.ObserveThreshold(context.Background(), event, tc.state, now, true, true))
		now = now.Add(time.Minute)
	}
	require.NoError(t, mock.ExpectationsWereMet())
}
