package sweeper

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

// An absent receipt is not proof that a signed transaction can never land.
// Keep the reservation and exact signed bytes for the normal resume path.
func TestReconcileRetainsFailedSignedGasWhenReceiptIsAbsent(t *testing.T) {
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
			18, 0, "failed", "Payment operation failed; reconcile transaction status before retrying", 1, 0,
			"2", chain.payer, "97500", "0", "0xraw", "0xhash", false, "", "", ""))
	expectWrite(mock, "gas_pending", nil, nil)
	mock.ExpectQuery(`SELECT pg_advisory_unlock`).
		WithArgs(sweepLockKey).
		WillReturnRows(sqlmock.NewRows([]string{"unlock"}).AddRow(true))

	out, err := e.Reconcile(t.Context(), 1)
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	if out.Action != ReconcileObserved || out.Status != "gas_pending" {
		t.Fatalf("absent receipt must retain the signed transaction, got %+v", out)
	}
	if out.Message == "" {
		t.Fatal("missing explanation that the existing reservation is retained")
	}
	chain.mu.Lock()
	defer chain.mu.Unlock()
	if len(chain.signs) != 0 || len(chain.raws) != 0 {
		t.Fatalf("reconcile must not sign or broadcast: signs=%d raws=%d", len(chain.signs), len(chain.raws))
	}
}

func TestReconcileFailedSignedSweepWithConfirmedReceipt(t *testing.T) {
	e, mock, chain, entry := setupExecutor(t)
	chain.mined = true
	chain.txs["0xhash"] = true

	mock.ExpectQuery(`SELECT pg_try_advisory_lock`).
		WithArgs(sweepLockKey).
		WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(true))
	mock.ExpectQuery(`SELECT id, chain, from_address`).
		WithArgs(int64(3)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "chain", "from_address", "to_address", "token", "raw_amount",
			"token_decimals", "derivation_index", "status", "error", "job_version", "revision",
			"confirmations_required", "gas_payer", "gas_amount", "gas_nonce", "gas_raw_tx",
			"gas_tx_hash", "gas_confirmed", "sweep_nonce", "sweep_raw_tx", "sweep_tx_hash",
		}).AddRow(3, "bsc", entry.Address, testDestination, entry.Token, "1000000",
			6, 1, "failed", "Payment operation failed; reconcile transaction status before retrying", 1, 0,
			"2", chain.payer, "0", "", "", "", false, "0", "0xraw", "0xhash"))
	expectWriteID(mock, 3, "swept", nil, nil)
	mock.ExpectQuery(`SELECT pg_advisory_unlock`).
		WithArgs(sweepLockKey).
		WillReturnRows(sqlmock.NewRows([]string{"unlock"}).AddRow(true))

	out, err := e.Reconcile(t.Context(), 3)
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	if out.Action != ReconcileConfirmed || out.Status != "swept" {
		t.Fatalf("confirmed failed job must be marked swept, got %+v", out)
	}
	chain.mu.Lock()
	defer chain.mu.Unlock()
	if len(chain.signs) != 0 || len(chain.raws) != 0 {
		t.Fatalf("reconcile must not sign or broadcast: signs=%d broadcasts=%d", len(chain.signs), len(chain.raws))
	}
}

func TestListBlockedReturnsFailedSignedTransactionsForReconciliation(t *testing.T) {
	e, mock, _, entry := setupExecutor(t)
	mock.ExpectQuery(`(?s)SELECT id, chain, from_address, status, error,.*status <> 'swept'.*status <> 'failed'.*gas_tx_hash`).
		WithArgs(50).
		WillReturnRows(sqlmock.NewRows([]string{"id", "chain", "from_address", "status", "error", "age_minutes"}).
			AddRow(49, "bsc", entry.Address, "failed", "Payment operation failed; reconcile transaction status before retrying", int64(12)))

	blocked, err := e.ListBlocked(t.Context(), 50)
	if err != nil {
		t.Fatalf("list blocked sweep jobs: %v", err)
	}
	if len(blocked) != 1 || blocked[0].JobID != 49 || !blocked[0].CanResolve || blocked[0].Status != "failed" {
		t.Fatalf("failed signed job was not surfaced for reconciliation: %+v", blocked)
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
