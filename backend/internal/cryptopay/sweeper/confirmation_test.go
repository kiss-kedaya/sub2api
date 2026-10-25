package sweeper

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
)

func TestSweepWaitContinuesGasAndTokenInOneOperation(t *testing.T) {
	e, mock, chain, entry := setupExecutor(t)
	e.confirmationDeadline = time.Now().Add(2 * time.Second)
	e.confirmationInterval = time.Millisecond
	chain.native, chain.mined = 0, false
	// Each broadcast initially has no receipt. It mines after that first
	// observation, so neither gas nor token can finish in a one-pass run.
	chain.beforeSend = func() {
		chain.mined = false
		go func() {
			time.Sleep(10 * time.Millisecond)
			chain.mu.Lock()
			defer chain.mu.Unlock()
			chain.mined, chain.native = true, 1_000_000_000
		}()
	}
	expectCreate(mock, entry, nil, nil)
	expectAddressClear(mock, entry)
	mock.ExpectQuery(`SELECT EXISTS .*gas_payer`).WithArgs("bsc", int64(1), chain.payer).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	expectWrite(mock, "gas_pending", nil, nil)
	expectWrite(mock, "gas_confirmed", nil, nil)
	expectWrite(mock, "sweep_pending", nil, nil)
	expectWrite(mock, "swept", nil, nil)
	out, err := e.sweepOne(t.Context(), entry, false)
	if err != nil || out.Status != "swept" || out.GasTxHash == "" || out.SweepTxHash == "" {
		t.Fatalf("gas/token operation did not finish: %+v %v", out, err)
	}
	chain.mu.Lock()
	defer chain.mu.Unlock()
	if len(chain.signs) != 2 || len(chain.raws) != 2 || chain.signs[0].Kind != "gas" || chain.signs[1].Kind != "sweep" {
		t.Fatalf("unexpected replacement or repeated funding: signs=%+v broadcasts=%d", chain.signs, len(chain.raws))
	}
}

func TestSweepWaitBudgetReturnsPendingAndResumesIdenticalJob(t *testing.T) {
	e, mock, chain, entry := setupExecutor(t)
	e.confirmationDeadline = time.Now().Add(15 * time.Millisecond)
	e.confirmationInterval = time.Millisecond
	chain.mined = false
	job := signedJob(chain, entry, false)
	expectWrite(mock, "sweep_pending", nil, nil)
	out, err := e.resume(t.Context(), job)
	if err != nil || out.Status != "pending" || out.SweepTxHash != job.SweepHash {
		t.Fatalf("wait budget lost the durable pending result: %+v %v", out, err)
	}
	chain.mu.Lock()
	if len(chain.raws) != 1 || len(chain.signs) != 0 || chain.raws[0] != job.SweepRaw {
		t.Errorf("polling signed or rebroadcast the transaction: signs=%d broadcasts=%d", len(chain.signs), len(chain.raws))
	}
	chain.mined = true
	chain.mu.Unlock()
	expectWrite(mock, "swept", nil, nil)
	out, err = e.resume(t.Context(), job)
	if err != nil || out.Status != "swept" {
		t.Fatalf("confirmed job did not resume: %+v %v", out, err)
	}
	chain.mu.Lock()
	defer chain.mu.Unlock()
	if len(chain.raws) != 1 || len(chain.signs) != 0 {
		t.Fatal("confirmed recovery sent another transaction")
	}
}

func TestSweepGasPayerPendingIsADeferredResult(t *testing.T) {
	e, mock, chain, entry := setupExecutor(t)
	chain.native = 0
	expectCreate(mock, entry, nil, nil)
	expectAddressClear(mock, entry)
	mock.ExpectQuery(`SELECT EXISTS .*gas_payer`).WithArgs("bsc", int64(1), chain.payer).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	expectWrite(mock, "planned", nil, nil)
	out, err := e.sweepOne(t.Context(), entry, false)
	if err != nil || out.Status != "pending" || out.Error != "awaiting gas payer transaction confirmation" {
		t.Fatalf("gas nonce deferral was reported as a failed transfer: %+v %v", out, err)
	}
	chain.mu.Lock()
	defer chain.mu.Unlock()
	if len(chain.raws) != 0 || len(chain.signs) != 0 {
		t.Fatal("deferred gas funding spent money")
	}
}

func TestSweepWaitKeepsCallerCancellationAndRPCFailures(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "RPC failure", true: "canceled"}[canceled], func(t *testing.T) {
			e, _, chain, entry := setupExecutor(t)
			e.confirmationDeadline = time.Now().Add(time.Second)
			e.confirmationInterval = time.Millisecond
			chain.receiptError = true
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if canceled {
				cancel()
			}
			state, err := e.waitForReceipt(ctx, e.pools[entry.Chain], "0x123", 2, receiptWaiting)
			if err == nil || state != receiptWaiting || (canceled && !errors.Is(err, context.Canceled)) {
				t.Fatalf("unsafe wait result: state=%v err=%v", state, err)
			}
			if !canceled && rpc.SafeError(err) != "RPC HTTP request failed (status 503)" {
				t.Fatalf("RPC failure was hidden: %v", err)
			}
			chain.mu.Lock()
			defer chain.mu.Unlock()
			if len(chain.raws) != 0 || len(chain.signs) != 0 || (canceled && chain.receiptReads != 0) {
				t.Fatal("receipt waiting performed an unauthorized action")
			}
		})
	}
}
