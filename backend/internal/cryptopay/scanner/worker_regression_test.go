package scanner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
)

type recordingHandler struct {
	pendingErr, processErr error
	pending, processed     []Transfer
}

func (h *recordingHandler) ProcessPending(_ context.Context, tr Transfer, _ int) error {
	h.pending = append(h.pending, tr)
	return h.pendingErr
}

func (h *recordingHandler) Process(_ context.Context, tr Transfer, _ int) (bool, error) {
	h.processed = append(h.processed, tr)
	return h.processErr == nil, h.processErr
}

func testTransfer() Transfer {
	return Transfer{Chain: "bsc", TxHash: "0x" + strings.Repeat("44", 32), LogIndex: 7,
		From: "0x" + strings.Repeat("22", 20), To: "0x" + strings.Repeat("33", 20),
		Token: "0x" + strings.Repeat("11", 20), Amount: big.NewInt(1_000_000),
		BlockNumber: 101, BlockHash: "0x" + strings.Repeat("55", 32)}
}

func logFor(tr Transfer) evmLog {
	return evmLog{Address: tr.Token, Topics: []string{transferTopic, padTopic(tr.From), padTopic(tr.To)},
		Data: fmt.Sprintf("0x%064x", tr.Amount), BlockNumber: fmt.Sprintf("0x%x", tr.BlockNumber),
		TxHash: tr.TxHash, LogIndex: fmt.Sprintf("0x%x", tr.LogIndex), BlockHash: tr.BlockHash}
}

// All RPCs are in-process HTTP fixtures. No endpoint is read from config or
// the environment, and no database connection leaves sqlmock.
type rpcFixture struct {
	receipt   any
	canonical any
	safe      any
	head      any
	chainID   string
	logs      []evmLog
	fail      string
	httpFail  string
	mu        sync.Mutex
	calls     map[string]int
}

func newRPCFixture(tr Transfer) *rpcFixture {
	return &rpcFixture{
		receipt: map[string]any{"status": "0x1", "blockNumber": fmt.Sprintf("0x%x", tr.BlockNumber),
			"blockHash": tr.BlockHash, "transactionHash": tr.TxHash, "logs": []evmLog{logFor(tr)}},
		canonical: map[string]any{"number": fmt.Sprintf("0x%x", tr.BlockNumber), "hash": tr.BlockHash},
		safe:      map[string]any{"number": "0x6e"}, head: "0x6e", chainID: "0x38", logs: []evmLog{logFor(tr)},
		calls: make(map[string]int),
	}
}

func setFixtureMapValue(target any, key string, value any) {
	fields, ok := target.(map[string]any)
	if !ok {
		panic("invalid RPC fixture map")
	}
	fields[key] = value
}

func (f *rpcFixture) pool(t *testing.T) *rpc.Pool {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		key := request.Method
		if key == "eth_getBlockByNumber" {
			var tag string
			if len(request.Params) == 0 || json.Unmarshal(request.Params[0], &tag) != nil {
				t.Error("missing block tag")
				return
			}
			if tag == "safe" {
				key = "safe"
			} else {
				key = "canonical"
			}
		}
		f.mu.Lock()
		f.calls[key]++
		f.mu.Unlock()
		if key == f.httpFail {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if key == f.fail {
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1,
				"error": map[string]any{"code": -32000, "message": "fixture RPC unavailable"}})
			return
		}
		var result any
		switch key {
		case "eth_chainId":
			result = f.chainID
		case "eth_blockNumber":
			result = f.head
		case "eth_getLogs":
			result = f.logs
		case "eth_getTransactionReceipt":
			result = f.receipt
		case "safe":
			result = f.safe
		case "canonical":
			result = f.canonical
		default:
			t.Errorf("unexpected RPC %q", request.Method)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": result})
	}))
	t.Cleanup(server.Close)
	return rpc.NewPoolWithHTTPClient(server.URL, server.Client())
}

func (f *rpcFixture) callCount(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[method]
}

func observationRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"chain", "tx_hash", "log_index", "from_address", "to_address", "token",
		"amount", "block_number", "block_hash", "reason"})
}

func expectObservation(mock sqlmock.Sqlmock, tr Transfer, amount, reason string) {
	mock.ExpectQuery(`FROM deposit_transactions`).WillReturnRows(observationRows().AddRow(
		tr.Chain, tr.TxHash, tr.LogIndex, tr.From, tr.To, tr.Token, amount, tr.BlockNumber, tr.BlockHash, reason))
}

func expectObservationUpdate(mock sqlmock.Sqlmock, tr Transfer, oldStatus, oldReason, status, reason string) *sqlmock.ExpectedExec {
	return mock.ExpectExec(`(?s)UPDATE deposit_transactions.*last_attempt_at = NOW\(\).*AND status = \$6 AND reason = \$7`).
		WithArgs(status, reason, tr.Chain, tr.TxHash, tr.LogIndex, oldStatus, oldReason)
}

func expectPendingReincludedRetirement(mock sqlmock.Sqlmock, tr Transfer, reason string) {
	mock.ExpectExec(`(?s)UPDATE deposit_transactions SET status = 'reverted'.*order_id IS NULL`).
		WithArgs(tr.Chain, tr.TxHash, tr.LogIndex, reason).
		WillReturnResult(sqlmock.NewResult(0, 1))
}

func expectPendingReincludedMove(mock sqlmock.Sqlmock, old Transfer, reason string, next Transfer) {
	mock.ExpectQuery(`SELECT EXISTS \(SELECT 1 FROM payment_orders WHERE out_trade_no = \$1\)`).
		WithArgs(depositOrderNo(old.Chain, old.TxHash, old.LogIndex)).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectExec(`(?s)UPDATE deposit_transactions SET log_index = \$1, block_number = \$2, block_hash = \$3.*status = 'pending'.*order_id IS NULL`).
		WithArgs(next.LogIndex, next.BlockNumber, next.BlockHash, old.Chain, old.TxHash, old.LogIndex, reason).
		WillReturnResult(sqlmock.NewResult(0, 1))
}

func expectPendingReincludedSameIndex(mock sqlmock.Sqlmock, old Transfer, reason string, next Transfer) {
	mock.ExpectExec(`(?s)UPDATE deposit_transactions SET block_number = \$1, block_hash = \$2, last_attempt_at = NOW\(\).*status = 'pending'.*order_id IS NULL`).
		WithArgs(next.BlockNumber, next.BlockHash, old.Chain, old.TxHash, old.LogIndex, reason).
		WillReturnResult(sqlmock.NewResult(0, 1))
}

func workerFixture(t *testing.T, f *rpcFixture) (*Worker, sqlmock.Sqlmock, *recordingHandler) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	t.Cleanup(func() {
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Error(err)
		}
	})
	db.SetMaxOpenConns(1)
	tr := testTransfer()
	handler := &recordingHandler{}
	w := &Worker{db: db, pools: map[string]*rpc.Pool{"bsc": f.pool(t)},
		chains: []ChainConfig{{Chain: "bsc", Tokens: []string{tr.Token}, Decimals: map[string]int{tr.Token: 6}, Confirmations: 5}},
		cursor: NewCursorStore(db), handler: handler, maxRange: 2000,
		addresses: func(context.Context, string) ([]string, error) { return []string{tr.To}, nil },
	}
	return w, mock, handler
}

func TestScanChainStopsBeforeCursorWhenPendingCannotBePersisted(t *testing.T) {
	for _, mode := range []string{"missing receipt", "RPC error", "unsafe block", "canonical error", "changed observation"} {
		t.Run(mode, func(t *testing.T) {
			tr := testTransfer()
			fixture := newRPCFixture(tr)
			switch mode {
			case "missing receipt":
				fixture.receipt = nil
			case "RPC error":
				fixture.fail = "eth_getTransactionReceipt"
			case "unsafe block":
				fixture.safe = map[string]any{"number": "0x64"}
			case "canonical error":
				fixture.fail = "canonical"
			case "changed observation":
				fixture.canonical = map[string]any{"number": "0x65", "hash": "0x" + strings.Repeat("66", 32)}
			}
			w, mock, handler := workerFixture(t, fixture)
			writeErr := errors.New("pending persistence failed")
			handler.pendingErr = writeErr
			mock.ExpectQuery(`SELECT last_block`).WithArgs("bsc").WillReturnRows(sqlmock.NewRows([]string{"last_block"}).AddRow(100))
			err := w.scanChain(t.Context(), w.chains[0])
			if !errors.Is(err, writeErr) {
				t.Fatalf("want pending persistence failure, got %v", err)
			}
			if len(handler.processed) != 0 || len(handler.pending) != 1 {
				t.Fatalf("processed=%d pending=%d", len(handler.processed), len(handler.pending))
			}
			// No cursor write is expected or permitted on this failed batch.
		})
	}
}

func TestScanChainPersistsDeferredObservationBeforeAdvancing(t *testing.T) {
	tr := testTransfer()
	fixture := newRPCFixture(tr)
	fixture.receipt = nil
	w, mock, handler := workerFixture(t, fixture)
	mock.ExpectQuery(`SELECT last_block`).WillReturnRows(sqlmock.NewRows([]string{"last_block"}).AddRow(100))
	mock.ExpectExec(`INSERT INTO crypto_scan_cursors`).WithArgs("bsc", int64(105)).WillReturnResult(sqlmock.NewResult(0, 1))
	if err := w.scanChain(t.Context(), w.chains[0]); err != nil {
		t.Fatal(err)
	}
	if len(handler.pending) != 1 || len(handler.processed) != 0 {
		t.Fatalf("unexpected handler calls: %+v", handler)
	}
}

func TestScanChainProcessingFailureKeepsCursor(t *testing.T) {
	fixture := newRPCFixture(testTransfer())
	w, mock, handler := workerFixture(t, fixture)
	writeErr := errors.New("ignored row write failed")
	handler.processErr = writeErr
	mock.ExpectQuery(`SELECT last_block`).WillReturnRows(sqlmock.NewRows([]string{"last_block"}).AddRow(100))
	if err := w.scanChain(t.Context(), w.chains[0]); !errors.Is(err, writeErr) {
		t.Fatalf("got %v", err)
	}
	if len(handler.processed) != 1 {
		t.Fatal("verified transfer was not processed")
	}
}

func TestLiveAndPendingUseSameFinalityPolicy(t *testing.T) {
	tests := []struct {
		name          string
		confirmations uint64
		safe          any
		fail          string
		head          any
		credit        bool
		wantErr       bool
	}{
		{"safe and enough depth", 5, map[string]any{"number": "0x6e"}, "", "0x6e", true, false},
		{"safe unavailable insufficient depth", 500, nil, "safe", "0x6e", false, false},
		{"safe unavailable enough depth", 5, nil, "safe", "0x6e", true, false},
		{"safe succeeds insufficient configured depth", 500, map[string]any{"number": "0x6e"}, "", "0x6e", false, false},
		{"safe behind despite enough depth", 5, map[string]any{"number": "0x64"}, "", "0x6e", false, false},
		{"head RPC fails", 5, nil, "eth_blockNumber", "0x6e", false, true},
		{"head malformed", 5, nil, "", "0x6ejunk", false, true},
		{"safe null fallback", 5, nil, "", "0x6e", true, false},
		{"head behind receipt", 0, nil, "safe", "0x64", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := testTransfer()
			fixture := newRPCFixture(tr)
			fixture.safe, fixture.fail, fixture.head = tt.safe, tt.fail, tt.head
			w, mock, handler := workerFixture(t, fixture)
			w.chains[0].Confirmations = tt.confirmations
			expectObservation(mock, tr, "1", "")
			expectObservationUpdate(mock, tr, "pending", "", "pending", "").WillReturnResult(sqlmock.NewResult(0, 1))
			err := w.retryPending(t.Context())
			if (err != nil) != tt.wantErr {
				t.Fatalf("retry error=%v, wantErr=%v", err, tt.wantErr)
			}
			if (len(handler.processed) == 1) != tt.credit {
				t.Fatalf("processed=%d credit=%v", len(handler.processed), tt.credit)
			}
			// This is the same function the live scan calls, with the same
			// block/config. Pin the fallback and fail-closed result directly.
			final, err := transactionIsFinal(t.Context(), w.pools["bsc"], tr.BlockNumber, tt.confirmations)
			if final != tt.credit || (err != nil) != tt.wantErr {
				t.Fatalf("shared finality=%v, err=%v", final, err)
			}
		})
	}
}

func TestPendingRejectsChangedOrNoncanonicalTransfer(t *testing.T) {
	tests := []struct {
		name    string
		change  func(*rpcFixture)
		reason  string
		wantErr bool
	}{
		{"block changed", func(f *rpcFixture) {
			hash := "0x" + strings.Repeat("66", 32)
			setFixtureMapValue(f.receipt, "blockHash", hash)
			setFixtureMapValue(f.canonical, "hash", hash)
		}, "reorg_transfer_changed", false},
		{"receipt block orphaned", func(f *rpcFixture) { setFixtureMapValue(f.canonical, "hash", "0x"+strings.Repeat("66", 32)) }, "reorg_noncanonical_block", false},
		{"log absent", func(f *rpcFixture) { setFixtureMapValue(f.receipt, "logs", []evmLog{}) }, "reorg_transfer_changed", false},
		{"wrong chain", func(f *rpcFixture) { f.chainID = "0x89" }, "", true},
		{"wrong tx", func(f *rpcFixture) { setFixtureMapValue(f.receipt, "transactionHash", "0x"+strings.Repeat("99", 32)) }, "", true},
		{"canonical RPC down", func(f *rpcFixture) { f.fail = "canonical" }, "", true},
	}
	for _, field := range []string{"token", "from", "to", "amount", "topic", "removed", "log block", "log tx"} {
		field := field
		tests = append(tests, struct {
			name    string
			change  func(*rpcFixture)
			reason  string
			wantErr bool
		}{
			field, func(f *rpcFixture) {
				receipt, ok := f.receipt.(map[string]any)
				if !ok {
					panic("invalid RPC receipt fixture")
				}
				logs, ok := receipt["logs"].([]evmLog)
				if !ok {
					panic("invalid RPC logs fixture")
				}
				switch field {
				case "token":
					logs[0].Address = "0x" + strings.Repeat("99", 20)
				case "from":
					logs[0].Topics[1] = padTopic("0x" + strings.Repeat("99", 20))
				case "to":
					logs[0].Topics[2] = padTopic("0x" + strings.Repeat("99", 20))
				case "amount":
					logs[0].Data = fmt.Sprintf("0x%064x", 2_000_000)
				case "index":
					logs[0].LogIndex = "0x8"
				case "topic":
					logs[0].Topics[0] = "0x" + strings.Repeat("99", 32)
				case "removed":
					logs[0].Removed = true
				case "log block":
					logs[0].BlockHash = "0x" + strings.Repeat("99", 32)
				case "log tx":
					logs[0].TxHash = "0x" + strings.Repeat("99", 32)
				}
			}, "reorg_transfer_changed", false,
		})
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := testTransfer()
			fixture := newRPCFixture(tr)
			tt.change(fixture)
			w, mock, handler := workerFixture(t, fixture)
			expectObservation(mock, tr, "1", "")
			status := "pending"
			if tt.reason != "" {
				status = "reverted"
			}
			expectObservationUpdate(mock, tr, "pending", "", status, tt.reason).WillReturnResult(sqlmock.NewResult(0, 1))
			err := w.retryPending(t.Context())
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
			if len(handler.processed) != 0 {
				t.Fatal("stale observation reached creditor")
			}
		})
	}
}

func TestPendingReinclusionAtSameLogIndexRefreshesCanonicalObservation(t *testing.T) {
	tr := testTransfer()
	newBlockHash := "0x" + strings.Repeat("66", 32)
	fixture := newRPCFixture(tr)
	setFixtureMapValue(fixture.receipt, "blockNumber", "0x65")
	setFixtureMapValue(fixture.receipt, "blockHash", newBlockHash)
	setFixtureMapValue(fixture.canonical, "hash", newBlockHash)
	logs := fixture.receipt.(map[string]any)["logs"].([]evmLog)
	logs[0].BlockHash = newBlockHash

	w, mock, handler := workerFixture(t, fixture)
	expectObservation(mock, tr, "1", "")
	next := tr
	next.BlockNumber = 101
	next.BlockHash = newBlockHash
	expectPendingReincludedSameIndex(mock, tr, "", next)
	if err := w.retryPending(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(handler.processed) != 1 || handler.processed[0].LogIndex != tr.LogIndex {
		t.Fatalf("same-index reinclusion was not processed after canonical update: %+v", handler.processed)
	}
}

func TestPendingPreservesExact18DecimalAmount(t *testing.T) {
	tr := testTransfer()
	tr.Amount = big.NewInt(10_000_000_000_000_001)
	w, mock, handler := workerFixture(t, newRPCFixture(tr))
	w.chains[0].Decimals[tr.Token] = 18
	expectObservation(mock, tr, "0.010000000000000001", "")
	expectObservationUpdate(mock, tr, "pending", "", "pending", "").WillReturnResult(sqlmock.NewResult(0, 1))
	if err := w.retryPending(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(handler.processed) != 1 || handler.processed[0].Amount.Cmp(tr.Amount) != 0 {
		t.Fatalf("wrong raw amount: %+v", handler.processed)
	}
}

func TestPendingDoesNotCreditIfAttemptPersistenceFailsOrRowChanged(t *testing.T) {
	for _, mode := range []string{"write failed", "row changed"} {
		t.Run(mode, func(t *testing.T) {
			tr := testTransfer()
			w, mock, handler := workerFixture(t, newRPCFixture(tr))
			expectObservation(mock, tr, "1", "")
			expectation := expectObservationUpdate(mock, tr, "pending", "", "pending", "")
			if mode == "write failed" {
				expectation.WillReturnError(errors.New("disk full"))
			} else {
				expectation.WillReturnResult(sqlmock.NewResult(0, 0))
			}
			err := w.retryPending(t.Context())
			if (err != nil) != (mode == "write failed") {
				t.Fatalf("got %v", err)
			}
			if len(handler.processed) != 0 {
				t.Fatal("credit ran without persisted current observation")
			}
		})
	}
}

func TestPendingReceiptNullNeverBecomesTerminalReorg(t *testing.T) {
	tr := testTransfer()
	fixture := newRPCFixture(tr)
	fixture.receipt = nil
	w, mock, handler := workerFixture(t, fixture)
	for range missingReceiptThreshold {
		expectObservation(mock, tr, "1", "")
		expectObservationUpdate(mock, tr, "pending", "", "pending", "").WillReturnResult(sqlmock.NewResult(0, 1))
		if err := w.retryPending(t.Context()); err != nil {
			t.Fatalf("null receipt must remain retryable: %v", err)
		}
	}
	if len(handler.processed) != 0 {
		t.Fatal("pending transfer must not be credited while RPC has no receipt")
	}
	if got := fixture.callCount("eth_getTransactionReceipt"); got != missingReceiptThreshold {
		t.Fatalf("receipt retries=%d, want %d", got, missingReceiptThreshold)
	}
}

func TestCreditedMissingReceiptPersistsAcrossWorkerRestarts(t *testing.T) {
	tr := testTransfer()
	fixture := newRPCFixture(tr)
	fixture.receipt = nil
	w, mock, _ := workerFixture(t, fixture)
	notifications := 0
	w.onReorg = func(chain, tx string) {
		if chain != tr.Chain || tx != tr.TxHash {
			t.Error("wrong notification")
		}
		notifications++
	}
	for round := 1; round <= 3; round++ {
		oldReason := ""
		if round > 1 {
			oldReason = fmt.Sprintf("%s%d", missingReceiptPrefix, round-1)
		}
		status, reason := "credited", fmt.Sprintf("%s%d", missingReceiptPrefix, round)
		if round == 3 {
			status, reason = "reverted", "reorg_receipt_missing"
		}
		expectObservation(mock, tr, "1", oldReason)
		expectObservationUpdate(mock, tr, "credited", oldReason, status, reason).WillReturnResult(sqlmock.NewResult(0, 1))
		// Recreate the worker: the sequence must come from SQL, not memory.
		restarted := &Worker{db: w.db, pools: w.pools, chains: w.chains, onReorg: w.onReorg}
		if err := restarted.recheckCredited(t.Context()); err != nil {
			t.Fatal(err)
		}
		want := 0
		if round == 3 {
			want = 1
		}
		if notifications != want {
			t.Fatalf("round=%d notifications=%d", round, notifications)
		}
	}
	mock.ExpectQuery(`FROM deposit_transactions`).WillReturnRows(observationRows())
	if err := w.recheckCredited(t.Context()); err != nil {
		t.Fatal(err)
	}
	if notifications != 1 {
		t.Fatal("duplicate reorg notification")
	}
}

func TestCreditedRPCFailureIsNotEvidenceOfMissingTransaction(t *testing.T) {
	for _, mode := range []string{"receipt RPC", "receipt HTTP", "bad receipt", "canonical RPC", "wrong chain"} {
		t.Run(mode, func(t *testing.T) {
			tr := testTransfer()
			fixture := newRPCFixture(tr)
			switch mode {
			case "receipt RPC":
				fixture.fail = "eth_getTransactionReceipt"
			case "receipt HTTP":
				fixture.httpFail = "eth_getTransactionReceipt"
			case "bad receipt":
				fixture.receipt = map[string]any{"status": "junk"}
			case "canonical RPC":
				fixture.fail = "canonical"
			case "wrong chain":
				fixture.chainID = "0x89"
			}
			w, mock, handler := workerFixture(t, fixture)
			notifications := 0
			w.onReorg = func(string, string) { notifications++ }
			expectObservation(mock, tr, "1", missingReceiptPrefix+"2")
			expectObservationUpdate(mock, tr, "credited", missingReceiptPrefix+"2", "credited", missingReceiptPrefix+"0").WillReturnResult(sqlmock.NewResult(0, 1))
			if err := w.recheckCredited(t.Context()); err == nil {
				t.Fatal("RPC/decode error was hidden")
			}
			if notifications != 0 || len(handler.processed) != 0 {
				t.Fatal("RPC failure caused a funds action or a reorg alert")
			}
		})
	}
}

func TestPendingExcludesWrongChainEndpointFromRotatingPool(t *testing.T) {
	tr := testTransfer()
	right, wrong := newRPCFixture(tr), newRPCFixture(tr)
	wrong.chainID = "0x89"
	w, mock, handler := workerFixture(t, right)
	w.pools["bsc"] = rpc.NewPoolWithHTTPClient(strings.Join([]string{w.pools["bsc"].Endpoints()[0], wrong.pool(t).Endpoints()[0]}, ","), &http.Client{})
	expectObservation(mock, tr, "1", "")
	expectObservationUpdate(mock, tr, "pending", "", "pending", "").WillReturnResult(sqlmock.NewResult(0, 1))
	if err := w.retryPending(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(handler.processed) != 1 || wrong.callCount("eth_getTransactionReceipt") != 0 {
		t.Fatalf("processed=%d receipts from wrong chain=%d", len(handler.processed), wrong.callCount("eth_getTransactionReceipt"))
	}
	if len(w.pools["bsc"].Endpoints()) != 2 {
		t.Fatal("shared configured pool was mutated")
	}
}

func TestCreditedExplicitCanonicalReorgIsPersistedAndNotified(t *testing.T) {
	for _, mode := range []string{"failed receipt", "orphaned receipt", "changed block"} {
		t.Run(mode, func(t *testing.T) {
			tr := testTransfer()
			fixture := newRPCFixture(tr)
			reason := ""
			switch mode {
			case "failed receipt":
				setFixtureMapValue(fixture.receipt, "status", "0x0")
				reason = "reorg_receipt_reverted"
			case "orphaned receipt":
				setFixtureMapValue(fixture.canonical, "hash", "0x"+strings.Repeat("66", 32))
				reason = "reorg_noncanonical_block"
			case "changed block":
				hash := "0x" + strings.Repeat("66", 32)
				setFixtureMapValue(fixture.receipt, "blockHash", hash)
				setFixtureMapValue(fixture.canonical, "hash", hash)
				reason = "reorg_transfer_changed"
			}
			w, mock, handler := workerFixture(t, fixture)
			notices := 0
			w.onReorg = func(string, string) {
				if err := mock.ExpectationsWereMet(); err != nil {
					t.Errorf("notice before persisted anomaly: %v", err)
				}
				notices++
			}
			expectObservation(mock, tr, "1", "")
			expectObservationUpdate(mock, tr, "credited", "", "reverted", reason).WillReturnResult(sqlmock.NewResult(0, 1))
			if err := w.recheckCredited(t.Context()); err != nil {
				t.Fatal(err)
			}
			if notices != 1 || len(handler.processed) != 0 {
				t.Fatalf("notices=%d processing=%d", notices, len(handler.processed))
			}
		})
	}
}

func TestDisabledChainRowsRotateOutOfTheRecheckPage(t *testing.T) {
	tr := testTransfer()
	fixture := newRPCFixture(tr)
	w, mock, handler := workerFixture(t, fixture)
	firstPage := observationRows()
	for index := 0; index < 200; index++ {
		tx := fmt.Sprintf("disabled-tx-%d", index)
		firstPage.AddRow("polygon", tx, 0, tr.From, tr.To, tr.Token, "1", 101, tr.BlockHash, "")
	}
	mock.ExpectQuery(`(?s)ORDER BY COALESCE\(d.last_attempt_at, d.created_at\), d.id.*LIMIT 200`).WillReturnRows(firstPage)
	for index := 0; index < 200; index++ {
		skipped := tr
		skipped.Chain, skipped.TxHash, skipped.LogIndex = "polygon", fmt.Sprintf("disabled-tx-%d", index), 0
		expectObservationUpdate(mock, skipped, "credited", "", "credited", "").WillReturnResult(sqlmock.NewResult(0, 1))
	}
	if err := w.recheckCredited(t.Context()); err != nil {
		t.Fatal(err)
	}
	// The configured chain behind that full page must get a turn next;
	// every skipped row above has a durable, newer check timestamp.
	expectObservation(mock, tr, "1", "")
	expectObservationUpdate(mock, tr, "credited", "", "credited", "").WillReturnResult(sqlmock.NewResult(0, 1))
	if err := w.recheckCredited(t.Context()); err != nil {
		t.Fatal(err)
	}
	if fixture.callCount("eth_getTransactionReceipt") != 1 || len(handler.processed) != 0 {
		t.Fatal("wrong recheck scope or repeated fulfillment")
	}
}

func TestCreditedReceiptReappearingClearsSuspicion(t *testing.T) {
	tr := testTransfer()
	w, mock, handler := workerFixture(t, newRPCFixture(tr))
	notices := 0
	w.onReorg = func(string, string) { notices++ }
	expectObservation(mock, tr, "1", missingReceiptPrefix+"2")
	expectObservationUpdate(mock, tr, "credited", missingReceiptPrefix+"2", "credited", "").WillReturnResult(sqlmock.NewResult(0, 1))
	if err := w.recheckCredited(t.Context()); err != nil {
		t.Fatal(err)
	}
	if notices != 0 || len(handler.processed) != 0 {
		t.Fatal("healthy credited deposit must not be fulfilled again")
	}
}

func TestCreditedAlertRequiresPersistedGuardedTransition(t *testing.T) {
	for _, mode := range []string{"persisted", "write failed", "another worker won"} {
		t.Run(mode, func(t *testing.T) {
			tr := testTransfer()
			fixture := newRPCFixture(tr)
			fixture.receipt = nil
			w, mock, _ := workerFixture(t, fixture)
			notices := 0
			w.onReorg = func(string, string) { notices++ }
			expectObservation(mock, tr, "1", missingReceiptPrefix+"2")
			expectation := expectObservationUpdate(mock, tr, "credited", missingReceiptPrefix+"2", "reverted", "reorg_receipt_missing")
			switch mode {
			case "persisted":
				expectation.WillReturnResult(sqlmock.NewResult(0, 1))
			case "write failed":
				expectation.WillReturnError(errors.New("disk full"))
			case "another worker won":
				expectation.WillReturnResult(sqlmock.NewResult(0, 0))
			}
			err := w.recheckCredited(t.Context())
			if (err != nil) != (mode == "write failed") {
				t.Fatalf("got %v", err)
			}
			want := 0
			if mode == "persisted" {
				want = 1
			}
			if notices != want {
				t.Fatalf("notices=%d", notices)
			}
		})
	}
}

func TestReceiptAbsenceIsDistinctFromMalformedResponse(t *testing.T) {
	for _, mode := range []string{"null", "malformed", "reverted"} {
		t.Run(mode, func(t *testing.T) {
			tr := testTransfer()
			fixture := newRPCFixture(tr)
			switch mode {
			case "null":
				fixture.receipt = nil
			case "malformed":
				fixture.receipt = map[string]any{}
			case "reverted":
				setFixtureMapValue(fixture.receipt, "status", "0x0")
			}
			checked, err := VerifyTransaction(t.Context(), fixture.pool(t), tr.TxHash)
			if err == nil {
				t.Fatal("expected verification error")
			}
			if errors.Is(err, ErrReceiptNotFound) != (mode == "null") {
				t.Fatalf("wrong absence classification: %v", err)
			}
			if errors.Is(err, ErrTransactionReverted) != (mode == "reverted") {
				t.Fatalf("wrong revert classification: %v", err)
			}
			if (checked != nil) != (mode == "reverted") {
				t.Fatal("failed mined receipts must remain available for canonical checks")
			}
		})
	}
}
