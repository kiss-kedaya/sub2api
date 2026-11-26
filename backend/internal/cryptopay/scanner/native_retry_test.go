package scanner

import (
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
)

// blockNode serves eth_getBlockByNumber and counts the requests per block. fail
// decides, from the block number and how many times it has been asked, whether this
// request is answered with the given HTTP status instead of the block.
type blockNode struct {
	mu     sync.Mutex
	hits   map[uint64]int
	blocks map[uint64]string
	fail   func(block uint64, attempt int) int
}

func newBlockNode(t *testing.T, blocks map[uint64]string, fail func(block uint64, attempt int) int) (*blockNode, *rpc.Pool) {
	t.Helper()
	node := &blockNode{hits: map[uint64]int{}, blocks: blocks, fail: fail}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Params []json.RawMessage `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		var tag string
		_ = json.Unmarshal(request.Params[0], &tag)
		height, _ := new(big.Int).SetString(strings.TrimPrefix(tag, "0x"), 16)
		block := height.Uint64()
		node.mu.Lock()
		node.hits[block]++
		attempt := node.hits[block]
		node.mu.Unlock()
		if status := node.fail(block, attempt); status != 0 {
			http.Error(w, "rate limit exceeded", status)
			return
		}
		result, ok := node.blocks[block]
		if !ok {
			result = `{"jsonrpc":"2.0","id":1,"result":null}`
		}
		_, _ = w.Write([]byte(result))
	}))
	t.Cleanup(server.Close)
	return node, rpc.NewPoolWithHTTPClient(server.URL, server.Client())
}

func (n *blockNode) hitsOf(block uint64) int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.hits[block]
}

func quickBatchRetries(t *testing.T) {
	t.Helper()
	old := batchRetryDelay
	batchRetryDelay = func(int) time.Duration { return time.Millisecond }
	t.Cleanup(func() { batchRetryDelay = old })
}

func tenNativeBlocks(t *testing.T) map[uint64]string {
	t.Helper()
	blocks := map[uint64]string{}
	for number := uint64(100); number <= 109; number++ {
		blocks[number] = nativeBlockResponse(t, number, nativeTestBlock, []map[string]any{
			nativeTxEntry(nativeTestHashA, nativeSender, nativeDeposit, false, big.NewInt(int64(number))),
		})
	}
	return blocks
}

// One block answered 429 once: it alone is asked again. The nine blocks that
// answered are not downloaded a second time, and the window completes.
func TestNativeScanRetriesOnlyTheBlocksThatFailed(t *testing.T) {
	quickBatchRetries(t)
	node, pool := newBlockNode(t, tenNativeBlocks(t), func(block uint64, attempt int) int {
		if block == 105 && attempt == 1 {
			return http.StatusTooManyRequests
		}
		return 0
	})
	transfers, err := NewEVMScanner(pool).ScanNativeRange(t.Context(), "bsc", 100, 109, []string{nativeDeposit})
	if err != nil {
		t.Fatalf("a window with one throttled block must complete: %v", err)
	}
	if len(transfers) != 10 {
		t.Fatalf("transfers = %d, want one per block (10)", len(transfers))
	}
	for i, transfer := range transfers { // assembled in block order, as before
		if transfer.BlockNumber != uint64(100+i) {
			t.Fatalf("transfer %d is for block %d, want %d", i, transfer.BlockNumber, 100+i)
		}
	}
	for block := uint64(100); block <= 109; block++ {
		want := 1
		if block == 105 {
			want = 2
		}
		if got := node.hitsOf(block); got != want {
			t.Errorf("block %d was requested %d times, want %d", block, got, want)
		}
	}
}

// A block that keeps failing is retried a bounded number of times, the window
// then fails (the cursor must not move over it), and the blocks that answered were
// still read only once.
func TestNativeScanGivesUpOnABlockThatKeepsFailing(t *testing.T) {
	quickBatchRetries(t)
	node, pool := newBlockNode(t, tenNativeBlocks(t), func(block uint64, attempt int) int {
		if block == 103 {
			return http.StatusTooManyRequests
		}
		return 0
	})
	transfers, err := NewEVMScanner(pool).ScanNativeRange(t.Context(), "bsc", 100, 109, []string{nativeDeposit})
	if err == nil {
		t.Fatal("a window with a block that never answered must not report success")
	}
	if rpc.CategoryOf(err) != rpc.CategoryTransport {
		t.Fatalf("category = %s, want the 429 (transport)", rpc.CategoryOf(err))
	}
	if got, want := node.hitsOf(103), 1+maxBatchRetryRounds; got != want {
		t.Fatalf("the failing block was requested %d times, want %d (1 + %d retry rounds)", got, want, maxBatchRetryRounds)
	}
	for _, block := range []uint64{100, 101, 102, 104, 109} {
		if got := node.hitsOf(block); got != 1 {
			t.Errorf("block %d was requested %d times, want 1", block, got)
		}
	}
	if len(transfers) != 9 {
		t.Fatalf("the blocks that answered are still returned (%d), as before", len(transfers))
	}
}

// Nothing answered: an outage or a spent quota. Retrying inside the window would
// only lengthen the pass and spend more requests; the next pass decides.
func TestNativeScanDoesNotRetryWhenNothingAnswered(t *testing.T) {
	quickBatchRetries(t)
	node, pool := newBlockNode(t, tenNativeBlocks(t), func(uint64, int) int { return http.StatusTooManyRequests })
	if _, err := NewEVMScanner(pool).ScanNativeRange(t.Context(), "bsc", 100, 109, []string{nativeDeposit}); err == nil {
		t.Fatal("expected the window to fail")
	}
	for block := uint64(100); block <= 109; block++ {
		if got := node.hitsOf(block); got != 1 {
			t.Errorf("block %d was requested %d times, want 1: no retry when nothing answered", block, got)
		}
	}
}

// An answer that is not a usable block (null: a node that cannot serve the height)
// is not a rate limit; asking again would fail the same way.
func TestNativeScanDoesNotRetryAnUnusableAnswer(t *testing.T) {
	quickBatchRetries(t)
	blocks := tenNativeBlocks(t)
	delete(blocks, 104)
	node, pool := newBlockNode(t, blocks, func(uint64, int) int { return 0 })
	if _, err := NewEVMScanner(pool).ScanNativeRange(t.Context(), "bsc", 100, 109, []string{nativeDeposit}); err == nil {
		t.Fatal("a block answered with null must fail the window")
	}
	if got := node.hitsOf(104); got != 1 {
		t.Fatalf("block 104 was requested %d times, want 1", got)
	}
}

// Many windows at once with random throttling: no race, every window ends.
func TestNativeScanRetriesAreSafeUnderConcurrency(t *testing.T) {
	quickBatchRetries(t)
	_, pool := newBlockNode(t, tenNativeBlocks(t), func(block uint64, attempt int) int {
		if attempt == 1 && block%3 == 0 {
			return http.StatusTooManyRequests
		}
		return 0
	})
	scanner := NewEVMScanner(pool)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := scanner.ScanNativeRange(t.Context(), "bsc", 100, 109, []string{nativeDeposit}); err != nil {
				t.Errorf("window failed: %v", err)
			}
		}()
	}
	wg.Wait()
}
