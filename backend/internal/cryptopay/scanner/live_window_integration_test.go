package scanner_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/ledger"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/scanner"
	_ "modernc.org/sqlite"
)

type liveWindowResolver struct{}

func (liveWindowResolver) ResolveDeposit(context.Context, string, string) (int64, bool, error) {
	return 42, true, nil
}
func (liveWindowResolver) IsOwnAddress(context.Context, string, string) (bool, error) {
	return false, nil
}

type liveWindowCreditor struct {
	db    *sql.DB
	calls int
}

func (c *liveWindowCreditor) CreditDeposit(ctx context.Context, req ledger.CreditRequest) error {
	_, tradeNo := ledger.OrderIdentity(req.Chain, req.TxHash, req.LogIndex)
	result, err := c.db.ExecContext(ctx, `INSERT INTO payment_orders(user_id,amount,out_trade_no) VALUES(?,?,?) ON CONFLICT(out_trade_no) DO NOTHING`, req.UserID, req.CreditUSDT, tradeNo)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed > 0 {
		c.calls++
		_, err = c.db.ExecContext(ctx, `UPDATE balances SET amount = amount + ? WHERE user_id = ?`, req.CreditUSDT, req.UserID)
	}
	return err
}

// A real in-memory ledger and fulfillment fixture verify that a 115k-block
// backlog cannot delay a fresh payment, and replaying its overlapping live
// window cannot add the balance twice. Cursor SQL remains independently
// mocked so an illicit jump directly to the live head fails the test.
func TestLiveWindowCreditsThroughLedgerBeforeHistoricalCatchup(t *testing.T) {
	for _, scenario := range []struct {
		name, finality     string
		historyUnavailable bool
		recentReplay       bool
	}{
		{"finalized", "finalized", false, false},
		{"finalized behind", "behind", false, false},
		{"finalized unavailable", "unavailable", false, false},
		{"history RPC failure cannot delay latest deposit", "finalized", true, false},
		{"late indexed logs below advanced cursor", "finalized", false, true},
		{"recent replay still requires finalized block", "behind", false, true},
		{"recent replay cannot bypass unavailable finality", "unavailable", false, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			db, err := sql.Open("sqlite", ":memory:")
			if err != nil {
				t.Fatal(err)
			}
			db.SetMaxOpenConns(1)
			defer func() { _ = db.Close() }()
			for _, schema := range []string{
				`CREATE TABLE balances(user_id INTEGER PRIMARY KEY,amount REAL)`,
				`INSERT INTO balances VALUES(42,0)`,
				`CREATE TABLE payment_orders(id INTEGER PRIMARY KEY,user_id INTEGER,amount REAL,out_trade_no TEXT UNIQUE)`,
				`CREATE TABLE deposit_transactions(chain TEXT,tx_hash TEXT,log_index INTEGER,from_address TEXT,to_address TEXT,token TEXT,amount TEXT,block_number INTEGER,block_hash TEXT,status TEXT,order_id INTEGER, UNIQUE(chain,tx_hash,log_index))`,
			} {
				if _, err := db.ExecContext(t.Context(), schema); err != nil {
					t.Fatal(err)
				}
			}
			token, from, to := "0x"+strings.Repeat("11", 20), "0x"+strings.Repeat("22", 20), "0x"+strings.Repeat("33", 20)
			tx, hash := "0x"+strings.Repeat("44", 32), "0x"+strings.Repeat("55", 32)
			const depositBlock = uint64(115100)
			log := map[string]any{"address": token, "topics": []string{
				"0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef", "0x" + strings.Repeat("0", 24) + from[2:], "0x" + strings.Repeat("0", 24) + to[2:]},
				"data": fmt.Sprintf("0x%064x", 1234567), "blockNumber": fmt.Sprintf("0x%x", depositBlock), "transactionHash": tx, "logIndex": "0x7", "blockHash": hash}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Method string            `json:"method"`
					Params []json.RawMessage `json:"params"`
				}
				if json.NewDecoder(r.Body).Decode(&request) != nil {
					t.Error("bad JSON-RPC request")
					return
				}
				var result any
				switch request.Method {
				case "eth_chainId":
					result = "0x38"
				case "eth_blockNumber":
					result = fmt.Sprintf("0x%x", 115110)
				case "eth_getLogs":
					var query struct{ From, To string }
					var filter map[string]json.RawMessage
					_ = json.Unmarshal(request.Params[0], &filter)
					_ = json.Unmarshal(filter["fromBlock"], &query.From)
					_ = json.Unmarshal(filter["toBlock"], &query.To)
					var first, last uint64
					_, _ = fmt.Sscanf(query.From, "0x%x", &first)
					_, _ = fmt.Sscanf(query.To, "0x%x", &last)
					if last-first+1 > 100 {
						t.Error("public provider range limit exceeded")
					}
					if scenario.historyUnavailable && last < depositBlock {
						w.WriteHeader(http.StatusServiceUnavailable)
						return
					}
					result = []any{}
					if first <= depositBlock && depositBlock <= last {
						result = []any{log}
					}
				case "eth_getTransactionReceipt":
					result = map[string]any{"status": "0x1", "blockNumber": fmt.Sprintf("0x%x", depositBlock), "blockHash": hash, "transactionHash": tx, "logs": []any{log}}
				case "eth_getBlockByNumber":
					var tag string
					_ = json.Unmarshal(request.Params[0], &tag)
					if tag == "safe" {
						t.Error("BSC incorrectly queried justified head")
					}
					if tag == "finalized" && scenario.finality == "unavailable" {
						w.WriteHeader(http.StatusServiceUnavailable)
						return
					}
					number := depositBlock
					if tag == "finalized" && scenario.finality == "behind" {
						number--
					}
					result = map[string]any{"number": fmt.Sprintf("0x%x", number), "hash": hash}
				default:
					t.Errorf("unexpected RPC %s", request.Method)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": result})
			}))
			defer server.Close()
			cursorDB, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = cursorDB.Close() }()
			creditor := &liveWindowCreditor{db: db}
			handler := ledger.New(db, creditor, liveWindowResolver{}, 0.001, 10000, 1)
			chain := scanner.ChainConfig{Chain: "bsc", Tokens: []string{token}, Decimals: map[string]int{token: 6}, Confirmations: 3, MaxRange: 100}
			worker := scanner.NewWorker(cursorDB, map[string]*rpc.Pool{"bsc": rpc.NewPoolWithHTTPClient(server.URL, server.Client())}, []scanner.ChainConfig{chain}, scanner.NewCursorStore(cursorDB), handler,
				func(context.Context, string) ([]string, error) { return []string{to}, nil }, 0, 100)
			for pass := 0; pass < 2; pass++ {
				start := int64(100 + pass*400)
				if scenario.recentReplay {
					start = 115107 // Caught up, but the provider indexed this deposit late.
					// A second live instance must not credit the overlapping transfer twice.
					worker = scanner.NewWorker(cursorDB, map[string]*rpc.Pool{"bsc": rpc.NewPoolWithHTTPClient(server.URL, server.Client())}, []scanner.ChainConfig{chain}, scanner.NewCursorStore(cursorDB), handler,
						func(context.Context, string) ([]string, error) { return []string{to}, nil }, 5*time.Second, 100)
				}
				if scenario.historyUnavailable {
					start = 100
				}
				mock.ExpectQuery(`SELECT last_block`).WithArgs("bsc").WillReturnRows(sqlmock.NewRows([]string{"last_block"}).AddRow(start))
				if !scenario.historyUnavailable && !scenario.recentReplay {
					for batch := int64(1); batch <= 4; batch++ {
						mock.ExpectExec(`INSERT INTO crypto_scan_cursors`).WithArgs("bsc", start+batch*100).WillReturnResult(sqlmock.NewResult(0, 1))
					}
				}
				if err := worker.ScanOnce(t.Context()); (err != nil) != scenario.historyUnavailable {
					t.Fatalf("scan error=%v wantError=%t", err, scenario.historyUnavailable)
				}
			}
			var amount float64
			if err := db.QueryRow(`SELECT amount FROM balances WHERE user_id=42`).Scan(&amount); err != nil {
				t.Fatal(err)
			}
			want, calls := float64(0), 0
			if scenario.finality == "finalized" {
				want, calls = 1.235, 1
			}
			if amount != want || creditor.calls != calls {
				t.Fatalf("balance=%v calls=%d want=%v/%d", amount, creditor.calls, want, calls)
			}
			var status string
			if err := db.QueryRow(`SELECT status FROM deposit_transactions WHERE tx_hash=?`, tx).Scan(&status); err != nil {
				t.Fatal(err)
			}
			wantStatus := "pending"
			if scenario.finality == "finalized" {
				wantStatus = "credited"
			}
			if status != wantStatus {
				t.Fatalf("deposit status=%s want=%s", status, wantStatus)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
