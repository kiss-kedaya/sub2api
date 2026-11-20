//go:build unit

package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"testing"

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

func TestIsMissingDebtTableError(t *testing.T) {
	require.True(t, isMissingDebtTableError(errors.New(`pq: relation "user_debt" does not exist`)))
	require.True(t, isMissingDebtTableError(errors.New(`ERROR: relation "user_debt" does not exist (SQLSTATE 42P01)`)))
	require.True(t, isMissingDebtTableError(errors.New("no such table: user_debt")))
	require.False(t, isMissingDebtTableError(nil))
	require.False(t, isMissingDebtTableError(errors.New("duplicate key value violates unique constraint")))
	require.False(t, isMissingDebtTableError(fmt.Errorf("load debt: %w", errors.New("db is closed"))))
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
