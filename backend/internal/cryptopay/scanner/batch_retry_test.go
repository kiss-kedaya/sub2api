package scanner

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
)

// retryNode is a node that answers eth_getLogs per address batch from a script.
// The batch is identified by the first address in the filter's third topic.
type retryNode struct {
	t        *testing.T
	mu       sync.Mutex
	attempts map[int]int // batch index -> requests seen
	script   func(batch, attempt int) (status int, body string)
	inflight atomic.Int64
	peak     map[int]int64 // attempt number -> highest concurrent requests with that attempt number
}

func newRetryNode(t *testing.T, recipients []string, script func(batch, attempt int) (int, string)) (*retryNode, *rpc.Pool) {
	node := &retryNode{t: t, attempts: map[int]int{}, peak: map[int]int64{}, script: script}
	first := map[string]int{}
	for i := 0; i < len(recipients); i += batchSize {
		first["0x"+strings.Repeat("0", 24)+strings.ToLower(strings.TrimPrefix(recipients[i], "0x"))] = i / batchSize
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if req.Method == "eth_blockNumber" {
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x6e"}`))
			return
		}
		var filter struct {
			Topics [][]any `json:"topics"`
		}
		_ = json.Unmarshal(req.Params[0], &filter)
		batch := -1
		if len(filter.Topics) > 2 && len(filter.Topics[2]) > 0 {
			if topic, ok := filter.Topics[2][0].(string); ok {
				batch = first[topic]
			}
		}
		node.mu.Lock()
		node.attempts[batch]++
		attempt := node.attempts[batch]
		node.mu.Unlock()
		now := node.inflight.Add(1)
		node.mu.Lock()
		if now > node.peak[attempt] {
			node.peak[attempt] = now
		}
		node.mu.Unlock()
		time.Sleep(15 * time.Millisecond) // let concurrent requests overlap
		node.inflight.Add(-1)
		status, body := script(batch, attempt)
		if status != http.StatusOK {
			w.WriteHeader(status)
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return node, rpc.NewPoolWithHTTPClient(server.URL, server.Client())
}

func (n *retryNode) attemptsOf(batch int) int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.attempts[batch]
}

func (n *retryNode) total() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	sum := 0
	for _, v := range n.attempts {
		sum += v
	}
	return sum
}

const okEmptyLogs = `{"jsonrpc":"2.0","id":1,"result":[]}`

func retryRecipients(batches int) []string {
	out := make([]string, batches*batchSize)
	for i := range out {
		out[i] = fmt.Sprintf("0x%040x", i+1)
	}
	return out
}

func noRetryDelay(t *testing.T) {
	t.Helper()
	old := batchRetryDelay
	batchRetryDelay = func(int) time.Duration { return time.Millisecond }
	t.Cleanup(func() { batchRetryDelay = old })
}

func scanAllBatches(t *testing.T, ctx context.Context, pool *rpc.Pool, recipients []string) error {
	t.Helper()
	tr := testTransfer()
	_, err := NewEVMScanner(pool).ScanRange(ctx, "bsc", 1, 100, []string{tr.Token}, recipients)
	return err
}

// The incident in one test: a few of 13 batches are rate limited. They must be
// re-run on their own, and the range must complete.
func TestScanRangeReRunsOnlyTheRateLimitedBatches(t *testing.T) {
	noRetryDelay(t)
	recipients := retryRecipients(13)
	limited := map[int]bool{2: true, 7: true, 11: true}
	node, pool := newRetryNode(t, recipients, func(batch, attempt int) (int, string) {
		if limited[batch] && attempt == 1 {
			return http.StatusTooManyRequests, `{"jsonrpc":"2.0","id":null,"error":{"code":-32005,"message":"rate limit exceeded"}}`
		}
		return http.StatusOK, okEmptyLogs
	})
	if err := scanAllBatches(t, t.Context(), pool, recipients); err != nil {
		t.Fatalf("the rate limited batches must succeed on retry: %v", err)
	}
	for batch := 0; batch < 13; batch++ {
		want := 1
		if limited[batch] {
			want = 2
		}
		if got := node.attemptsOf(batch); got != want {
			t.Errorf("batch %d was requested %d times, want %d (only failed batches may be re-run)", batch, got, want)
		}
	}
}

// A node that never stops rate limiting must not be hammered: first attempt plus
// maxBatchRetryRounds, only for the failing batch, and the failure is reported.
func TestScanRangeRetriesAreBoundedAndTheFailureStillSurfaces(t *testing.T) {
	noRetryDelay(t)
	recipients := retryRecipients(5)
	node, pool := newRetryNode(t, recipients, func(batch, attempt int) (int, string) {
		if batch == 3 {
			return http.StatusTooManyRequests, `{"error":{"message":"Too many requests, slow down"}}`
		}
		return http.StatusOK, okEmptyLogs
	})
	err := scanAllBatches(t, t.Context(), pool, recipients)
	if err == nil {
		t.Fatal("a batch that never succeeds must fail the range")
	}
	if !rpc.IsTransient(err) {
		t.Fatalf("the rate limit must stay visible to the caller: %v", err)
	}
	if got := node.attemptsOf(3); got != 1+maxBatchRetryRounds {
		t.Fatalf("failing batch was requested %d times, want %d", got, 1+maxBatchRetryRounds)
	}
	for _, batch := range []int{0, 1, 2, 4} {
		if got := node.attemptsOf(batch); got != 1 {
			t.Errorf("healthy batch %d was requested %d times", batch, got)
		}
	}
}

// An exhausted daily allowance or prepaid balance fails the same way every time;
// asking again only spends requests. (A malformed answer is answered by the
// pre-existing range/address splitting, not by these retry rounds, and is covered
// by the classification table below.)
func TestScanRangeDoesNotRetryAnExhaustedQuota(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
	}{
		"daily quota":   {http.StatusTooManyRequests, `{"jsonrpc":"2.0","id":1,"error":{"code":-32003,"message":"daily request limit reached"}}`},
		"prepaid empty": {http.StatusServiceUnavailable, `{"error":{"code":10007,"message":"Insufficient RU balance"}}`},
	} {
		t.Run(name, func(t *testing.T) {
			noRetryDelay(t)
			recipients := retryRecipients(2)
			node, pool := newRetryNode(t, recipients, func(batch, attempt int) (int, string) {
				if batch == 1 {
					return tc.status, tc.body
				}
				return http.StatusOK, okEmptyLogs
			})
			if err := scanAllBatches(t, t.Context(), pool, recipients); err == nil {
				t.Fatal("expected the failure to surface")
			}
			if got := node.attemptsOf(1); got > 2 { // one pool round may try the node twice; never a retry round
				t.Fatalf("a failure that cannot change was retried: %d requests", got)
			}
		})
	}
}

// Each retry round puts fewer requests in flight than the one before: the burst is
// what the node rejected, so the second try must not be the same burst.
func TestScanRangeRetryRoundsHalveTheConcurrency(t *testing.T) {
	noRetryDelay(t)
	old := scanBatchConcurrency
	scanBatchConcurrency = 8
	t.Cleanup(func() { scanBatchConcurrency = old })
	recipients := retryRecipients(8)
	node, pool := newRetryNode(t, recipients, func(batch, attempt int) (int, string) {
		if batch != 0 && attempt <= 2 { // batch 0 always answers; the rest fail twice, then recover
			return http.StatusTooManyRequests, `{"error":{"message":"rate limit exceeded"}}`
		}
		return http.StatusOK, okEmptyLogs
	})
	// 1 endpoint -> scanWorkers = min(8, 1*4) = 4
	if err := scanAllBatches(t, t.Context(), pool, recipients); err != nil {
		t.Fatalf("the third attempt succeeds: %v", err)
	}
	node.mu.Lock()
	defer node.mu.Unlock()
	if node.peak[1] < 2 {
		t.Fatalf("setup: the first round must run batches concurrently, peak=%d", node.peak[1])
	}
	if node.peak[2] > node.peak[1]/2+1 || node.peak[3] > node.peak[2]/2+1 {
		t.Fatalf("each retry round must use about half the previous concurrency: peaks by attempt = %v", node.peak)
	}
}

// Cancelling during the wait must stop the retries promptly and report the
// unfinished batches as errors, never as scanned.
func TestScanRangeRetryWaitHonoursCancellation(t *testing.T) {
	old := batchRetryDelay
	batchRetryDelay = func(int) time.Duration { return 30 * time.Second }
	t.Cleanup(func() { batchRetryDelay = old })
	recipients := retryRecipients(3)
	_, pool := newRetryNode(t, recipients, func(batch, attempt int) (int, string) {
		if batch == 1 {
			return http.StatusTooManyRequests, `{"error":{"message":"rate limit exceeded"}}`
		}
		return http.StatusOK, okEmptyLogs
	})
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := scanAllBatches(t, ctx, pool, recipients)
	if err == nil {
		t.Fatal("an unfinished batch must fail the range")
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("cancellation did not interrupt the retry wait: %v", took)
	}
}

func TestRetryableBatchFailureClassification(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"http 429", &rpc.Failure{Kind: "http", HTTPStatus: 429}, true},
		{"http 503", &rpc.Failure{Kind: "http", HTTPStatus: 503}, true},
		{"timeout", rpc.NewTransportFailure(context.DeadlineExceeded), true},
		{"connection", &rpc.Failure{Kind: "transport"}, true},
		{"json-rpc rate limited", &rpc.Failure{Kind: "remote", RPCCode: -32005, Reason: "rate limited"}, true},
		{"quota exhausted 429", &rpc.Failure{Kind: "http", HTTPStatus: 429, Quota: true}, false},
		{"canceled", rpc.NewTransportFailure(context.Canceled), false},
		{"invalid response", &rpc.Failure{Kind: "response"}, false},
		{"range too wide", &rpc.Failure{Kind: "remote", RPCCode: -32602, Reason: rpc.ReasonRangeLimit}, false},
		{"auth", &rpc.Failure{Kind: "http", HTTPStatus: 401}, false},
		{"plain error", fmt.Errorf("boom"), false},
		{"wrapped in LogsError", &LogsError{Chain: "polygon", Err: &rpc.Failure{Kind: "http", HTTPStatus: 429}}, true},
	} {
		if got := retryableBatchFailure(tc.err); got != tc.want {
			t.Errorf("%s: retryableBatchFailure = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// When EVERY batch of a range fails there is no evidence the pool is merely
// throttling a burst: it is down, or its allowance is gone. Waiting inside the call
// would only stretch the pass (which holds the scan lock), so the range fails at
// once and the next pass / the chain's backoff handle it.
func TestScanRangeDoesNotRetryWhenNothingAnswered(t *testing.T) {
	noRetryDelay(t)
	recipients := retryRecipients(6)
	node, pool := newRetryNode(t, recipients, func(batch, attempt int) (int, string) {
		return http.StatusTooManyRequests, `{"error":{"message":"rate limit exceeded"}}`
	})
	start := time.Now()
	if err := scanAllBatches(t, t.Context(), pool, recipients); err == nil {
		t.Fatal("a range where every batch failed must fail")
	}
	if got := node.total(); got != 6 {
		t.Fatalf("each batch must be asked exactly once when none answered, got %d requests", got)
	}
	if took := time.Since(start); took > time.Second {
		t.Fatalf("a wholly failing range must fail fast, took %v", took)
	}
	// the very same range with ONE healthy batch is retried
	node2, pool2 := newRetryNode(t, recipients, func(batch, attempt int) (int, string) {
		if batch == 0 {
			return http.StatusOK, okEmptyLogs
		}
		return http.StatusTooManyRequests, `{"error":{"message":"rate limit exceeded"}}`
	})
	_ = scanAllBatches(t, t.Context(), pool2, recipients)
	if got := node2.attemptsOf(1); got != 1+maxBatchRetryRounds {
		t.Fatalf("with one batch answering, the failing batches are retried: batch 1 requested %d times, want %d", got, 1+maxBatchRetryRounds)
	}
}
