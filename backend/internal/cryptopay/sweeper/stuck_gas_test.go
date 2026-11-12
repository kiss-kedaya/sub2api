package sweeper

import (
	"context"
	"database/sql/driver"
	"errors"
	"math/big"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
)

// expectGasRewrite expects the write that stores re-signed gas bytes: still
// gas_pending at nonce 7, with raw bytes that differ from the earlier ones.
func expectGasRewrite(m sqlmock.Sqlmock, oldRaw string, rewritten *atomic.Bool) {
	args := []driver.Value{int64(1), "gas_pending", sqlmock.AnyArg(), "2", sqlmock.AnyArg(), sqlmock.AnyArg(), "7",
		captureArg{func(v driver.Value) bool {
			raw, ok := v.(string)
			if ok && strings.HasPrefix(raw, "0x02") && raw != oldRaw {
				rewritten.Store(true)
				return true
			}
			return false
		}}}
	for i := 8; i < 14; i++ {
		args = append(args, sqlmock.AnyArg())
	}
	m.ExpectExec("UPDATE sweep_records SET").WithArgs(args...).WillReturnResult(sqlmock.NewResult(0, 1))
}

func TestStuckUnderpricedGasTopUpIsReplacedAtTheSameNonce(t *testing.T) {
	e, m, c, entry := setupExecutor(t)
	c.gasPrice = "0x64" // 100 wei today; the top-up signed earlier carries a fee cap of 1 wei
	j := signedJobAtPrice(c, entry, true, "1")
	oldRaw, oldHash := j.GasRaw, j.GasHash
	var rewritten atomic.Bool
	c.beforeSend = func() {
		if !rewritten.Load() {
			t.Error("a replacement was sent before it was stored")
		}
	}
	expectRunStart(m, j)
	expectGasRewrite(m, oldRaw, &rewritten)
	expectWrite(m, "gas_confirmed", nil, nil)
	expectAddressClear(m, entry)
	expectWrite(m, "sweep_pending", nil, nil)
	expectWrite(m, "swept", nil, nil)
	expectUnlock(m)
	summary, err := e.Run(t.Context(), false)
	if err != nil || summary.Swept != 1 {
		t.Fatalf("summary=%+v err=%v", summary, err)
	}
	if len(c.signs) != 2 || c.signs[0].Kind != "gas" || c.signs[0].Nonce != 7 || c.signs[0].GasPrice != chainGasPriceFloor("bsc").String() ||
		c.signs[0].Amount != "195000" || c.signs[1].Kind != "sweep" {
		t.Fatalf("replacement must keep nonce and amount and raise only the fee: %+v", c.signs)
	}
	if len(c.raws) != 2 {
		t.Fatalf("want the replacement and the sweep to be sent, got %d sends", len(c.raws))
	}
	for _, raw := range c.raws {
		if raw == oldRaw || rawHash(raw) == oldHash {
			t.Fatal("the old underpriced bytes were sent again")
		}
	}
}

func TestSupersededGasTopUpIsReleasedAndFundedAgain(t *testing.T) {
	e, m, c, entry := setupExecutor(t)
	c.native = 0
	c.payerNonce = "0x9" // the payer has mined two more transactions since nonce 7 was reserved
	j := signedJob(c, entry, true)
	oldRaw := j.GasRaw
	expectRunStart(m, j)
	m.ExpectExec("UPDATE sweep_records SET").WithArgs(int64(1), "planned", supersededGasError, "2",
		"", "", "", "", "", false, "", "", "", sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	expectAddressClear(m, entry)
	m.ExpectQuery(`SELECT EXISTS .*gas_payer`).WithArgs("bsc", int64(1), c.payer).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	expectWrite(m, "gas_pending", nil, nil)
	expectWrite(m, "gas_confirmed", nil, nil)
	expectWrite(m, "sweep_pending", nil, nil)
	expectWrite(m, "swept", nil, nil)
	expectUnlock(m)
	summary, err := e.Run(t.Context(), false)
	if err != nil || summary.Swept != 1 {
		t.Fatalf("summary=%+v err=%v", summary, err)
	}
	if len(c.signs) != 2 || c.signs[0].Kind != "gas" || c.signs[0].Nonce != 9 || c.signs[1].Kind != "sweep" {
		t.Fatalf("the new top-up must use the payer's current nonce: %+v", c.signs)
	}
	for _, raw := range c.raws {
		if raw == oldRaw {
			t.Fatal("the dead top-up was sent again")
		}
	}
}

func TestStuckGasTopUpIsLeftAloneWhenReplacingItWouldExceedTheBudget(t *testing.T) {
	e, m, c, entry := setupExecutor(t)
	c.native, c.mined = 0, false
	c.gasPrice = "0x6fc23ac00" // 30 gwei: the buffered transfer cost is above the 0.002 funding cap
	j := signedJob(c, entry, true)
	expectRunStart(m, j)
	expectWrite(m, "gas_pending", nil, nil)
	expectUnlock(m)
	summary, err := e.Run(t.Context(), false)
	if err != nil || summary.Pending != 1 {
		t.Fatalf("summary=%+v err=%v", summary, err)
	}
	if len(c.signs) != 0 || len(c.raws) != 1 || c.raws[0] != j.GasRaw {
		t.Fatalf("nothing new may be signed over the budget: signs=%+v sends=%d", c.signs, len(c.raws))
	}
}

func TestStuckGasTopUpIsNotReplacedWithoutAuthorization(t *testing.T) {
	e, m, c, entry := setupExecutor(t)
	c.gasPrice = "0x64"
	fixture, ok := e.wallet.(*fixtureWallet)
	if !ok {
		t.Fatal("unexpected wallet fixture")
	}
	e.wallet = denySigningWallet{fixture}
	j := signedJobAtPrice(c, entry, true, "1")
	expectRunStart(m, j)
	expectUnlock(m)
	summary, err := e.Run(t.Context(), false)
	if err == nil || summary.Pending != 1 || len(c.raws) != 0 {
		t.Fatalf("summary=%+v err=%v sends=%d", summary, err, len(c.raws))
	}
	if got := summary.Entries[0].Error; got != "wallet signing is disabled; no new broadcast was sent" {
		t.Fatalf("operator is told: %q", got)
	}
}

type denySigningWallet struct{ *fixtureWallet }

func (denySigningWallet) SignEVM(context.Context, EVMSignRequest) (string, error) {
	return "", errors.New("wallet signing is disabled; no new broadcast was sent")
}

func TestSendingAnAlreadyKnownTransactionIsNotAFailure(t *testing.T) {
	e, m, c, entry := setupExecutor(t)
	c.native, c.mined = 0, false
	c.sendError = "already known"
	j := signedJob(c, entry, true)
	expectRunStart(m, j)
	expectWrite(m, "gas_pending", nil, nil)
	expectUnlock(m)
	summary, err := e.Run(t.Context(), false)
	if err != nil || summary.Pending != 1 || len(c.raws) != 1 {
		t.Fatalf("summary=%+v err=%v sends=%d", summary, err, len(c.raws))
	}
	if got := summary.Entries[0].Error; got != "awaiting canonical receipt and required confirmations" {
		t.Fatalf("a transaction the node already holds is just waiting, got %q", got)
	}
}

func TestARealRejectionKeepsItsReason(t *testing.T) {
	e, m, c, entry := setupExecutor(t)
	c.native, c.mined = 0, false
	c.sendError = "replacement transaction underpriced"
	j := signedJob(c, entry, true)
	expectRunStart(m, j)
	expectWrite(m, "gas_pending", nil, nil)
	expectUnlock(m)
	summary, err := e.Run(t.Context(), false)
	want := "RPC node rejected request (code -32000): replacement transaction underpriced"
	if err == nil || summary.Pending != 1 || summary.Entries[0].Error != want {
		t.Fatalf("summary=%+v err=%v", summary, err)
	}
}

func TestGivingUpKeepsTheRealReason(t *testing.T) {
	e, m, c, entry := setupExecutor(t)
	c.live = 9_000_000 // less than the planned 10_000_000
	j := &sweepJob{PlanEntry: entry, ID: 1, Version: 1, Revision: maxSweepAttempts,
		Destination: testDestination, Status: "planned", Confirmations: 2}
	expectRunStart(m, j)
	want := "live balance 9000000 is below planned amount 10000000 (gave up after 25 attempts)"
	args := []driver.Value{int64(1), "failed", want, "2"}
	for i := 4; i < 14; i++ {
		args = append(args, sqlmock.AnyArg())
	}
	m.ExpectExec("UPDATE sweep_records SET").WithArgs(args...).WillReturnResult(sqlmock.NewResult(0, 1))
	expectUnlock(m)
	summary, err := e.Run(t.Context(), false)
	if err == nil || len(summary.Entries) != 1 || !strings.Contains(summary.Entries[0].Error, "below planned amount") {
		t.Fatalf("summary=%+v err=%v", summary, err)
	}
	_ = c
}

func TestSupersededGasMessageIsOneTheRecordKeeps(t *testing.T) {
	// saveJob runs every error through rpc.SafeRecordedError; if the two sentences
	// drift apart the stored reason silently becomes the generic one.
	if got := rpc.SafeRecordedError(supersededGasError); got != supersededGasError {
		t.Fatalf("stored as %q", got)
	}
}

func TestReplacementFeeIsAlwaysAValidBump(t *testing.T) {
	cases := []struct{ old, quote, want int64 }{
		{1, 100, 100},      // far below today's price: use today's price
		{1000, 1010, 1126}, // the quote is only 1% higher: the node still wants 12.5%
		{8, 9, 10},
		{1, 1, 2},
	}
	for _, tc := range cases {
		if got := replacementPrice(big.NewInt(tc.old), big.NewInt(tc.quote)); got.Cmp(big.NewInt(tc.want)) != 0 {
			t.Fatalf("old=%d quote=%d: got %s want %d", tc.old, tc.quote, got, tc.want)
		}
	}
	// A node refuses a replacement below old*1.10, whatever the quote.
	for old := int64(1); old <= 5000; old++ {
		got := replacementPrice(big.NewInt(old), big.NewInt(1))
		threshold := new(big.Int).Div(new(big.Int).Mul(big.NewInt(old), big.NewInt(110)), big.NewInt(100))
		if got.Cmp(threshold) <= 0 {
			t.Fatalf("old=%d: %s does not clear the %s a node requires", old, got, threshold)
		}
	}
}
