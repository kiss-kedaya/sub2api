package sweeper

import (
	"math/big"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestSweepWrongChainCannotConfirmPersistedTransaction(t *testing.T) {
	e, mock, c, entry := setupExecutor(t)
	j := signedJob(c, entry, false)
	c.chainID = "0x89" // valid receipt, but this endpoint is Polygon, not BSC
	c.txs[j.SweepHash] = true
	expectWrite(mock, "sweep_pending", nil, nil)
	out, err := e.resume(t.Context(), j)
	if err == nil || out.Status == "swept" || c.receiptReads != 0 || len(c.signs) != 0 || len(c.raws) != 0 {
		t.Fatalf("wrong-chain endpoint resolved/spent a BSC job: out=%+v err=%v receipts=%d", out, err, c.receiptReads)
	}
}

func TestRPCGasQuoteCannotIncreaseConfiguredFundingBudget(t *testing.T) {
	e, mock, c, entry := setupExecutor(t)
	c.native = 0
	c.gasPrice = "0xe8d4a51000" // 1000 gwei; former code funds 0.195 BNB, above 0.002
	j := &sweepJob{PlanEntry: entry, ID: 1, Version: 1, Destination: testDestination,
		Status: "planned", Confirmations: 2}
	expectAddressClear(mock, entry)
	// An over-budget quote is a funding wait: the record keeps the reason but
	// the bounded-attempt counter must not move, or 25 expensive runs would
	// park a payable sweep as `failed`.
	mock.ExpectExec("UPDATE sweep_records SET error=").
		WithArgs(int64(1), errGasQuoteExceedsBudget.Error(), int64(0)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	out, err := e.resume(t.Context(), j)
	if err != nil || out.Status != "pending" || out.Error != errGasQuoteExceedsBudget.Error() ||
		len(c.signs) != 0 || len(c.raws) != 0 {
		t.Fatalf("untrusted quote authorized excess funding: out=%+v err=%v signs=%+v", out, err, c.signs)
	}
	if j.Status != "planned" || j.Revision != 0 {
		t.Fatalf("deferral consumed an attempt: status=%q revision=%d", j.Status, j.Revision)
	}
}

func TestGasTopUpNeverExceedsConfiguredAmount(t *testing.T) {
	config := Config{GasTopUp: map[string]float64{"bsc": 0.002}}
	budget := new(big.Int).Exp(big.NewInt(10), big.NewInt(20), nil)
	if got := gasTopUpAmount(config, "bsc", budget, big.NewInt(0)); got.Sign() != 0 {
		t.Fatalf("untrusted price raised configured gas payment to %s", got)
	}
}
