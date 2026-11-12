package sweeper

import (
	"database/sql/driver"
	"math/big"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

// expectSweepRewrite expects the write that stores re-signed sweep bytes: still
// sweep_pending at the same nonce, with raw bytes that differ from the dead ones.
func expectSweepRewrite(m sqlmock.Sqlmock, oldRaw string, rewritten *atomic.Bool) {
	args := []driver.Value{int64(1), "sweep_pending", sqlmock.AnyArg(), "2"}
	for i := 4; i < 11; i++ {
		args = append(args, sqlmock.AnyArg())
	}
	args = append(args, captureArg{func(v driver.Value) bool {
		raw, ok := v.(string)
		if ok && strings.HasPrefix(raw, "0x02") && raw != oldRaw {
			rewritten.Store(true)
			return true
		}
		return false
	}})
	for i := 12; i < 14; i++ {
		args = append(args, sqlmock.AnyArg())
	}
	m.ExpectExec("UPDATE sweep_records SET").WithArgs(args...).WillReturnResult(sqlmock.NewResult(0, 1))
}

// TestUnderpricedSweepIsReplacedAtTheSameNonce replays the 2026-11-13 BSC
// incident: a sweep signed at 1 wei cannot be mined, so the retry must replace
// it at the same nonce with a mineable fee — not replay the dead bytes and not
// release the record while its nonce still blocks the address.
func TestUnderpricedSweepIsReplacedAtTheSameNonce(t *testing.T) {
	e, m, c, entry := setupExecutor(t)
	j := signedJobAtPrice(c, entry, false, "1")
	oldRaw, oldHash := j.SweepRaw, j.SweepHash
	var rewritten atomic.Bool
	c.beforeSend = func() {
		if !rewritten.Load() {
			t.Error("a replacement was broadcast before it was stored")
		}
	}
	expectRunStart(m, j)
	expectSweepRewrite(m, oldRaw, &rewritten)
	expectWrite(m, "swept", nil, nil)
	expectUnlock(m)
	summary, err := e.Run(t.Context(), false)
	if err != nil || summary.Swept != 1 {
		t.Fatalf("summary=%+v err=%v", summary, err)
	}
	if len(c.signs) != 1 || c.signs[0].Kind != "sweep" || c.signs[0].Nonce != 3 ||
		c.signs[0].GasPrice != chainGasPriceFloor("bsc").String() ||
		c.signs[0].Amount != "10000000" || c.signs[0].To != testDestination {
		t.Fatalf("the replacement must keep nonce, destination and amount and raise only the fee: %+v", c.signs)
	}
	if len(c.raws) != 1 || c.raws[0] == oldRaw || rawHash(c.raws[0]) == oldHash {
		t.Fatalf("the dead bytes were sent again: %+v", c.raws)
	}
}

// TestReleasedUnderpricedRecordLetsTheNextSweepReuseItsNonce covers a record
// released before this fix: the dead transaction is still in a pool, so the
// address's pending nonce is past it. Signing there would queue behind it
// forever; the new payment takes the nonce the released record left dead.
func TestReleasedUnderpricedRecordLetsTheNextSweepReuseItsNonce(t *testing.T) {
	e, m, c, entry := setupExecutor(t)
	c.childNonce, c.childPending = "0x0", "0x1"
	dead := c.signed(EVMSignRequest{Index: 42, Kind: "sweep", ChainID: 56, Token: entry.Token,
		To: testDestination, Amount: "10000000", Nonce: 0, GasPrice: "1", GasLimit: transferGasLimit})
	expectCreate(m, entry, nil, nil)
	expectAddressClear(m, entry)
	m.ExpectQuery(`SELECT sweep_raw_tx FROM sweep_records`).
		WithArgs("bsc", entry.Address, "0", staleReleasePrefix+"%").
		WillReturnRows(sqlmock.NewRows([]string{"sweep_raw_tx"}).AddRow(dead))
	expectWrite(m, "sweep_pending", nil, nil)
	expectWrite(m, "swept", nil, nil)
	out, err := e.sweepOne(t.Context(), entry, false)
	if err != nil || out.Status != "swept" {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	if len(c.signs) != 1 || c.signs[0].Kind != "sweep" || c.signs[0].Nonce != 0 ||
		c.signs[0].GasPrice != chainGasPriceFloor("bsc").String() {
		t.Fatalf("the new sweep must take the dead nonce with a mineable fee: %+v", c.signs)
	}
	if len(c.raws) != 1 || c.raws[0] == dead {
		t.Fatalf("the new payment must not replay the dead bytes: %+v", c.raws)
	}
}

// TestSweepWithAHealthyFeeIsNotReplaced: a payment at or above today's quote is
// landable, and replacing it would only spend the address's balance.
func TestSweepWithAHealthyFeeIsNotReplaced(t *testing.T) {
	e, m, c, entry := setupExecutor(t)
	j := signedJob(c, entry, false) // signed at the chain floor
	expected := j.SweepHash
	expectRunStart(m, j)
	expectWrite(m, "swept", nil, nil)
	expectUnlock(m)
	summary, err := e.Run(t.Context(), false)
	if err != nil || summary.Swept != 1 {
		t.Fatalf("summary=%+v err=%v", summary, err)
	}
	if len(c.signs) != 0 {
		t.Fatalf("nothing new may be signed over a landable payment: %+v", c.signs)
	}
	if len(c.raws) != 1 || rawHash(c.raws[0]) != expected {
		t.Fatalf("the persisted bytes were not the ones broadcast: %+v", c.raws)
	}
}

// TestGasBudgetBelowTheChainFloorIsNamed guards the configuration defect the
// floor exposes: a budget that cannot pay one transfer at the chain minimum is
// reported as exactly that, and it is the relationship (limit x floor < cap)
// that decides it.
func TestGasBudgetBelowTheChainFloorIsNamed(t *testing.T) {
	config := Config{HotWalletEVM: testDestination, Confirmations: map[string]uint64{"bsc": 2},
		GasTopUp: map[string]float64{"bsc": 1e-6}} // 1e12 wei: below one floored transfer
	err := GasBudgetCoversGasFloor(config, "bsc")
	if err == nil || !strings.Contains(err.Error(), "gas_topup") {
		t.Fatalf("a budget below the chain floor must be named: %v", err)
	}
	if !strings.Contains(err.Error(), chainGasPriceFloor("bsc").String()) {
		t.Fatalf("the report must carry the floor it compared against: %v", err)
	}
	// A missing budget is a different state, handled where gas is prepared.
	missing := Config{HotWalletEVM: testDestination, Confirmations: map[string]uint64{"bsc": 2}}
	if err := GasBudgetCoversGasFloor(missing, "bsc"); err != nil {
		t.Fatalf("a missing budget is not this defect: %v", err)
	}
	// The live BSC budget covers the floor: 0.00001 BNB = 1e13 wei.
	live := Config{GasTopUp: map[string]float64{"bsc": 1e-5}}
	budget := configuredGasBudget(live, "bsc")
	if want := big.NewInt(10_000_000_000_000); budget.Cmp(want) != 0 {
		t.Fatalf("0.00001 BNB parsed as %s, want %s", budget, want)
	}
	floor := chainGasPriceFloor("bsc")
	legacy := new(big.Int).Mul(floor, big.NewInt(legacyTransferGasLimit))
	if legacy.Cmp(budget) >= 0 {
		t.Fatalf("65000 gas at the BSC floor costs %s wei, budget is %s", legacy, budget)
	}
	if need := transferGasBudget(floor); need.Cmp(budget) >= 0 {
		t.Fatalf("one buffered transfer at the BSC floor costs %s wei, budget is %s", need, budget)
	}
	if err := GasBudgetCoversGasFloor(live, "bsc"); err != nil {
		t.Fatalf("the live BSC budget must cover the floor: %v", err)
	}
}
