//go:build unit

package service

import (
	"context"
	"errors"
	"math"
	"regexp"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/stretchr/testify/require"
)

func TestPlanDebtClawback(t *testing.T) {
	cases := []struct {
		name   string
		credit float64
		debt   float64
		want   float64
	}{
		{name: "no debt", credit: 100, debt: 0, want: 0},
		{name: "no credit", credit: 0, debt: 10, want: 0},
		{name: "negative credit", credit: -5, debt: 10, want: 0},
		{name: "negative debt", credit: 5, debt: -10, want: 0},
		{name: "partial", credit: 10, debt: 50, want: 10},
		{name: "covers whole debt", credit: 100, debt: 3.5, want: 3.5},
		{name: "exact", credit: 2, debt: 2, want: 2},
		{name: "sub-cent debt", credit: 1, debt: 0.00000071, want: 0.00000071},
		{name: "sub-cent credit", credit: 0.00000001, debt: 5, want: 0.00000001},
		{name: "nan credit", credit: math.NaN(), debt: 5, want: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, planDebtClawback(tc.credit, tc.debt))
		})
	}

	require.LessOrEqual(t, planDebtClawback(0.123456789, 5), 0.123456789)
}

func TestParseDebtClawbackEnabledDefaultsOn(t *testing.T) {
	require.True(t, parseDebtClawbackEnabled(""), "missing value must keep clawback enabled")
	require.True(t, parseDebtClawbackEnabled("true"))
	require.True(t, parseDebtClawbackEnabled(" TRUE "))
	require.True(t, parseDebtClawbackEnabled("1"))
	require.True(t, parseDebtClawbackEnabled("false!"), "only the exact false token disables clawback")
	require.False(t, parseDebtClawbackEnabled("false"))
	require.False(t, parseDebtClawbackEnabled(" False "))
}

func TestPayDownUserDebtSkipsWhenStoreUnavailable(t *testing.T) {
	applied, err := payDownUserDebt(context.Background(), nil, 42, 10, DebtClawbackSourceRedeemCode, "code")
	require.NoError(t, err)
	require.Zero(t, applied)

	applied, err = payDownUserDebt(context.Background(), nil, 42, 0, DebtClawbackSourceAdminAdjust, "notes")
	require.NoError(t, err)
	require.Zero(t, applied)
}

func TestPayDownUserDebtReadsPolicyInCreditTransaction(t *testing.T) {
	for _, tc := range []struct {
		name        string
		value       *string
		queryError  bool
		rowError    bool
		wantApplied float64
	}{
		{name: "disabled", value: new("false")},
		{name: "default on when no row", wantApplied: 2},
		{name: "explicitly enabled", value: new("true"), wantApplied: 2},
		{name: "policy query failure", queryError: true},
		{name: "policy row failure", rowError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			db.SetMaxOpenConns(1)
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			mock.ExpectBegin()
			tx, err := client.Tx(ctx)
			require.NoError(t, err)
			defer func() { _ = tx.Rollback() }()
			readErr := errors.New("temporary settings read failure")
			setting := mock.ExpectQuery(regexp.QuoteMeta("SELECT value FROM settings WHERE key = $1")).WithArgs(SettingKeyDebtClawbackEnabled)
			if tc.queryError {
				setting.WillReturnError(readErr)
			} else {
				rows := sqlmock.NewRows([]string{"value"})
				if tc.value != nil {
					rows.AddRow(*tc.value)
				}
				if tc.rowError {
					rows.AddRow("true").RowError(0, readErr)
				}
				setting.WillReturnRows(rows)
			}
			if tc.wantApplied > 0 {
				mock.ExpectQuery(regexp.QuoteMeta("SELECT amount FROM user_debt WHERE user_id = $1 FOR UPDATE")).
					WithArgs(int64(42)).WillReturnRows(sqlmock.NewRows([]string{"amount"}).AddRow(2.0))
				mock.ExpectExec("UPDATE user_debt").WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectExec("INSERT INTO user_debt_clawbacks").
					WithArgs(int64(42), 2.0, 3.0, DebtClawbackSourceRedeemCode, "fixture").
					WillReturnResult(sqlmock.NewResult(1, 1))
			}
			applied, err := payDownUserDebt(dbent.NewTxContext(ctx, tx), client, 42, 3, DebtClawbackSourceRedeemCode, "fixture")
			if tc.queryError || tc.rowError {
				require.ErrorIs(t, err, readErr)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tc.wantApplied, applied)
			mock.ExpectRollback()
			require.NoError(t, tx.Rollback())
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestClampDebtClawbackSourceID(t *testing.T) {
	require.Equal(t, "admin-adjust", clampDebtClawbackSourceID("admin-adjust"))
	require.Equal(t, "", clampDebtClawbackSourceID(""))

	long := ""
	for i := 0; i < debtClawbackSourceIDMaxLen+20; i++ {
		long += "码"
	}
	clamped := clampDebtClawbackSourceID(long)
	require.Equal(t, debtClawbackSourceIDMaxLen, len([]rune(clamped)))
}
