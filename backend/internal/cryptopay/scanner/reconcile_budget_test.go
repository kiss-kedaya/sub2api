package scanner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
)

type receiptBudgetFixture struct {
	mu       sync.Mutex
	stalled  string
	requests []string
	good     Transfer
}

func (f *receiptBudgetFixture) pool(t *testing.T) *rpc.Pool {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var input struct {
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		if json.NewDecoder(request.Body).Decode(&input) != nil {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		good := f.good
		f.mu.Unlock()
		var result any
		switch input.Method {
		case "eth_chainId":
			result = "0x38"
		case "eth_blockNumber":
			result = "0x6e"
		case "eth_getTransactionReceipt":
			var hash string
			if len(input.Params) != 1 || json.Unmarshal(input.Params[0], &hash) != nil {
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
			f.mu.Lock()
			f.requests = append(f.requests, hash)
			stall := hash == f.stalled
			f.mu.Unlock()
			if stall {
				<-request.Context().Done()
				return
			}
			result = map[string]any{"status": "0x1", "blockNumber": fmt.Sprintf("0x%x", good.BlockNumber),
				"blockHash": good.BlockHash, "transactionHash": good.TxHash, "logs": []evmLog{logFor(good)}}
		case "eth_getBlockByNumber":
			var tag string
			if len(input.Params) == 0 || json.Unmarshal(input.Params[0], &tag) != nil {
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
			if tag == "finalized" || tag == "safe" {
				result = map[string]any{"number": "0x6e"}
			} else {
				result = map[string]any{"number": fmt.Sprintf("0x%x", good.BlockNumber), "hash": good.BlockHash}
			}
		default:
			t.Errorf("unexpected RPC: %s", input.Method)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": result})
	}))
	t.Cleanup(server.Close)
	return rpc.NewPoolWithHTTPClient(server.URL, server.Client())
}

func (f *receiptBudgetFixture) requestOrder() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.requests...)
}

func TestPendingReconcileSlowFirstReceiptRotatesAndCreditsNextRow(t *testing.T) {
	good, slow := testTransfer(), testTransfer()
	slow.TxHash = "0x" + strings.Repeat("66", 32)
	fixture := &receiptBudgetFixture{stalled: slow.TxHash, good: good}
	worker, mock, handler := workerFixture(t, newRPCFixture(good))
	worker.pools["bsc"] = fixture.pool(t)
	// The first row is older. A blocked receipt used to consume the entire
	// pass, cancel its UPDATE too, and remain first on every subsequent pass.
	mock.ExpectQuery(`(?s)WHERE status = 'pending'.*ORDER BY COALESCE\(last_attempt_at, created_at\), id.*LIMIT 50`).
		WillReturnRows(observationRows().
			AddRow(slow.Chain, slow.TxHash, slow.LogIndex, slow.From, slow.To, slow.Token, "1", slow.BlockNumber, slow.BlockHash, "").
			AddRow(good.Chain, good.TxHash, good.LogIndex, good.From, good.To, good.Token, "1", good.BlockNumber, good.BlockHash, ""))
	expectObservationUpdate(mock, slow, "pending", "", "pending", "").
		WillDelayFor(20 * time.Millisecond).WillReturnResult(sqlmock.NewResult(0, 1))
	expectObservationUpdate(mock, good, "pending", "", "pending", "").WillReturnResult(sqlmock.NewResult(0, 1))
	started := time.Now()
	if err := worker.retryPending(t.Context()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("timed-out receipt failure was hidden", err)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatal("one receipt consumed the whole reconciliation budget", elapsed)
	}
	if len(handler.processed) != 1 || handler.processed[0].TxHash != good.TxHash || len(handler.pending) != 0 {
		t.Fatal("a timed-out observation was credited or prevented the verified next payment", handler.processed)
	}
	if got := fixture.requestOrder(); len(got) != 2 || got[0] != slow.TxHash || got[1] != good.TxHash {
		t.Fatal("both rows did not receive real receipt verification", got)
	}
	// The persisted attempt makes this older never-attempted row eligible
	// before the stalled row on the next ordered DB page. No status changed
	// for the stalled row and no payment was fabricated just to rotate it.
	third := good
	third.TxHash = "0x" + strings.Repeat("77", 32)
	fixture.mu.Lock()
	fixture.good = third
	fixture.mu.Unlock()
	mock.ExpectQuery(`(?s)WHERE status = 'pending'.*ORDER BY COALESCE\(last_attempt_at, created_at\), id.*LIMIT 50`).
		WillReturnRows(observationRows().AddRow(third.Chain, third.TxHash, third.LogIndex, third.From, third.To, third.Token, "1", third.BlockNumber, third.BlockHash, ""))
	expectObservationUpdate(mock, third, "pending", "", "pending", "").WillReturnResult(sqlmock.NewResult(0, 1))
	if err := worker.retryPending(t.Context()); err != nil {
		t.Fatal("next ordered page could not make progress", err)
	}
	if len(handler.processed) != 2 || handler.processed[1].TxHash != third.TxHash {
		t.Fatal("rotation dropped the next real payment", handler.processed)
	}
}

func TestCreditedReconcileTimeoutReservesCASWithoutReorgOrCredit(t *testing.T) {
	transfer := testTransfer()
	fixture := &receiptBudgetFixture{stalled: transfer.TxHash, good: transfer}
	worker, mock, handler := workerFixture(t, newRPCFixture(transfer))
	worker.pools["bsc"] = fixture.pool(t)
	notifications := 0
	worker.SetReorgNotifier(func(string, string) { notifications++ })
	const oldReason = missingReceiptPrefix + "2"
	expectObservation(mock, transfer, "1", oldReason)
	expectObservationUpdate(mock, transfer, "credited", oldReason, "credited", missingReceiptPrefix+"0").
		WillDelayFor(20 * time.Millisecond).WillReturnResult(sqlmock.NewResult(0, 1))
	// A shorter caller deadline exercises the end-of-pass reserve, instead
	// of only the ordinary two-second row cap. The CAS must still complete.
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := worker.recheckCredited(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("inconclusive timeout was not returned", err)
	}
	if err := ctx.Err(); err != nil {
		t.Fatal("RPC consumed the persistence reserve", err)
	}
	if notifications != 0 || len(handler.processed) != 0 || len(handler.pending) != 0 {
		t.Fatal("timeout caused a false reorg notification or balance operation")
	}
	if got := fixture.requestOrder(); len(got) != 1 || got[0] != transfer.TxHash {
		t.Fatal("a fresh check time was written without actually attempting the receipt", got)
	}
}

func TestReconcileVerifiedPaymentStillRequiresObservationCAS(t *testing.T) {
	transfer := testTransfer()
	worker, mock, handler := workerFixture(t, newRPCFixture(transfer))
	expectObservation(mock, transfer, "1", "")
	expectObservationUpdate(mock, transfer, "pending", "", "pending", "").WillReturnResult(sqlmock.NewResult(0, 0))
	if err := worker.retryPending(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(handler.processed) != 0 || len(handler.pending) != 0 {
		t.Fatal("a changed observation was fulfilled after a failed compare-and-swap")
	}
}

func TestReconcileChainProbeKeepsHealthyPeerDespiteStalledEndpoint(t *testing.T) {
	transfer := testTransfer()
	fixture := newRPCFixture(transfer)
	worker, mock, handler := workerFixture(t, fixture)
	stalled := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		<-request.Context().Done()
	}))
	t.Cleanup(stalled.Close)
	healthy := worker.pools["bsc"]
	worker.pools["bsc"] = healthy.WithEndpoints(stalled.URL + "," + strings.Join(healthy.Endpoints(), ","))
	expectObservation(mock, transfer, "1", "")
	expectObservationUpdate(mock, transfer, "pending", "", "pending", "").WillReturnResult(sqlmock.NewResult(0, 1))
	if err := worker.retryPending(t.Context()); err != nil {
		t.Fatal("per-receipt budget discarded a healthy chain endpoint", err)
	}
	if len(handler.processed) != 1 || handler.processed[0].TxHash != transfer.TxHash || fixture.callCount("eth_getTransactionReceipt") != 1 {
		t.Fatal("verified healthy peer did not perform the real receipt check and fulfillment")
	}
}
