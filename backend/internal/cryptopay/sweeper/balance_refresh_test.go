package sweeper

import (
	"context"
	"math/big"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

type currentTestReader struct {
	fakeReader
	current *AddressBalance
	nextRaw *big.Int
	reads   int
}

func (r *currentTestReader) CurrentBalance(context.Context, string, string, string) (*AddressBalance, error) {
	r.reads++
	copy := *r.current
	copy.Raw = new(big.Int).Set(r.current.Raw)
	if r.reads > 1 && r.nextRaw != nil {
		copy.Raw.Set(r.nextRaw)
	}
	return &copy, nil
}

func currentReaderFor(entry PlanEntry, raw int64) *currentTestReader {
	return &currentTestReader{current: &AddressBalance{Chain: entry.Chain,
		Address: entry.Address, Token: entry.Token, Decimals: entry.Decimals,
		DerivationIndex: entry.DerivationIndex, Raw: big.NewInt(raw)}}
}

func TestSweepUnsignedJobUsesDepositsCreditedAfterOldPlan(t *testing.T) {
	e, mock, chain, entry := setupExecutor(t)
	reader := currentReaderFor(entry, 12_000_000)
	e.reader = reader
	job := &sweepJob{PlanEntry: entry, ID: 1, Version: 1,
		Destination: testDestination, Status: "planned", Confirmations: 2}
	expectRunStart(mock, job)
	mock.ExpectExec("UPDATE sweep_records SET\\s+amount=").
		WithArgs(int64(1), "12", "12000000", int64(0)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	expectAddressClear(mock, entry)
	expectWrite(mock, "sweep_pending", nil, nil)
	expectWrite(mock, "swept", nil, nil)
	expectUnlock(mock)
	summary, err := e.Run(t.Context(), false)
	if err != nil || summary.Swept != 1 || summary.Pending != 0 {
		t.Fatalf("sweep failed: summary=%+v err=%v", summary, err)
	}
	if len(chain.signs) != 1 || chain.signs[0].Amount != "12000000" || reader.reads != 2 {
		t.Fatalf("old amount survived execution: signs=%+v balance reads=%d", chain.signs, reader.reads)
	}
}

func TestSweepRefreshNeverChangesSignedTokenTransaction(t *testing.T) {
	e, mock, chain, entry := setupExecutor(t)
	reader := currentReaderFor(entry, 12_000_000)
	e.reader = reader
	job := signedJob(chain, entry, false)
	chain.txs[job.SweepHash] = true
	expectRunStart(mock, job)
	expectWrite(mock, "swept", nil, nil)
	expectUnlock(mock)
	summary, err := e.Run(t.Context(), false)
	if err != nil || summary.Swept != 1 || reader.reads != 0 || len(chain.signs) != 0 {
		t.Fatalf("signed transfer was changed: summary=%+v reads=%d signs=%d err=%v", summary, reader.reads, len(chain.signs), err)
	}
}

func TestSweepRefreshIncludesDepositCreditedWhileGasWasConfirming(t *testing.T) {
	e, mock, chain, entry := setupExecutor(t)
	chain.native = 0
	reader := currentReaderFor(entry, 10_000_000)
	reader.nextRaw = big.NewInt(12_000_000)
	e.reader = reader
	expectCreate(mock, entry, nil, nil)
	expectAddressClear(mock, entry)
	mock.ExpectQuery(`SELECT EXISTS .*gas_payer`).WithArgs("bsc", int64(1), chain.payer).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	expectWrite(mock, "gas_pending", nil, nil)
	expectWrite(mock, "gas_confirmed", nil, nil)
	mock.ExpectExec("UPDATE sweep_records SET\\s+amount=").
		WithArgs(int64(1), "12", "12000000", int64(2)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	expectWrite(mock, "sweep_pending", nil, nil)
	expectWrite(mock, "swept", nil, nil)
	out, err := e.sweepOne(t.Context(), entry, false)
	if err != nil || out.Status != "swept" || out.Amount != "12000000" {
		t.Fatalf("new credited deposit was omitted: %+v %v", out, err)
	}
	if len(chain.signs) != 2 || chain.signs[0].Kind != "gas" || chain.signs[1].Amount != "12000000" {
		t.Fatalf("unexpected funding or token amount: %+v", chain.signs)
	}
}

func TestSweepCurrentBalanceCannotChangeAddressAssignment(t *testing.T) {
	for _, field := range []string{"address", "token", "index", "decimals", "below_minimum"} {
		t.Run(field, func(t *testing.T) {
			e, mock, chain, entry := setupExecutor(t)
			reader := currentReaderFor(entry, 12_000_000)
			switch field {
			case "address":
				reader.current.Address = testDestination
			case "token":
				reader.current.Token = testDestination
			case "index":
				reader.current.DerivationIndex++
			case "decimals":
				reader.current.Decimals++
			case "below_minimum":
				e.config.MinPerAddress = map[string]float64{"bsc": 1}
				reader.current.Raw = big.NewInt(500_000)
			}
			e.reader = reader
			expectCreate(mock, entry, nil, nil)
			expectWrite(mock, "planned", nil, nil)
			out, err := e.sweepOne(t.Context(), entry, false)
			if err == nil || out.Status == "swept" || len(chain.signs) != 0 || len(chain.raws) != 0 {
				t.Fatalf("changed %s authorized a transaction: %+v %v", field, out, err)
			}
		})
	}
}

func TestSweepAmountRevisionConflictCannotSignOrBroadcast(t *testing.T) {
	e, mock, chain, entry := setupExecutor(t)
	e.reader = currentReaderFor(entry, 12_000_000)
	job := &sweepJob{PlanEntry: entry, ID: 1, Version: 1,
		Destination: testDestination, Status: "planned", Confirmations: 2}
	expectRunStart(mock, job)
	mock.ExpectExec("UPDATE sweep_records SET\\s+amount=").
		WithArgs(int64(1), "12", "12000000", int64(0)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	expectUnlock(mock)
	summary, err := e.Run(t.Context(), false)
	if err == nil || summary.Pending != 1 || len(chain.signs) != 0 || len(chain.raws) != 0 {
		t.Fatalf("conflict authorized a transfer: summary=%+v signs=%d sends=%d err=%v", summary, len(chain.signs), len(chain.raws), err)
	}
}

func TestSweepManualDryRunDoesNotReserveOrSignBelowAutomaticThreshold(t *testing.T) {
	e, _, chain, entry := setupExecutor(t)
	e.config.GlobalThreshold = 1000
	e.config.MinPerAddress = map[string]float64{"bsc": 1}
	reader := currentReaderFor(entry, 10_000_000)
	reader.balances = map[string][]AddressBalance{"bsc": {*reader.current}}
	e.reader = reader
	for range 2 {
		summary, err := e.RunManualAndWait(t.Context(), true)
		if err != nil || summary.Planned != 1 || len(summary.Entries) != 1 || !summary.DryRun {
			t.Fatalf("manual preview used automatic threshold: %+v %v", summary, err)
		}
	}
	if len(chain.signs) != 0 || len(chain.raws) != 0 || e.config.GlobalThreshold != 1000 {
		t.Fatal("dry run mutated transactions or automatic scheduling policy")
	}
	plan, err := NewPlanner(e.db, e.reader, e.config, e.pools).BuildPlan(t.Context())
	if err != nil || len(plan.Entries) != 0 {
		t.Fatalf("automatic sweep lost its threshold: %+v %v", plan, err)
	}
}
