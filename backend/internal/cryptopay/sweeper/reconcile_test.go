package sweeper

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

// Reconcile must never sign or broadcast: it only observes the chain and
// either advances a confirmed job or releases an address whose tx is gone.
func TestReconcileReleasesAbsentGasWithoutSigning(t *testing.T) {
	e, mock, chain, entry := setupExecutor(t)
	chain.mined = false // receipt is null -> the broadcast never landed

	mock.ExpectQuery(`SELECT pg_try_advisory_lock`).
		WithArgs(sweepLockKey).
		WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(true))
	mock.ExpectQuery(`SELECT id, chain, from_address`).
		WithArgs(int64(1)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "chain", "from_address", "to_address", "token", "raw_amount",
			"token_decimals", "derivation_index", "status", "error", "job_version", "revision",
			"confirmations_required", "gas_payer", "gas_amount", "gas_nonce", "gas_raw_tx",
			"gas_tx_hash", "gas_confirmed", "sweep_nonce", "sweep_raw_tx", "sweep_tx_hash",
		}).AddRow(1, "bsc", entry.Address, entry.Address, entry.Token, "1000000",
			18, 0, "gas_pending", "awaiting canonical receipt and required confirmations", 1, 0,
			"2", chain.payer, "97500", "0", "0xraw", "0xhash", false, "", "", ""))
	expectWrite(mock, "failed", nil, nil)
	mock.ExpectQuery(`SELECT pg_advisory_unlock`).
		WithArgs(sweepLockKey).
		WillReturnRows(sqlmock.NewRows([]string{"unlock"}).AddRow(true))

	out, err := e.Reconcile(t.Context(), 1)
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	if out.Action != ReconcileReleased || out.Status != "failed" {
		t.Fatalf("absent transaction must be released, got %+v", out)
	}
	chain.mu.Lock()
	defer chain.mu.Unlock()
	if len(chain.signs) != 0 || len(chain.raws) != 0 {
		t.Fatalf("reconcile must not sign or broadcast: signs=%d raws=%d", len(chain.signs), len(chain.raws))
	}
}

// A job whose reserved transaction already confirmed is advanced, not resent.
func TestReconcileAdvancesConfirmedGas(t *testing.T) {
	e, mock, chain, entry := setupExecutor(t)
	chain.mined = true
	chain.native = 1_000_000_000
	chain.txs["0xhash"] = true // the reserved gas tx is already on chain

	mock.ExpectQuery(`SELECT pg_try_advisory_lock`).
		WithArgs(sweepLockKey).
		WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(true))
	mock.ExpectQuery(`SELECT id, chain, from_address`).
		WithArgs(int64(2)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "chain", "from_address", "to_address", "token", "raw_amount",
			"token_decimals", "derivation_index", "status", "error", "job_version", "revision",
			"confirmations_required", "gas_payer", "gas_amount", "gas_nonce", "gas_raw_tx",
			"gas_tx_hash", "gas_confirmed", "sweep_nonce", "sweep_raw_tx", "sweep_tx_hash",
		}).AddRow(2, "bsc", entry.Address, entry.Address, entry.Token, "1000000",
			18, 0, "gas_pending", "", 1, 0,
			"2", chain.payer, "97500", "0", "0xraw", "0xhash", false, "", "", ""))
	expectWriteID(mock, 2, "gas_confirmed", nil, nil)
	mock.ExpectQuery(`SELECT pg_advisory_unlock`).
		WithArgs(sweepLockKey).
		WillReturnRows(sqlmock.NewRows([]string{"unlock"}).AddRow(true))

	out, err := e.Reconcile(t.Context(), 2)
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	if out.Action != ReconcileConfirmed || out.Status != "gas_confirmed" {
		t.Fatalf("confirmed gas must advance the job, got %+v", out)
	}
	chain.mu.Lock()
	defer chain.mu.Unlock()
	if len(chain.signs) != 0 || len(chain.raws) != 0 {
		t.Fatalf("reconcile must not sign or broadcast: signs=%d raws=%d", len(chain.signs), len(chain.raws))
	}
}
