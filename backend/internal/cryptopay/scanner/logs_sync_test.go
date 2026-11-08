package scanner

import (
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
)

// syncNode is a JSON-RPC node with an explicit head. A node behind the scanned
// range answers eth_getLogs with only the blocks it has, so an empty array from
// it is indistinguishable from a range that really has no transfers.
type syncNode struct {
	server    *httptest.Server
	head      uint64
	logs      []evmLog
	headCalls atomic.Int64
	logCalls  atomic.Int64
}

func newSyncNode(t *testing.T, head uint64, logs []evmLog) *syncNode {
	t.Helper()
	node := &syncNode{head: head, logs: logs}
	node.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("undecodable RPC request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch request.Method {
		case "eth_blockNumber":
			node.headCalls.Add(1)
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":1,"result":"0x%x"}`, node.head)
		case "eth_getLogs":
			node.logCalls.Add(1)
			logs := node.logs
			if logs == nil {
				logs = []evmLog{}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": logs})
		case "eth_chainId":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x38"}`))
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	t.Cleanup(node.server.Close)
	return node
}

func syncRecipients(count int) []string {
	out := make([]string, count)
	for i := range out {
		out[i] = fmt.Sprintf("0x%040x", i+1)
	}
	return out
}

func nodePool(nodes ...*syncNode) *rpc.Pool {
	urls := make([]string, len(nodes))
	for i, node := range nodes {
		urls[i] = node.server.URL
	}
	return rpc.NewPoolWithHTTPClient(strings.Join(urls, ","), nodes[0].server.Client())
}

func TestScanRangeRejectsAnEmptyAnswerFromABehindNode(t *testing.T) {
	behind := newSyncNode(t, 100, nil)
	synced := newSyncNode(t, 0xffffffff, nil)
	pool := nodePool(behind, synced)

	transfers, err := NewEVMScanner(pool).ScanRange(t.Context(), "bsc", 100, 200,
		[]string{splitToken}, syncRecipients(1))
	if err == nil || len(transfers) != 0 {
		t.Fatalf("a behind node's empty answer must be an error, got transfers=%d err=%v", len(transfers), err)
	}
	if !strings.Contains(err.Error(), "behind") {
		t.Fatalf("error must name the stale endpoint condition, got %v", err)
	}
	if behind.logCalls.Load() != 1 || behind.headCalls.Load() != 1 {
		t.Fatalf("behind node was not probed exactly once: logs=%d heads=%d",
			behind.logCalls.Load(), behind.headCalls.Load())
	}

	// The same range on the caught-up node is a complete, transfer-free scan.
	transfers, err = NewEVMScanner(pool).ScanRange(t.Context(), "bsc", 100, 200,
		[]string{splitToken}, syncRecipients(1))
	if err != nil || len(transfers) != 0 {
		t.Fatalf("caught-up empty answer was not accepted: transfers=%d err=%v", len(transfers), err)
	}
	if synced.logCalls.Load() != 1 {
		t.Fatalf("rotation did not move to the synced node: calls=%d", synced.logCalls.Load())
	}
}

func TestScanRangeAcceptsEmptyAnswerFromSyncedNode(t *testing.T) {
	node := newSyncNode(t, 0xffffffff, nil)
	pool := nodePool(node)
	transfers, err := NewEVMScanner(pool).ScanRange(t.Context(), "bsc", 100, 200,
		[]string{splitToken}, syncRecipients(1))
	if err != nil || len(transfers) != 0 {
		t.Fatalf("synced empty answer rejected: transfers=%d err=%v", len(transfers), err)
	}
	if node.headCalls.Load() != 1 {
		t.Fatalf("empty answer must prove the head once, got %d", node.headCalls.Load())
	}
}

func TestScanRangeDoesNotProbeHeadWhenLogsAreReturned(t *testing.T) {
	watched := "0x" + strings.Repeat("33", 20)
	tr := Transfer{Chain: "bsc", TxHash: "0x" + strings.Repeat("11", 32), LogIndex: 1,
		From: "0x" + strings.Repeat("22", 20), To: watched, Token: splitToken,
		Amount: big.NewInt(500), BlockNumber: 150, BlockHash: "0x" + strings.Repeat("55", 32)}
	node := newSyncNode(t, 0xffffffff, []evmLog{logFor(tr)})
	pool := nodePool(node)

	transfers, err := NewEVMScanner(pool).ScanRange(t.Context(), "bsc", 100, 200,
		[]string{splitToken}, []string{watched})
	if err != nil || len(transfers) != 1 {
		t.Fatalf("non-empty answer was not accepted: transfers=%d err=%v", len(transfers), err)
	}
	if node.headCalls.Load() != 0 {
		t.Fatalf("a non-empty answer names real blocks and must not cost a head probe, got %d", node.headCalls.Load())
	}
}

func TestSyncWitnessProbesAnEndpointOncePerScanRange(t *testing.T) {
	pinConcurrency(t, 1)
	node := newSyncNode(t, 0xffffffff, nil)
	pool := nodePool(node)
	// 401 recipients is two address batches, so the empty result is judged twice.
	if _, err := NewEVMScanner(pool).ScanRange(t.Context(), "bsc", 100, 200,
		[]string{splitToken}, syncRecipients(batchSize+1)); err != nil {
		t.Fatal(err)
	}
	if calls := node.headCalls.Load(); calls != 1 {
		t.Fatalf("head was probed %d times for one scan range, want once", calls)
	}
	if calls := node.logCalls.Load(); calls != 2 {
		t.Fatalf("expected two getLogs batches, got %d", calls)
	}
}

// The cursor is the security boundary for later credit: a range that only a
// behind node claims to have covered must not be written down as scanned.
func TestCursorDoesNotAdvancePastAnUntrustedEmptyRange(t *testing.T) {
	tr := testTransfer()
	behind := newSyncNode(t, 100, nil)
	syncedHead := newSyncNode(t, 0x6e, nil) // head 110, 5 confirmations: [101,105]

	fixture := newRPCFixture(tr)
	w, mock, _ := workerFixture(t, fixture)
	w.pools["bsc"] = nodePool(syncedHead, behind) // head call first, getLogs second
	mock.ExpectQuery(`SELECT last_block`).WithArgs("bsc").
		WillReturnRows(sqlmock.NewRows([]string{"last_block"}).AddRow(100))

	err := w.scanChainFromSnapshot(t.Context(), w.chains[0], 0, &chainHeadSnapshot{pool: w.pools["bsc"]})
	if err == nil || !strings.Contains(err.Error(), "behind") {
		t.Fatalf("untrusted empty range must fail the pass, got %v", err)
	}
	if behind.logCalls.Load() != 1 || syncedHead.logCalls.Load() != 0 {
		t.Fatalf("expected the behind node to serve getLogs: behind=%d synced=%d",
			behind.logCalls.Load(), syncedHead.logCalls.Load())
	}
}

func TestCursorAdvancesAfterAnEmptyRangeFromASyncedNode(t *testing.T) {
	tr := testTransfer()
	synced := newSyncNode(t, 0x6e, nil)
	fixture := newRPCFixture(tr)
	w, mock, _ := workerFixture(t, fixture)
	w.pools["bsc"] = nodePool(synced)
	mock.ExpectQuery(`SELECT last_block`).WithArgs("bsc").
		WillReturnRows(sqlmock.NewRows([]string{"last_block"}).AddRow(100))
	mock.ExpectExec(`INSERT INTO crypto_scan_cursors`).WithArgs("bsc", int64(105)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := w.scanChainFromSnapshot(t.Context(), w.chains[0], 0, &chainHeadSnapshot{pool: w.pools["bsc"]}); err != nil {
		t.Fatalf("verified empty range failed: %v", err)
	}
}
