package sweeper

import (
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
)

func sweptAuditRows(id int64, entry PlanEntry) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "chain", "from_address", "to_address", "token",
		"raw_amount", "token_decimals", "sweep_tx_hash"}).
		AddRow(id, entry.Chain, entry.Address, testDestination, entry.Token,
			entry.RawAmount.String(), entry.Decimals, "0xhash")
}

func sweepTransferLog(entry PlanEntry) map[string]any {
	return map[string]any{
		"address": testToken,
		"topics":  []string{transferEventTopic, padAddressTopic(entry.Address), padAddressTopic(testDestination)},
		"data":    "0x" + strings.Repeat("0", 64-len(entry.RawAmount.Text(16))) + entry.RawAmount.Text(16),
		"removed": false,
	}
}

func expectSweptAuditQuery(mock sqlmock.Sqlmock, entry PlanEntry) {
	mock.ExpectQuery(`(?s)FROM sweep_records\s+WHERE status = 'swept' AND job_version = 1`).
		WithArgs(int64(7200), 50).
		WillReturnRows(sweptAuditRows(1, entry))
}

// A swept record is only true while its transaction stays canonical. The audit
// re-reads the receipt and its block instead of trusting the status.
func TestAuditSweptKeepsACanonicalSweep(t *testing.T) {
	e, mock, chain, entry := setupExecutor(t)
	chain.mined = true
	chain.txs, chain.blocks = map[string]bool{"0xhash": true}, map[string]uint64{"0xhash": 100}
	chain.receiptLogs = []any{sweepTransferLog(entry)}
	expectSweptAuditQuery(mock, entry)

	audited, rolledBack, err := e.AuditSweptRecent(t.Context())
	if err != nil || audited != 1 || rolledBack != 0 {
		t.Fatalf("canonical sweep was not kept: audited=%d rolled_back=%d err=%v", audited, rolledBack, err)
	}
}

func TestAuditSweptFailsAProvenRollback(t *testing.T) {
	tests := []struct {
		name   string
		reason string
		setup  func(*localChain, PlanEntry)
	}{
		{"block is no longer canonical", "sweep block is no longer canonical", func(c *localChain, _ PlanEntry) {
			c.canonical = "0x" + strings.Repeat("77", 32)
		}},
		{"receipt reverted", "sweep transaction reverted on chain", func(c *localChain, _ PlanEntry) {
			c.status = "0x0"
		}},
		{"transfer missing from the receipt", "recorded transfer is missing from the canonical receipt", func(c *localChain, _ PlanEntry) {
			c.receiptLogs = nil
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, mock, chain, entry := setupExecutor(t)
			chain.mined = true
			chain.txs, chain.blocks = map[string]bool{"0xhash": true}, map[string]uint64{"0xhash": 100}
			chain.receiptLogs = []any{sweepTransferLog(entry)}
			tt.setup(chain, entry)
			expectSweptAuditQuery(mock, entry)
			mock.ExpectExec(`(?s)UPDATE sweep_records\s+SET status = 'failed', error = \$2`).
				WithArgs(int64(1), sweptRollbackPrefix+tt.reason).
				WillReturnResult(sqlmock.NewResult(0, 1))

			audited, rolledBack, err := e.AuditSweptRecent(t.Context())
			if err != nil || audited != 1 || rolledBack != 1 {
				t.Fatalf("rollback was not recorded: audited=%d rolled_back=%d err=%v", audited, rolledBack, err)
			}
		})
	}
}

// A missing receipt is not proof: a node that lags behind can also hide a
// canonical transaction. Only a mismatch or a revert is acted on.
func TestAuditSweptDoesNotTrustAMissingReceipt(t *testing.T) {
	e, mock, chain, entry := setupExecutor(t)
	chain.mined = false
	expectSweptAuditQuery(mock, entry)
	audited, rolledBack, err := e.AuditSweptRecent(t.Context())
	if err != nil || audited != 1 || rolledBack != 0 {
		t.Fatalf("missing receipt released the job: audited=%d rolled_back=%d err=%v", audited, rolledBack, err)
	}
}

// Reconcile used to answer "nothing to reconcile" for every swept row, so a
// rollback stayed hidden. It now re-reads the chain like the periodic audit.
func TestReconcileSweptReverifiesInsteadOfTrustingTheStatus(t *testing.T) {
	e, mock, chain, entry := setupExecutor(t)
	chain.mined = true
	chain.txs, chain.blocks = map[string]bool{"0xhash": true}, map[string]uint64{"0xhash": 100}
	chain.canonical = "0x" + strings.Repeat("77", 32)

	mock.ExpectQuery(`SELECT pg_try_advisory_lock`).WithArgs(sweepLockKey).
		WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(true))
	mock.ExpectQuery(`SELECT id, chain, from_address`).WithArgs(int64(4)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "chain", "from_address", "to_address", "token", "raw_amount",
			"token_decimals", "derivation_index", "status", "error", "job_version", "revision",
			"confirmations_required", "gas_payer", "gas_amount", "gas_nonce", "gas_raw_tx",
			"gas_tx_hash", "gas_confirmed", "sweep_nonce", "sweep_raw_tx", "sweep_tx_hash",
		}).AddRow(4, "bsc", entry.Address, testDestination, entry.Token, "10000000",
			6, 1, "swept", "", 1, 0, "2", "", "", "", "", "", false, "0", "0xraw", "0xhash"))
	mock.ExpectExec(`(?s)UPDATE sweep_records\s+SET status = 'failed', error = \$2`).
		WithArgs(int64(4), sweptRollbackPrefix+"sweep block is no longer canonical").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT pg_advisory_unlock`).WithArgs(sweepLockKey).
		WillReturnRows(sqlmock.NewRows([]string{"unlock"}).AddRow(true))

	out, err := e.Reconcile(t.Context(), 4)
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	if out.Action != ReconcileReleased || out.Status != "failed" {
		t.Fatalf("rolled back sweep must be released, got %+v", out)
	}
}

func TestReconcileSweptStillCanonicalReportsObserved(t *testing.T) {
	e, mock, chain, entry := setupExecutor(t)
	chain.mined = true
	chain.txs, chain.blocks = map[string]bool{"0xhash": true}, map[string]uint64{"0xhash": 100}
	chain.receiptLogs = []any{sweepTransferLog(entry)}

	mock.ExpectQuery(`SELECT pg_try_advisory_lock`).WithArgs(sweepLockKey).
		WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(true))
	mock.ExpectQuery(`SELECT id, chain, from_address`).WithArgs(int64(5)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "chain", "from_address", "to_address", "token", "raw_amount",
			"token_decimals", "derivation_index", "status", "error", "job_version", "revision",
			"confirmations_required", "gas_payer", "gas_amount", "gas_nonce", "gas_raw_tx",
			"gas_tx_hash", "gas_confirmed", "sweep_nonce", "sweep_raw_tx", "sweep_tx_hash",
		}).AddRow(5, "bsc", entry.Address, testDestination, entry.Token, "10000000",
			6, 1, "swept", "", 1, 0, "2", "", "", "", "", "", false, "0", "0xraw", "0xhash"))
	mock.ExpectQuery(`SELECT pg_advisory_unlock`).WithArgs(sweepLockKey).
		WillReturnRows(sqlmock.NewRows([]string{"unlock"}).AddRow(true))

	out, err := e.Reconcile(t.Context(), 5)
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	if out.Action != ReconcileObserved || out.Status != "swept" || out.Message == "" {
		t.Fatalf("canonical sweep must stay swept with an explanation, got %+v", out)
	}
}

// "failed" alone still reserves an address when the record carries a hash, so
// a rollback that must be re-planned relies on this released prefix.
func TestRolledBackSweepReleasesTheAddressForANewPlan(t *testing.T) {
	if !strings.Contains(reservedPredicate, "swept rollback released:%") {
		t.Fatal("reservedPredicate must stop treating a released rollback as a reservation")
	}
	message := rpc.SafeRecordedError(sweptRollbackPrefix + "sweep block is no longer canonical")
	if !strings.HasPrefix(message, sweptRollbackPrefix) {
		t.Fatalf("rollback reason did not survive the durable-record boundary: %q", message)
	}
}
