package sweeper

import (
	"database/sql/driver"
	"encoding/json"
	"math/big"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
)

type safeSweepErrorArg struct{ t *testing.T }

func (a safeSweepErrorArg) Match(value driver.Value) bool {
	text, ok := value.(string)
	if !ok || strings.Contains(text, "fixture-") || strings.Contains(text, "https://") {
		a.t.Error("durable sweep error leaked endpoint or upstream details")
		return false
	}
	return text != ""
}

func TestSweepLegacyErrorIsSafeInPersistenceAndSummary(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	j := &sweepJob{ID: 1, Version: 1, Status: "sweep_pending", Confirmations: 2,
		PlanEntry: PlanEntry{Chain: "bsc", Address: "fixture-address", RawAmount: big.NewInt(1)},
		Error:     "rpc https://example.test/fixture-secret-path?key=fixture-secret-query: fixture-node-message"}
	args := []driver.Value{int64(1), "sweep_pending", safeSweepErrorArg{t}, "2"}
	for len(args) < 14 {
		args = append(args, sqlmock.AnyArg())
	}
	mock.ExpectExec("UPDATE sweep_records SET").WithArgs(args...).WillReturnResult(sqlmock.NewResult(0, 1))
	e := &Executor{db: db}
	if err := e.saveJob(t.Context(), j); err != nil {
		t.Fatal(err)
	}
	// Simulate an old row loaded without re-saving it before a read.
	j.Error = "rpc https://example.test/fixture-secret-path?key=fixture-secret-query: fixture-node-message"
	out, err := json.Marshal(j.outcome())
	if err != nil || strings.Contains(string(out), "fixture-secret") || strings.Contains(string(out), "fixture-node") {
		t.Fatal("sweep summary exposed a historical credential")
	}
	if got := rpc.SafeError(&rpc.Failure{Kind: "http", HTTPStatus: 403}); !strings.Contains(got, "403") {
		t.Fatal("stable HTTP status missing from safe failure")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
