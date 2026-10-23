package scanner

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	_ "modernc.org/sqlite"
)

// Capture the query actually issued by the worker, then evaluate its joins
// and window predicate on isolated in-memory fixtures. Only the PostgreSQL
// cast and interval syntax are translated; the selection logic is unchanged.
func creditedWindowQuery(t *testing.T) string {
	t.Helper()
	var query string
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherFunc(func(_, actual string) error {
		query = actual
		return nil
	})))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		mock.ExpectClose()
		if err := db.Close(); err != nil {
			t.Errorf("close query capture db: %v", err)
		}
	}()
	mock.ExpectQuery("capture credited window").WillReturnRows(observationRows())
	if err := (&Worker{db: db}).recheckCredited(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
	return strings.NewReplacer(
		"d.amount::text", "CAST(d.amount AS TEXT)",
		"amount::text", "CAST(amount AS TEXT)",
		"NOW() - INTERVAL '1 hour'", "?",
	).Replace(query)
}

func TestCreditedRecheckWindowStartsAtOrderCompletion(t *testing.T) {
	query := creditedWindowQuery(t)
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	ptr := func(value time.Time) *time.Time { return &value }
	stamp := func(value time.Time) string { return value.Format("2006-01-02 15:04:05") }
	tests := []struct {
		name                 string
		created              time.Time
		completed            *time.Time
		linked, missingOrder bool
		status, reason       string
		want                 bool
	}{
		{"pending three hours then just completed", now.Add(-3 * time.Hour), ptr(now.Add(-time.Minute)), true, false, "credited", "", true},
		{"old completion stays expired despite fresh retry", now.Add(-3 * time.Hour), ptr(now.Add(-2 * time.Hour)), true, false, "credited", "", false},
		{"legacy recent observation", now.Add(-10 * time.Minute), nil, false, false, "credited", "", true},
		{"legacy expired observation despite fresh retry", now.Add(-2 * time.Hour), nil, false, false, "credited", "", false},
		{"null completion falls back to recent creation", now.Add(-10 * time.Minute), nil, true, false, "credited", "", true},
		{"null completion preserves expired fallback", now.Add(-2 * time.Hour), nil, true, false, "credited", "", false},
		{"missing joined order preserves legacy fallback", now.Add(-10 * time.Minute), nil, true, true, "credited", "", true},
		{"unresolved missing receipt survives window", now.Add(-3 * time.Hour), ptr(now.Add(-2 * time.Hour)), true, false, "credited", missingReceiptPrefix + "2", true},
		{"completion at strict window boundary", now.Add(-3 * time.Hour), ptr(now.Add(-time.Hour)), true, false, "credited", "", false},
		{"pending row is not a credited recheck", now.Add(-3 * time.Hour), ptr(now.Add(-time.Minute)), true, false, "pending", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, err := sql.Open("sqlite", ":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := db.Close(); err != nil {
					t.Errorf("close fixture db: %v", err)
				}
			}()
			db.SetMaxOpenConns(1)
			for _, schema := range []string{
				`CREATE TABLE payment_orders (id INTEGER PRIMARY KEY, completed_at TEXT, created_at TEXT, status TEXT, amount TEXT)`,
				`CREATE TABLE deposit_transactions (
					id INTEGER PRIMARY KEY, chain TEXT DEFAULT 'bsc', tx_hash TEXT, log_index INTEGER DEFAULT 0,
					from_address TEXT DEFAULT 'from', to_address TEXT DEFAULT 'to', token TEXT DEFAULT 'token',
					amount TEXT DEFAULT '1', block_number INTEGER DEFAULT 101, block_hash TEXT DEFAULT 'block',
					reason TEXT, status TEXT, created_at TEXT, last_attempt_at TEXT, order_id INTEGER)`,
			} {
				if _, err := db.ExecContext(t.Context(), schema); err != nil {
					t.Fatal(err)
				}
			}
			var orderID, completed any
			if tt.linked {
				orderID = int64(42)
				if tt.completed != nil {
					completed = stamp(*tt.completed)
				}
				if !tt.missingOrder {
					if _, err := db.ExecContext(t.Context(), `INSERT INTO payment_orders(id,completed_at,created_at,status,amount) VALUES(42,?,?,'COMPLETED','67')`, completed, stamp(tt.created)); err != nil {
						t.Fatal(err)
					}
				}
			}
			// Every fixture has a fresh attempt timestamp. That timestamp may
			// rotate the queue, but must never extend the risk window.
			if _, err := db.ExecContext(t.Context(), `INSERT INTO deposit_transactions(tx_hash,reason,status,created_at,last_attempt_at,order_id) VALUES(?,?,?,?,?,?)`,
				tt.name, tt.reason, tt.status, stamp(tt.created), stamp(now), orderID); err != nil {
				t.Fatal(err)
			}
			rows, err := db.QueryContext(t.Context(), query, stamp(now.Add(-time.Hour)))
			if err != nil {
				t.Fatal(err)
			}
			selected, err := readObservations(rows, "credited")
			if err != nil {
				t.Fatal(err)
			}
			if (len(selected) == 1) != tt.want {
				t.Fatalf("selected=%d, want eligible=%v", len(selected), tt.want)
			}
		})
	}
}
