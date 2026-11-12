package sweeper

import (
	"errors"
	"math/big"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

// These tests pin the gas funding failure modes: a resource wait must not
// consume the bounded attempt counter (which is what parked payable sweeps as
// `failed`), and the gas wallet must never sign a top-up it cannot pay for.

// TestGasFundingWaitDoesNotBurnAnAttempt covers every non-transient gas
// shortfall branch. Each one used to go through jobError, which spends a
// bounded attempt and finally marks a payable sweep as `failed`.
func TestGasFundingWaitDoesNotBurnAnAttempt(t *testing.T) {
	smallBudget := map[string]float64{"bsc": 1e-5} // 1e13 wei: pays one floored transfer, not 30 gwei
	cases := []struct {
		name        string
		native      int64
		payerNative int64
		budget      map[string]float64
		gasPrice    string
		askGasPayer bool
		want        string
	}{
		{
			name: "no gas budget configured", native: 0, payerNative: 1_000_000_000,
			budget: nil,
			want:   "sweep gas budget is not configured for bsc",
		},
		{
			name: "quote above configured budget", native: 0, payerNative: 1_000_000_000,
			budget: smallBudget, gasPrice: "0x6fc23ac00", // 30 gwei: above a 1e13 wei budget
			want: errGasQuoteExceedsBudget.Error(),
		},
		{
			name: "gas wallet cannot pay", native: 0, payerNative: 0,
			budget: map[string]float64{"bsc": 0.002}, askGasPayer: true,
			want: errGasPayerUnderfunded.Error(),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, m, c, entry := setupExecutor(t)
			c.native, c.payerNative = tc.native, tc.payerNative
			if tc.gasPrice != "" {
				c.gasPrice = tc.gasPrice
			}
			e.config.GasTopUp = tc.budget
			// Already at the terminal attempt bound: one more persisted failure
			// would mark this job `failed` forever.
			j := &sweepJob{PlanEntry: entry, ID: 1, Version: 1, Revision: maxSweepAttempts,
				Destination: testDestination, Status: "planned", Confirmations: 2}

			expectAddressFree(m, entry.Address, 1)
			if tc.askGasPayer {
				m.ExpectQuery(`SELECT EXISTS .*gas_payer`).WithArgs("bsc", int64(1), c.payer).
					WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
			}
			m.ExpectExec("UPDATE sweep_records SET error=").
				WithArgs(int64(1), tc.want, int64(maxSweepAttempts)).
				WillReturnResult(sqlmock.NewResult(0, 1))

			out, err := e.resume(t.Context(), j)
			if err != nil || out.Status != "pending" || out.Error != tc.want {
				t.Fatalf("out=%+v err=%v", out, err)
			}
			if j.Status != "planned" || j.Revision != maxSweepAttempts {
				t.Fatalf("deferral consumed an attempt: status=%q revision=%d", j.Status, j.Revision)
			}
			if len(c.signs) != 0 || len(c.raws) != 0 {
				t.Fatalf("unfundable gas was signed: signs=%+v sends=%d", c.signs, len(c.raws))
			}
		})
	}
}

// TestPrepareGasRefusesAZeroTopUp keeps the lowest-level guard honest: a zero
// payment must never be signed, whatever the caller computed.
func TestPrepareGasRefusesAZeroTopUp(t *testing.T) {
	e, _, c, entry := setupExecutor(t)
	j := &sweepJob{PlanEntry: entry, ID: 1, Version: 1, Destination: testDestination, Status: "planned"}
	err := e.prepareGas(t.Context(), e.pools["bsc"], j, big.NewInt(0), big.NewInt(1))
	if !errors.Is(err, errGasBudgetInsufficient) {
		t.Fatalf("zero top-up error = %v, want errGasBudgetInsufficient", err)
	}
	if len(c.signs) != 0 || len(c.nonceReads) != 0 {
		t.Fatalf("zero top-up read a nonce or signed: signs=%+v nonces=%+v", c.signs, c.nonceReads)
	}
	if j.GasRaw != "" || j.GasHash != "" {
		t.Fatalf("zero top-up left reserved bytes: %+v", j)
	}
}

// TestGasWaitReasonsSurviveTheRecordBoundary: the entries in Summary are built
// from job.outcome(), which sanitises every reason. If a sentence is not
// allowlisted the operator sees the generic payment sentence instead.
func TestGasWaitReasonsSurviveTheRecordBoundary(t *testing.T) {
	for _, text := range []string{
		errGasBudgetInsufficient.Error(),
		errGasPayerUnderfunded.Error(),
		errGasQuoteExceedsBudget.Error(),
		"sweep gas budget is not configured for bsc",
	} {
		if got := jobWithError(text).outcome().Error; got != text {
			t.Errorf("reason lost: %q -> %q", text, got)
		}
	}
}

func jobWithError(text string) *sweepJob {
	return &sweepJob{PlanEntry: PlanEntry{Chain: "bsc", Address: multiAddrAddress(99),
		Token: testToken, RawAmount: big.NewInt(1), Decimals: 6}, ID: 1, Version: 1,
		Destination: testDestination, Status: "planned", Error: text}
}
