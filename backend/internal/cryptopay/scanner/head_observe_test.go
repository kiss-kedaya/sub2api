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

func headTransfer() Transfer {
	tr := testTransfer()
	tr.BlockNumber = 108 // Above confirmed = 110 - 5: only head observation sees it.
	return tr
}

func TestObserveHeadWindowBounds(t *testing.T) {
	if got := observeHeadCap(5, 2000); got != 5 {
		t.Fatalf("cap=%d", got)
	}
	if got := observeHeadCap(1000, 2000); got != 256 {
		t.Fatalf("cap=%d", got)
	}
	if got := observeHeadCap(1000, 100); got != 100 {
		t.Fatalf("cap=%d", got)
	}
	if got := confirmationsAt(110, 108, 5); got != 2 {
		t.Fatalf("confirmations=%d", got)
	}
	if got := confirmationsAt(108, 108, 5); got != 0 {
		t.Fatalf("inclusion block is not a descendant confirmation, got %d", got)
	}
	if got := confirmationsAt(110, 101, 5); got != 5 {
		t.Fatalf("confirmations must cap at requirement, got %d", got)
	}
	if got := confirmationsAt(100, 108, 5); got != 0 {
		t.Fatalf("lagging head must read as zero, got %d", got)
	}
}

func TestHeadObservationRecordsPendingOnlyAndCountsConfirmations(t *testing.T) {
	tr := headTransfer()
	fixture := newRPCFixture(tr)
	w, mock, handler := workerFixture(t, fixture)
	mock.ExpectExec(`(?s)UPDATE deposit_transactions SET confirmations = \$1.*status = 'pending'`).
		WithArgs(int64(2), "bsc", tr.TxHash, tr.LogIndex).WillReturnResult(sqlmock.NewResult(0, 1))
	if err := w.observeHead(t.Context(), w.pools["bsc"], w.chains[0], 110); err != nil {
		t.Fatal(err)
	}
	if len(handler.pending) != 1 || len(handler.processed) != 0 {
		t.Fatalf("head observation must only record pending: pending=%d processed=%d", len(handler.pending), len(handler.processed))
	}
	fixture.mu.Lock()
	ranges := append([]getLogsParams(nil), fixture.logRanges...)
	fixture.mu.Unlock()
	// head 110, 5 confirmations: confirmed 105, window [106, 110].
	if len(ranges) != 1 || ranges[0].FromBlock != "0x6a" || ranges[0].ToBlock != "0x6e" {
		t.Fatalf("unexpected head window: %+v", ranges)
	}
}

func TestHeadObservationPersistsSuccessfulBatchesDespiteRateLimit(t *testing.T) {
	tr := headTransfer()
	recipients := splitRecipients(batchSize + 1)
	recipients[0] = tr.To
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Method != "eth_getLogs" {
			t.Errorf("invalid RPC request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls == 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": []evmLog{logFor(tr)}})
	}))
	t.Cleanup(server.Close)

	w, mock, handler := workerFixture(t, newRPCFixture(tr))
	w.pools["bsc"] = rpc.NewPoolWithHTTPClient(server.URL, server.Client())
	w.addresses = func(context.Context, string) ([]string, error) { return recipients, nil }
	mock.ExpectExec(`(?s)UPDATE deposit_transactions SET confirmations = \$1.*status = 'pending'`).
		WithArgs(int64(2), "bsc", tr.TxHash, tr.LogIndex).WillReturnResult(sqlmock.NewResult(0, 1))
	err := w.observeHead(t.Context(), w.pools["bsc"], w.chains[0], 110)
	if err == nil || calls != 2 {
		t.Fatalf("expected one partial scan error without retry storm: calls=%d err=%v", calls, err)
	}
	if len(handler.pending) != 1 || len(handler.processed) != 0 {
		t.Fatalf("successful batch must be displayed as pending only: pending=%d credited=%d", len(handler.pending), len(handler.processed))
	}
}

func TestManualScanSkipsHeadObservation(t *testing.T) {
	tr := testTransfer()
	fixture := newRPCFixture(tr)
	w, mock, handler := workerFixture(t, fixture)
	w.liveProgress = true
	mock.ExpectQuery(`SELECT last_block`).WithArgs("bsc").WillReturnRows(sqlmock.NewRows([]string{"last_block"}).AddRow(100))
	mock.ExpectExec(`INSERT INTO crypto_scan_cursors`).WithArgs("bsc", int64(105)).WillReturnResult(sqlmock.NewResult(0, 1))
	if err := w.scanChainFrom(t.Context(), w.chains[0], 101); err != nil {
		t.Fatal(err)
	}
	if fixture.callCount("eth_getLogs") != 1 || len(handler.pending)+len(handler.processed) != 1 {
		t.Fatalf("manual scan ran head observation: getLogs=%d", fixture.callCount("eth_getLogs"))
	}
}

func TestHeadObservationRunsBeforeSecurityScan(t *testing.T) {
	tr := headTransfer()
	fixture := newRPCFixture(tr)
	w, mock, handler := workerFixture(t, fixture)
	w.liveProgress = true
	securityErr := errors.New("security window unavailable")
	w.SetSecurityGuards(nil, func(context.Context, *EVMScanner, *rpc.Pool, ChainConfig, uint64, uint64) error {
		if fixture.callCount("eth_getLogs") == 0 {
			t.Fatal("security scan ran before the low-latency deposit observation")
		}
		return securityErr
	})
	mock.ExpectExec(`(?s)UPDATE deposit_transactions SET confirmations = \$1.*status = 'pending'`).
		WithArgs(int64(2), "bsc", tr.TxHash, tr.LogIndex).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT last_block`).WithArgs("bsc").
		WillReturnRows(sqlmock.NewRows([]string{"last_block"}).AddRow(105))
	err := w.scanChainFrom(t.Context(), w.chains[0], 0)
	if !errors.Is(err, securityErr) {
		t.Fatalf("security failure was lost: %v", err)
	}
	if len(handler.pending) != 1 || len(handler.processed) != 0 {
		t.Fatalf("head observation must persist pending only: pending=%d credited=%d", len(handler.pending), len(handler.processed))
	}
}

// A head-observed pending row is final by depth, but its block was never in
// a security-scanned range. It may be credited only once a security scan of
// this pass covered it.
func TestHeadObservedPendingWaitsForSecurityScan(t *testing.T) {
	for _, securityOK := range []bool{false, true} {
		t.Run(map[bool]string{false: "head_security_failed", true: "head_security_ok"}[securityOK], func(t *testing.T) {
			tr := headTransfer()
			fixture := newRPCFixture(tr)
			w, mock, handler := workerFixture(t, fixture)
			incident := errors.New("security RPC unavailable")
			w.SetSecurityGuards(nil, func(context.Context, *EVMScanner, *rpc.Pool, ChainConfig, uint64, uint64) error {
				if securityOK {
					return nil
				}
				return incident
			})
			// Cursor is caught up at 105, below the observed block 108.
			mock.ExpectQuery(`SELECT last_block`).WithArgs("bsc").WillReturnRows(sqlmock.NewRows([]string{"last_block"}).AddRow(105))
			err := w.scanChainFrom(t.Context(), w.chains[0], 0)
			if securityOK != (err == nil) {
				t.Fatalf("scan err=%v", err)
			}
			fixture.head = "0x71" // 113: block 108 now has the full five-block depth.
			expectObservation(mock, tr, "1", "")
			expectObservationUpdate(mock, tr, "pending", "", "pending", "").WillReturnResult(sqlmock.NewResult(0, 1))
			if err := w.retryPending(t.Context()); err != nil {
				t.Fatal(err)
			}
			if got := len(handler.processed); (got == 1) != securityOK || got > 1 {
				t.Fatalf("credit calls=%d securityOK=%t", got, securityOK)
			}
		})
	}
}

func TestPendingAtOrBelowDurableCursorIsCovered(t *testing.T) {
	tr := testTransfer() // Block 101.
	fixture := newRPCFixture(tr)
	w, mock, handler := workerFixture(t, fixture)
	w.SetSecurityGuards(nil, func(context.Context, *EVMScanner, *rpc.Pool, ChainConfig, uint64, uint64) error { return nil })
	expectObservation(mock, tr, "1", "")
	expectObservationUpdate(mock, tr, "pending", "", "pending", "").WillReturnResult(sqlmock.NewResult(0, 1))
	if err := w.retryPending(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(handler.processed) != 0 {
		t.Fatal("pending credited without any security coverage in this pass")
	}
	w.noteCursor("bsc", 105)
	expectObservation(mock, tr, "1", "")
	expectObservationUpdate(mock, tr, "pending", "", "pending", "").WillReturnResult(sqlmock.NewResult(0, 1))
	if err := w.retryPending(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(handler.processed) != 1 {
		t.Fatal("pending below the durable cursor was not credited")
	}
}

// ledgerFake mirrors the ledger contract: one row per identity, credit only
// on a pending -> credited transition, repeated calls are no-ops.
type ledgerFake struct {
	mu      sync.Mutex
	status  map[string]string
	block   map[string]uint64
	balance *big.Int
}

func (l *ledgerFake) key(tr Transfer) string {
	return fmt.Sprintf("%s|%s|%d", tr.Chain, strings.ToLower(tr.TxHash), tr.LogIndex)
}

func (l *ledgerFake) ProcessPending(_ context.Context, tr Transfer, _ int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.status[l.key(tr)]; !ok {
		l.status[l.key(tr)], l.block[l.key(tr)] = "pending", tr.BlockNumber
	}
	return nil
}

func (l *ledgerFake) Process(_ context.Context, tr Transfer, _ int) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	switch l.status[l.key(tr)] {
	case "credited":
		return false, nil
	case "", "pending":
		l.status[l.key(tr)] = "credited"
		l.balance.Add(l.balance, tr.Amount)
		return true, nil
	}
	return false, nil
}

func (l *ledgerFake) HasRecordedTransfer(_ context.Context, tr Transfer, _ int) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, ok := l.status[l.key(tr)]
	return ok && l.block[l.key(tr)] == tr.BlockNumber, nil
}

func TestSameTransferCreditsOnceAcrossObserveCursorAndRetry(t *testing.T) {
	tr := headTransfer()
	fixture := newRPCFixture(tr)
	w, mock, _ := workerFixture(t, fixture)
	led := &ledgerFake{status: map[string]string{}, block: map[string]uint64{}, balance: new(big.Int)}
	w.handler = led
	w.liveProgress = true
	w.SetSecurityGuards(nil, func(context.Context, *EVMScanner, *rpc.Pool, ChainConfig, uint64, uint64) error { return nil })

	// 1. Head observation: pending only.
	mock.ExpectExec(`(?s)UPDATE deposit_transactions SET confirmations = \$1.*status = 'pending'`).
		WithArgs(int64(2), "bsc", tr.TxHash, tr.LogIndex).WillReturnResult(sqlmock.NewResult(0, 1))
	if err := w.observeHead(t.Context(), w.pools["bsc"], w.chains[0], 110); err != nil {
		t.Fatal(err)
	}
	// 2. Cursor scan reaches the same final transfer: already recorded, skipped.
	fixture.head = "0x71"
	w.markSecurityCovered("bsc", 1, 113)
	if err := w.processTransfer(t.Context(), w.pools["bsc"], w.chains[0], tr, 6); err != nil {
		t.Fatal(err)
	}
	if led.balance.Sign() != 0 {
		t.Fatal("cursor scan credited a transfer the pending queue owns")
	}
	// 3. retryPending credits it once and writes the full counter.
	expectObservation(mock, tr, "1", "")
	expectObservationUpdate(mock, tr, "pending", "", "pending", "").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`(?s)UPDATE deposit_transactions SET confirmations = \$1.*status = 'credited'`).
		WithArgs(int64(5), "bsc", tr.TxHash, tr.LogIndex).WillReturnResult(sqlmock.NewResult(0, 1))
	if err := w.retryPending(t.Context()); err != nil {
		t.Fatal(err)
	}
	// 4. Every path runs again: no second credit.
	mock.ExpectQuery(`FROM deposit_transactions`).WillReturnRows(observationRows())
	if err := w.retryPending(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := w.processTransfer(t.Context(), w.pools["bsc"], w.chains[0], tr, 6); err != nil {
		t.Fatal(err)
	}
	if err := w.observeHead(t.Context(), w.pools["bsc"], w.chains[0], 113); err != nil {
		t.Fatal(err)
	}
	if credited, _ := led.Process(t.Context(), tr, 6); credited {
		t.Fatal("ledger credited an already credited transfer")
	}
	if led.balance.Cmp(tr.Amount) != 0 {
		t.Fatalf("balance=%s want exactly one credit of %s", led.balance, tr.Amount)
	}
}
