package repository

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// Check the SQL sent by every CTE consumer, not just an isolated SQL fragment.
func TestUpstreamMonitorQueriesHaveDefinedUniqueColumns(t *testing.T) {
	var queries []string
	matcher := sqlmock.QueryMatcherFunc(func(_, actual string) error {
		queries = append(queries, actual)
		return nil
	})
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(matcher))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := NewUpstreamMonitorRepository(db)
	now := time.Now().UTC().Truncate(time.Second)
	accountRow := func() *sqlmock.Rows {
		return sqlmock.NewRows([]string{
			"id", "name", "notes", "platform", "type", "status",
			"balance_text", "unit", "unlimited_text", "balance_status", "balance_received_at", "balance_fresh_until", "balance_error",
			"rate_text", "rate_status", "rate_received_at", "rate_error", "rate_synced_text", "rate_sync_text",
			"last_sample_at", "threshold", "enabled", "balance_json",
		}).AddRow(7, "monitor", "notes", "openai", "apikey", "active",
			"7.25", "USD", "false", "failed", now.Format(time.RFC3339), now.Format(time.RFC3339), "unavailable",
			"0.75", "ok", now.Format(time.RFC3339), "", "0.7", "true", now, 10.0, true, "{}")
	}
	mock.ExpectQuery("overview").WillReturnRows(sqlmock.NewRows([]string{"total", "balance", "healthy", "failed", "unsupported", "stale", "rate"}).AddRow(1, 1, 0, 1, 0, 0, 1))
	mock.ExpectQuery("currencies").WillReturnRows(sqlmock.NewRows([]string{"unit", "total", "accounts"}).AddRow("USD", 7.25, 1))
	mock.ExpectQuery("attention").WillReturnRows(accountRow())
	mock.ExpectQuery("rate count").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery("last sample").WillReturnRows(sqlmock.NewRows([]string{"sampled_at"}).AddRow(now))
	mock.ExpectQuery("list count").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery("list").WillReturnRows(accountRow())
	mock.ExpectQuery("export").WillReturnRows(accountRow())

	overview, err := repo.Overview(context.Background(), now)
	require.NoError(t, err)
	items, total, err := repo.ListAccounts(context.Background(), service.UpstreamMonitorFilter{Page: 1, PageSize: 20})
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	exported, err := repo.ListAccountsForExport(context.Background(), service.UpstreamMonitorFilter{}, 10)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
	require.Len(t, queries, 8)
	require.Len(t, overview.Attention, 1)
	require.Equal(t, items, overview.Attention)
	require.Equal(t, items, exported)
	require.Equal(t, "notes", items[0].Notes)
	require.Equal(t, 7.25, *items[0].Balance)
	require.Equal(t, 0.75, *items[0].Rate)
	require.Equal(t, 0.7, *items[0].RateSynced)
	require.True(t, items[0].RateSyncEnabled)
	require.Equal(t, now, *items[0].LastSampledAt)
	require.Equal(t, 10.0, *items[0].AlertThreshold)
	require.True(t, items[0].AlertEnabled)

	shaped := regexp.MustCompile(`(?s)shaped AS \(\s*SELECT (.*?)\s+FROM (src|accounts)\b`)
	alias := regexp.MustCompile(`\bAS ([a-z_]+)$`)
	bare := regexp.MustCompile(`^[a-z_]+$`)
	expected := strings.Fields(`id name notes platform type status rate_sync_text balance_text unit unlimited_text balance_status balance_received_at balance_fresh_until balance_error rate_text rate_status rate_received_at rate_error rate_synced_text balance_json`)
	checked := 0
	for _, query := range queries {
		if !strings.HasPrefix(strings.TrimSpace(query), "WITH") {
			continue
		}
		checked++
		match := shaped.FindStringSubmatch(query)
		require.Len(t, match, 3, "full query must include shaped CTE: %s", query)
		seen := map[string]bool{}
		// These projections have no commas in identifiers or aliases. Function
		// argument fragments cannot match either a bare identifier or an alias.
		for _, part := range strings.Split(match[1], ",") {
			part = strings.TrimSpace(part)
			var name string
			if output := alias.FindStringSubmatch(part); output != nil {
				name = output[1]
			} else if bare.MatchString(part) {
				name = part
				if match[2] == "accounts" {
					require.Contains(t, strings.Fields("id name platform type status"), name, "accounts has no bare column %s", name)
				} else {
					require.Contains(t, strings.Fields("id name notes platform type status rate_sync_text"), name)
				}
			} else {
				continue
			}
			require.False(t, seen[name], "duplicate output %s in full query: %s", name, query)
			seen[name] = true
		}
		require.Len(t, seen, len(expected))
		for _, name := range expected {
			require.True(t, seen[name], "missing CTE output %s", name)
		}
		for _, ref := range regexp.MustCompile(`\bv\.([a-z_]+)`).FindAllStringSubmatch(query, -1) {
			require.True(t, seen[ref[1]] || ref[1] == "balance_num", "undefined projected column %s", ref[1])
		}
	}
	require.Equal(t, 6, checked, "overview, currencies, attention, list count, list and export must be checked")
}

func TestUpstreamMonitorRateChangesAcceptsFractionalPercentage(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	filter := service.UpstreamRateFilter{From: time.Now().UTC().Add(-time.Hour), AccountID: 7, MinPct: 0.5, Page: 1, PageSize: 20}
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM upstream_rate_changes[\s\S]+\$3::numeric = 0`).
		WithArgs(filter.From, filter.AccountID, filter.MinPct).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery(`SELECT c.id,[\s\S]+\$3::numeric = 0`).
		WithArgs(filter.From, filter.AccountID, filter.MinPct, filter.PageSize, 0).
		WillReturnRows(sqlmock.NewRows([]string{"id", "account_id", "name", "old_rate", "new_rate", "changed_at"}).
			AddRow(1, 7, "monitor", 1.0, 1.005, time.Now().UTC()))
	items, total, err := NewUpstreamMonitorRepository(db).RateChanges(context.Background(), filter)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, items, 1)
	require.InDelta(t, 0.5, items[0].ChangePct, 0.000001)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpstreamMonitorRecordAlertsUsesExplicitParameterTypes(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	now := time.Date(2026, time.November, 17, 0, 0, 0, 0, time.UTC)
	alert := service.UpstreamAlert{
		AccountID: 7,
		Kind:      service.UpstreamAlertKindBalanceLow,
		Level:     "warning",
		Unit:      "USD",
		Message:   "Low balance",
		CreatedAt: now,
	}
	cooldownSince := now.Add(-6 * time.Hour)
	mock.ExpectBegin()
	mock.ExpectExec(`
INSERT INTO upstream_monitor_alerts(account_id, kind, level, balance, threshold, unit, old_rate, new_rate, message, created_at)
SELECT $1::bigint, $2::varchar, $3, $4, $5, $6, $7, $8, $9, $10
WHERE NOT EXISTS (
  SELECT 1 FROM upstream_monitor_alerts
  WHERE account_id = $1::bigint AND kind = $2::varchar AND created_at >= $11::timestamptz
)`).WithArgs(alert.AccountID, alert.Kind, alert.Level, nil, nil, alert.Unit, nil, nil,
		alert.Message, alert.CreatedAt, cooldownSince).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	recorded, err := NewUpstreamMonitorRepository(db).RecordAlerts(context.Background(), []service.UpstreamAlert{alert}, cooldownSince)
	require.NoError(t, err)
	require.Equal(t, 1, recorded)
	require.NoError(t, mock.ExpectationsWereMet())
}
