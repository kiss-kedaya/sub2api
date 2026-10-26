package cryptopay

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestSweepHistoryRedactsStoredRPCSecretsWithoutRewritingTransactions(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	mock.ExpectQuery("SELECT id, chain, from_address").WillReturnRows(
		sqlmock.NewRows([]string{"id", "chain", "from_address", "to_address", "token", "amount", "gas_tx_hash", "sweep_tx_hash", "status", "error", "created_at", "job_version"}).
			AddRow(1, "bsc", "from", "to", "token", "1", "gas-hash", "sweep-hash", "sweep_pending", "rpc https://example.test/fixture-secret-path?key=fixture-secret-query: fixture-node-message", time.Now(), 1))
	rows, err := (&Service{db: db}).ListSweepRecords(t.Context(), "", 100)
	if err != nil || len(rows) != 1 || rows[0].Status != "sweep_pending" || rows[0].SweepTxHash != "sweep-hash" {
		t.Fatal("history failed or transaction state changed")
	}
	encoded, err := json.Marshal(rows)
	if err != nil || strings.Contains(string(encoded), "fixture-") || strings.Contains(string(encoded), "https://") {
		t.Fatal("history response exposed old RPC credentials")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
