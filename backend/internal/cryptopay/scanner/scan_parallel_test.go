package scanner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
)

// pinConcurrency fixes scanBatchConcurrency for one test and restores it after.
func pinConcurrency(t *testing.T, workers int) {
	t.Helper()
	previous, previousPerEndpoint := scanBatchConcurrency, scanBatchesPerEndpoint
	// The per-node cap is tested on its own; here the worker count is the subject.
	scanBatchConcurrency, scanBatchesPerEndpoint = workers, 1<<20
	t.Cleanup(func() { scanBatchConcurrency, scanBatchesPerEndpoint = previous, previousPerEndpoint })
}

func TestScanWorkersAreCappedPerNodeTheChainCanRotateOver(t *testing.T) {
	client := &http.Client{}
	cases := []struct {
		name      string
		endpoints string
		want      int
	}{
		{"one public node", "http://a.invalid", 4},
		{"two nodes", "http://a.invalid,http://b.invalid", 8},
		{"seven nodes never exceed the global ceiling", "http://a.invalid,http://b.invalid,http://c.invalid,http://d.invalid,http://e.invalid,http://f.invalid,http://g.invalid", 8},
		{"blank entries are not nodes", " , ,http://a.invalid, ", 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scanner := NewEVMScanner(rpc.NewPoolWithHTTPClient(tc.endpoints, client))
			if got := scanner.scanWorkers(); got != tc.want {
				t.Fatalf("scanWorkers = %d, want %d", got, tc.want)
			}
		})
	}
	if got := (&EVMScanner{}).scanWorkers(); got != scanBatchConcurrency {
		t.Fatalf("a scanner without a pool uses the global value, got %d", got)
	}
	t.Cleanup(func(prev int) func() { return func() { scanBatchConcurrency = prev } }(scanBatchConcurrency))
	scanBatchConcurrency = 0
	if got := NewEVMScanner(rpc.NewPoolWithHTTPClient("http://a.invalid", client)).scanWorkers(); got != 1 {
		t.Fatalf("workers must never drop below one, got %d", got)
	}
}

// raiseTo lifts target to value if it is lower, without losing a concurrent raise.
func raiseTo(target *atomic.Int64, value int64) {
	for {
		current := target.Load()
		if value <= current || target.CompareAndSwap(current, value) {
			return
		}
	}
}

func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// batchFake is an in-process node answering eth_getLogs for address batches. A
// request is mapped to its batch by the first watched address, and a batch
// answers with exactly one transfer, so the right result of any scan is known.
type batchFake struct {
	t          *testing.T
	outgoing   bool
	recipients []string
	batchOf    map[string]int // padded watched topic -> batch index
	delay      func(batch int) time.Duration
	reject     func(batch int) int // 0 accepts, anything else is the JSON-RPC error code
	stop       chan struct{}
	url        string
	client     *http.Client
	pool       *rpc.Pool

	calls    atomic.Int64
	inflight atomic.Int64
	peak     atomic.Int64
	perBatch []atomic.Int64
}

func newBatchFake(t *testing.T, batches int, outgoing bool) *batchFake {
	t.Helper()
	f := &batchFake{t: t, outgoing: outgoing, recipients: splitRecipients(batches * batchSize),
		batchOf: make(map[string]int), stop: make(chan struct{}), perBatch: make([]atomic.Int64, batches)}
	for i, recipient := range f.recipients {
		f.batchOf[padTopic(recipient)] = i / batchSize
	}
	server := httptest.NewServer(http.HandlerFunc(f.serve))
	// Cleanups run last in, first out: handlers parked on stop are released
	// before Close waits for them.
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(f.stop) })
	f.url, f.client = server.URL, server.Client()
	f.pool = rpc.NewPoolWithHTTPClient(f.url, f.client)
	return f
}

func (f *batchFake) serve(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Method string          `json:"method"`
		Params []getLogsParams `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		// A client that is canceled mid-send leaves a truncated body. That is a
		// request that went away, not a malformed one, and the scan under test
		// is expected to cancel while requests are still being written.
		if !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) && r.Context().Err() == nil {
			f.t.Errorf("undecodable RPC request: %v", err)
		}
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if request.Method != "eth_getLogs" || len(request.Params) != 1 {
		f.t.Errorf("unexpected RPC request %q with %d params", request.Method, len(request.Params))
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	topics := request.Params[0].Topics
	if len(topics) == 0 || len(topics[len(topics)-1]) == 0 {
		f.t.Error("getLogs filter has no watched address")
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	first, _ := topics[len(topics)-1][0].(string)
	batch, known := f.batchOf[first]
	if !known {
		f.t.Errorf("getLogs filter names an unknown address %q", first)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	f.calls.Add(1)
	f.perBatch[batch].Add(1)
	raiseTo(&f.peak, f.inflight.Add(1))
	defer f.inflight.Add(-1)
	if f.delay != nil {
		timer := time.NewTimer(f.delay(batch))
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-r.Context().Done():
			return
		case <-f.stop:
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	if f.reject != nil {
		if code := f.reject(batch); code != 0 {
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1,
				"error": map[string]any{"code": code, "message": "fixture rejection"}})
			return
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1,
		"result": []evmLog{logFor(f.transferOf(batch))}})
}

// transferOf is the single transfer batch i holds. It is spelled out field by
// field rather than produced by the code under test.
func (f *batchFake) transferOf(batch int) Transfer {
	watched := f.recipients[batch*batchSize]
	other := "0x" + strings.Repeat("22", 20)
	transfer := Transfer{Chain: "bsc", TxHash: fmt.Sprintf("0x%064x", batch+1), LogIndex: batch,
		From: other, To: watched, Token: splitToken, Amount: big.NewInt(int64(1000 + batch)),
		BlockNumber: uint64(150 + batch), BlockHash: "0x" + strings.Repeat("55", 32)}
	if f.outgoing {
		transfer.From, transfer.To = watched, other
	}
	return transfer
}

// expected lists the transfers of every batch except the skipped ones, in batch order.
func (f *batchFake) expected(skip ...int) []Transfer {
	var out []Transfer
	for batch := range len(f.perBatch) {
		if !slices.Contains(skip, batch) {
			out = append(out, f.transferOf(batch))
		}
	}
	return out
}

func (f *batchFake) scan(ctx context.Context) ([]Transfer, error) {
	scanner := NewEVMScanner(f.pool)
	if f.outgoing {
		return scanner.ScanOutgoingRange(ctx, "bsc", 100, 200, []string{splitToken}, f.recipients)
	}
	return scanner.ScanRange(ctx, "bsc", 100, 200, []string{splitToken}, f.recipients)
}

// sameTransfers compares field by field, in order. Amount is compared by value.
func sameTransfers(got, want []Transfer) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		a, b := got[i], want[i]
		if a.Chain != b.Chain || a.TxHash != b.TxHash || a.LogIndex != b.LogIndex || a.From != b.From ||
			a.To != b.To || a.Token != b.Token || a.BlockNumber != b.BlockNumber || a.BlockHash != b.BlockHash ||
			a.Decimals != b.Decimals || a.Amount == nil || b.Amount == nil || a.Amount.Cmp(b.Amount) != 0 {
			return false
		}
	}
	return true
}

// joinedParts flattens one level of errors.Join, in order.
func joinedParts(err error) []error {
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		return joined.Unwrap()
	}
	return []error{err}
}

// failedBatches lists the Batch of every LogsError, in the order they were joined.
func failedBatches(t *testing.T, err error) []int {
	t.Helper()
	var batches []int
	for _, part := range joinedParts(err) {
		var logsErr *LogsError
		if !errors.As(part, &logsErr) {
			t.Fatalf("scan error part is not a LogsError: %v", part)
		}
		batches = append(batches, logsErr.Batch)
	}
	return batches
}

func TestScanRangeRunsBatchesConcurrentlyWithinTheBoundAndMatchesSerial(t *testing.T) {
	const batches = 14
	const latency = 50 * time.Millisecond
	fake := newBatchFake(t, batches, false)
	fake.delay = func(int) time.Duration { return latency }

	pinConcurrency(t, 1)
	start := time.Now()
	serial, err := fake.scan(t.Context())
	serialTime := time.Since(start)
	if err != nil || !sameTransfers(serial, fake.expected()) {
		t.Fatalf("serial scan is not the reference result: %v %+v", err, serial)
	}
	if fake.peak.Load() != 1 || serialTime < batches*latency {
		t.Fatalf("workers=1 did not run batches one at a time: peak=%d elapsed=%s", fake.peak.Load(), serialTime)
	}

	pinConcurrency(t, 8)
	fake.peak.Store(0)
	start = time.Now()
	parallel, err := fake.scan(t.Context())
	parallelTime := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if !sameTransfers(parallel, serial) {
		t.Fatalf("concurrent scan differs from the serial one:\n%+v\n%+v", parallel, serial)
	}
	if peak := fake.peak.Load(); peak < 4 || peak > int64(scanBatchConcurrency) {
		t.Fatalf("in-flight requests peaked at %d, want 4..%d", peak, scanBatchConcurrency)
	}
	// 14 batches in 2 waves of 50ms is ~100ms against >= 700ms serial.
	if parallelTime*2 > serialTime {
		t.Fatalf("concurrent scan was not clearly faster: serial=%s concurrent=%s", serialTime, parallelTime)
	}
}

func TestScanRangeResultOrderIgnoresCompletionOrder(t *testing.T) {
	const batches = 9
	for _, outgoing := range []bool{false, true} {
		for _, workers := range []int{1, 2, 3, 8, 32} {
			t.Run(fmt.Sprintf("outgoing_%t/workers_%d", outgoing, workers), func(t *testing.T) {
				fake := newBatchFake(t, batches, outgoing)
				// Earlier batches are slower, so they finish last.
				fake.delay = func(batch int) time.Duration { return time.Duration(batches-batch) * 4 * time.Millisecond }
				pinConcurrency(t, workers)
				got, err := fake.scan(t.Context())
				if err != nil || !sameTransfers(got, fake.expected()) {
					t.Fatalf("result is not in batch order: %v %+v", err, got)
				}
				if peak := fake.peak.Load(); peak > int64(workers) {
					t.Fatalf("%d requests in flight with %d workers", peak, workers)
				}
			})
		}
	}
}

func TestScanRangeKeepsHealthyBatchesWhenOneFails(t *testing.T) {
	const batches = 6
	for _, failing := range []int{0, 3, batches - 1} {
		for _, workers := range []int{1, 8} {
			t.Run(fmt.Sprintf("batch_%d/workers_%d", failing, workers), func(t *testing.T) {
				fake := newBatchFake(t, batches, false)
				fake.delay = func(int) time.Duration { return 2 * time.Millisecond }
				fake.reject = func(batch int) int {
					if batch == failing {
						return 429 // rate limits are never split, so exactly one call fails
					}
					return 0
				}
				pinConcurrency(t, workers)
				got, err := fake.scan(t.Context())
				var logsErr *LogsError
				var failure *rpc.Failure
				if err == nil || !errors.As(err, &logsErr) || logsErr.Batch != failing ||
					!errors.As(err, &failure) || failure.Kind != "remote" || failure.RPCCode != 429 {
					t.Fatalf("failed batch %d was not reported: %v", failing, err)
				}
				if !sameTransfers(got, fake.expected(failing)) {
					t.Fatalf("healthy batches were lost or reordered: %+v", got)
				}
				for batch := range batches {
					if calls := fake.perBatch[batch].Load(); calls != 1 {
						t.Fatalf("batch %d was requested %d times", batch, calls)
					}
				}
			})
		}
	}
}

func TestScanRangeJoinsErrorsInBatchOrderNotCompletionOrder(t *testing.T) {
	var serialText string
	for _, workers := range []int{1, 8} {
		t.Run(fmt.Sprintf("workers_%d", workers), func(t *testing.T) {
			fake := newBatchFake(t, 6, false)
			// Batch 4 fails long before batch 1 does.
			fake.delay = func(batch int) time.Duration {
				if batch == 1 {
					return 80 * time.Millisecond
				}
				return time.Millisecond
			}
			fake.reject = func(batch int) int {
				if batch == 1 || batch == 4 {
					return 429
				}
				return 0
			}
			pinConcurrency(t, workers)
			got, err := fake.scan(t.Context())
			if batches := failedBatches(t, err); !slices.Equal(batches, []int{1, 4}) {
				t.Fatalf("errors joined out of batch order: %v", batches)
			}
			if !sameTransfers(got, fake.expected(1, 4)) {
				t.Fatalf("healthy batches were lost or reordered: %+v", got)
			}
			if workers == 1 {
				serialText = err.Error()
			} else if err.Error() != serialText {
				t.Fatalf("concurrent error text differs from serial:\n%s\n%s", err, serialText)
			}
		})
	}
}

// batchWorkersRunning reports whether any goroutine is still inside runBatches,
// which is where the caller and every worker of a scan live.
func batchWorkersRunning() bool {
	buf := make([]byte, 4<<20)
	return strings.Contains(string(buf[:runtime.Stack(buf, true)]), "scanner.runBatches")
}

func TestScanRangeCancelStopsEveryWorkerAndReportsTheUnscannedRest(t *testing.T) {
	const workers = 8
	fake := newBatchFake(t, 20, false)
	fake.delay = func(int) time.Duration { return time.Hour } // parked until canceled
	pinConcurrency(t, workers)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	type outcome struct {
		got []Transfer
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		got, err := fake.scan(ctx)
		done <- outcome{got, err}
	}()
	waitFor(t, "every worker to have a request in flight", func() bool { return fake.inflight.Load() == workers })
	if !batchWorkersRunning() {
		t.Fatal("the leak check cannot see the running workers, so it proves nothing")
	}
	canceledAt := time.Now()
	cancel()

	var result outcome
	select {
	case result = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("scan did not return after cancel")
	}
	t.Logf("returned %s after cancel", time.Since(canceledAt))
	if !errors.Is(result.err, context.Canceled) || len(result.got) != 0 {
		t.Fatalf("canceled scan must fail with nothing to advance on: %v %+v", result.err, result.got)
	}
	if calls, peak := fake.calls.Load(), fake.peak.Load(); calls != workers || peak != workers {
		t.Fatalf("a batch started after cancel or the bound was exceeded: calls=%d peak=%d", calls, peak)
	}
	// Batches 0..7 were interrupted in flight; batch 8 is the first that never
	// started and must be named, or the range would look fully scanned.
	parts := joinedParts(result.err)
	if len(parts) != workers+1 {
		t.Fatalf("want %d in-flight failures plus one unscanned batch, got %d: %v", workers, len(parts), result.err)
	}
	var unscanned *LogsError
	var failure *rpc.Failure
	if !errors.As(parts[workers], &unscanned) || unscanned.Batch != workers || unscanned.Addresses != batchSize ||
		unscanned.FromBlock != 100 || unscanned.ToBlock != 200 ||
		!errors.As(parts[workers], &failure) || failure.Kind != "transport" {
		t.Fatalf("first unscanned batch is not reported: %v", parts[workers])
	}
	if batches := failedBatches(t, result.err); !slices.Equal(batches, []int{0, 1, 2, 3, 4, 5, 6, 7, 8}) {
		t.Fatalf("failures are not reported in batch order: %v", batches)
	}
	waitFor(t, "all batch workers to exit", func() bool { return !batchWorkersRunning() })
}

func TestScanRangeThatEndedBeforeStartSendsNothingAndLooksInterrupted(t *testing.T) {
	for _, kind := range []string{"canceled", "deadline"} {
		for _, workers := range []int{1, 8} {
			t.Run(fmt.Sprintf("%s/workers_%d", kind, workers), func(t *testing.T) {
				fake := newBatchFake(t, 3, false)
				pinConcurrency(t, workers)
				var ctx context.Context
				var cancel context.CancelFunc
				want := context.Canceled
				if kind == "deadline" {
					ctx, cancel = context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
					want = context.DeadlineExceeded
				} else {
					ctx, cancel = context.WithCancel(t.Context())
					cancel()
				}
				defer cancel()
				got, err := fake.scan(ctx)
				var logsErr *LogsError
				var failure *rpc.Failure
				if got != nil || !errors.Is(err, want) || !errors.As(err, &logsErr) || logsErr.Batch != 0 ||
					logsErr.Addresses != batchSize || !errors.As(err, &failure) || failure.Kind != "transport" {
					t.Fatalf("an ended context was not reported like an interrupted request: %v %+v", err, got)
				}
				if fake.calls.Load() != 0 || len(joinedParts(err)) != 1 || strings.Contains(err.Error(), "127.0.0.1") {
					t.Fatalf("an ended context still sent requests or leaked the endpoint: calls=%d err=%v", fake.calls.Load(), err)
				}
			})
		}
	}
}

// closeHookTransport calls onClose each time a response body is closed. That is
// the instant Pool.Call has read one reply in full and is about to return it, so
// canceling there ends the context exactly between two batches.
type closeHookTransport struct {
	base    http.RoundTripper
	onClose func()
}

func (h closeHookTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := h.base.RoundTrip(r)
	if err == nil {
		resp.Body = &closeHookBody{ReadCloser: resp.Body, onClose: h.onClose}
	}
	return resp, err
}

type closeHookBody struct {
	io.ReadCloser
	onClose func()
}

func (b *closeHookBody) Close() error {
	err := b.ReadCloser.Close()
	b.onClose()
	return err
}

// hookedPool routes the fake through a transport that runs onClose per reply.
func (f *batchFake) hookedPool(onClose func()) {
	client := &http.Client{Transport: closeHookTransport{base: f.client.Transport, onClose: onClose}}
	f.pool = rpc.NewPoolWithHTTPClient(f.url, client)
}

func TestScanRangeEndingBetweenBatchesNeverLooksComplete(t *testing.T) {
	fake := newBatchFake(t, 4, false)
	fake.recipients = fake.recipients[:3*batchSize+50] // the last batch is short
	pinConcurrency(t, 1)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var replies atomic.Int64
	fake.hookedPool(func() {
		if replies.Add(1) == 3 {
			cancel() // the third batch is complete, the fourth must not start
		}
	})
	got, err := fake.scan(ctx)
	var logsErr *LogsError
	if !errors.Is(err, context.Canceled) || !errors.As(err, &logsErr) || logsErr.Batch != 3 ||
		logsErr.Addresses != 50 || len(joinedParts(err)) != 1 {
		t.Fatalf("the unscanned last batch is not reported: %v", err)
	}
	if !sameTransfers(got, fake.expected(3)) || fake.calls.Load() != 3 {
		t.Fatalf("the finished batches must be returned and no fourth request sent: calls=%d %+v", fake.calls.Load(), got)
	}
}

// With several workers the number of batches that never start is up to timing,
// so this checks the invariant instead: every batch is either returned, reported
// as failed, or inside the unscanned tail the error names. Never silently lost.
func TestScanRangeEndingMidConcurrentScanAccountsForEveryBatch(t *testing.T) {
	const batches = 40
	for attempt := range 5 {
		t.Run(fmt.Sprint(attempt), func(t *testing.T) {
			fake := newBatchFake(t, batches, false)
			fake.delay = func(int) time.Duration { return 2 * time.Millisecond }
			pinConcurrency(t, 8)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var replies atomic.Int64
			fake.hookedPool(func() {
				if replies.Add(1) == 5 {
					cancel()
				}
			})
			got, err := fake.scan(ctx)

			scanned := make(map[int]bool)
			for _, transfer := range got {
				if transfer.LogIndex < 0 || transfer.LogIndex >= batches ||
					!sameTransfers([]Transfer{transfer}, []Transfer{fake.transferOf(transfer.LogIndex)}) {
					t.Fatalf("returned a transfer that no batch holds: %+v", transfer)
				}
				scanned[transfer.LogIndex] = true
			}
			failed, tail := make(map[int]bool), batches
			if err != nil {
				for _, batch := range failedBatches(t, err) {
					failed[batch] = true
				}
				parts := joinedParts(err)
				var last *LogsError
				if errors.As(parts[len(parts)-1], &last) {
					// An interrupted request wraps its failure; a batch that never ran does not.
					if _, direct := last.Err.(*rpc.Failure); direct {
						tail = last.Batch
					}
				}
			}
			for batch := range batches {
				switch {
				case batch >= tail && scanned[batch]:
					t.Fatalf("batch %d is past the unscanned tail yet was returned", batch)
				case batch < tail && scanned[batch] == failed[batch]:
					t.Fatalf("batch %d is neither returned nor reported, or both: %v", batch, err)
				}
			}
		})
	}
}

// panicTransport stands in for a bug deep in the HTTP stack.
type panicTransport struct{}

func (panicTransport) RoundTrip(*http.Request) (*http.Response, error) {
	panic("fixture transport bug")
}

func recovered(fn func()) (value any) {
	defer func() { value = recover() }()
	fn()
	return nil
}

func TestScanRangeWorkerPanicSurfacesOnTheCallerWithItsStack(t *testing.T) {
	pool := rpc.NewPoolWithHTTPClient("http://rpc.invalid", &http.Client{Transport: panicTransport{}})
	scan := func(recipients int) func() {
		return func() {
			_, _ = NewEVMScanner(pool).ScanRange(t.Context(), "bsc", 100, 200, []string{splitToken}, splitRecipients(recipients))
		}
	}
	pinConcurrency(t, 8)
	// One batch runs on the caller's goroutine, so its panic is untouched.
	if value := recovered(scan(1)); value != "fixture transport bug" {
		t.Fatalf("inline panic was altered: %v", value)
	}
	// Several batches run on workers: the panic must reach the caller anyway.
	value := recovered(scan(3 * batchSize))
	wrapped, ok := value.(*batchPanic)
	if !ok || wrapped.value != "fixture transport bug" || !strings.Contains(string(wrapped.stack), "panicTransport") ||
		!strings.Contains(wrapped.Error(), "fixture transport bug") {
		t.Fatalf("worker panic did not reach the caller with its stack: %#v", value)
	}
	waitFor(t, "all batch workers to exit", func() bool { return !batchWorkersRunning() })
}

func TestRunBatchesRunsEveryIndexOnceWithinTheWorkerBound(t *testing.T) {
	const n = 24
	for _, workers := range []int{-3, 0, 1, 2, 5, n, 100} {
		t.Run(fmt.Sprint(workers), func(t *testing.T) {
			var ran [n]atomic.Int64
			var active, peak atomic.Int64
			runBatches(t.Context(), n, workers, func(index int) {
				raiseTo(&peak, active.Add(1))
				time.Sleep(time.Millisecond)
				active.Add(-1)
				ran[index].Add(1)
			})
			for index := range ran {
				if ran[index].Load() != 1 {
					t.Fatalf("index %d ran %d times", index, ran[index].Load())
				}
			}
			if limit := int64(max(1, min(workers, n))); peak.Load() > limit {
				t.Fatalf("%d calls active with %d workers", peak.Load(), workers)
			}
		})
	}
	runBatches(t.Context(), 0, 8, func(int) { t.Fatal("ran a batch of an empty scan") })
}

func TestRunBatchesStopsClaimingAfterCancelAndLeavesAPrefix(t *testing.T) {
	const n, workers, cancelAt = 60, 8, 10
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	release := make(chan struct{})
	var mu sync.Mutex
	var ran []int
	runBatches(ctx, n, workers, func(index int) {
		mu.Lock()
		ran = append(ran, index)
		mu.Unlock()
		switch {
		case index == cancelAt:
			cancel()
			close(release)
		case index > cancelAt:
			<-release // parked until the cancel has happened
		}
	})
	slices.Sort(ran)
	for i, index := range ran {
		if index != i {
			t.Fatalf("batches that ran are not a prefix: %v", ran)
		}
	}
	// Every worker may claim at most one more index after the cancel.
	if len(ran) < cancelAt+1 || len(ran) > cancelAt+workers {
		t.Fatalf("%d batches ran, want %d..%d", len(ran), cancelAt+1, cancelAt+workers)
	}
}

func TestGetLogsBudgetNeverGoesNegativeUnderContention(t *testing.T) {
	for _, initial := range []int{0, 1, 5, maxGetLogsSplitRequests, 1001} {
		t.Run(fmt.Sprint(initial), func(t *testing.T) {
			budget := newGetLogsBudget(initial)
			start, stop := make(chan struct{}), make(chan struct{})
			var granted, negative atomic.Int64
			var observer, racers sync.WaitGroup
			observer.Go(func() {
				for {
					select {
					case <-stop:
						return
					default:
						if budget.remaining.Load() < 0 {
							negative.Add(1)
						}
						runtime.Gosched()
					}
				}
			})
			for range 64 {
				racers.Go(func() {
					<-start
					for budget.reserve(2) {
						granted.Add(1)
					}
				})
			}
			close(start)
			racers.Wait()
			close(stop)
			observer.Wait()
			if negative.Load() != 0 || granted.Load() != int64(initial/2) || budget.remaining.Load() != int64(initial%2) {
				t.Fatalf("budget %d: granted=%d remaining=%d negative=%d", initial, granted.Load(), budget.remaining.Load(), negative.Load())
			}
		})
	}
}

func TestScanRangeSharesOneSplitBudgetAcrossConcurrentBatches(t *testing.T) {
	const batches = 8
	for _, workers := range []int{1, 8} {
		t.Run(fmt.Sprintf("workers_%d", workers), func(t *testing.T) {
			fake := newBatchFake(t, batches, false)
			fake.reject = func(int) int { return -32005 } // "query exceeds max results", forever
			pinConcurrency(t, workers)
			got, err := fake.scan(t.Context())
			// Each batch could descend 10 times (7 block halvings, 3 address
			// halvings) but the whole scan may reserve only 64/2 = 32 splits. The
			// left half always fails again, so each reserve buys exactly one more
			// request: any other total means the budget was overspent or lost.
			if want := int64(batches + maxGetLogsSplitRequests/2); fake.calls.Load() != want {
				t.Fatalf("%d requests, want %d (one per batch plus the shared split budget)", fake.calls.Load(), want)
			}
			if got != nil || !slices.Equal(failedBatches(t, err), []int{0, 1, 2, 3, 4, 5, 6, 7}) {
				t.Fatalf("every batch must be reported failed, in order: %v %+v", err, got)
			}
		})
	}
}

func TestGetLogsAdaptiveSpendsBudgetOnlyWhenItSplits(t *testing.T) {
	for _, c := range []struct {
		name                string
		code                int
		from, to            uint64
		addresses, budget   int
		wantLeft, wantCalls int64
	}{
		{"rate_limit_is_never_split", 429, 100, 200, 100, 64, 64, 1},
		{"single_block_few_addresses_cannot_split", -32005, 100, 100, 25, 64, 64, 1},
		{"too_little_budget_for_two_halves", -32005, 100, 200, 100, 1, 1, 1},
		{"a_split_pays_for_both_halves_at_once", -32005, 100, 101, 25, 64, 62, 2},
	} {
		t.Run(c.name, func(t *testing.T) {
			fake := newBatchFake(t, 1, false)
			fake.reject = func(int) int { return c.code }
			budget := newGetLogsBudget(c.budget)
			_, err := NewEVMScanner(fake.pool).getLogsAdaptive(t.Context(), "bsc", c.from, c.to,
				[]string{splitToken}, fake.recipients[:c.addresses], false, 0, budget)
			if err == nil || budget.remaining.Load() != c.wantLeft || fake.calls.Load() != c.wantCalls {
				t.Fatalf("left=%d calls=%d err=%v, want left=%d calls=%d", budget.remaining.Load(), fake.calls.Load(), err, c.wantLeft, c.wantCalls)
			}
		})
	}
}

func TestLogsToTransfersKeepsOnlyWellFormedLogsOnTheWatchedSide(t *testing.T) {
	watched, other := "0x"+strings.Repeat("ab", 20), "0x"+strings.Repeat("cd", 20)
	hash := func(i int) string { return fmt.Sprintf("0x%064x", i) }
	blockHash := "0x" + strings.Repeat("55", 32)
	for _, outgoing := range []bool{false, true} {
		t.Run(fmt.Sprintf("outgoing_%t", outgoing), func(t *testing.T) {
			watchedTopic := 2 // the recipient; an outgoing scan watches the sender
			if outgoing {
				watchedTopic = 1
			}
			build := func(i int, watchedSide, farSide string) evmLog {
				from, to := farSide, watchedSide
				if outgoing {
					from, to = watchedSide, farSide
				}
				return logFor(Transfer{TxHash: hash(i), LogIndex: i, From: from, To: to, Token: splitToken,
					Amount: big.NewInt(int64(100 + i)), BlockNumber: uint64(150 + i), BlockHash: blockHash})
			}
			logs := []evmLog{
				build(1, watched, other), // kept
				build(2, other, watched), // the watched address is only on the far side
				build(3, watched, other), // reorged away
				build(4, watched, other), // topics cut short
				build(5, watched, other), // amount is not hex
				build(6, watched, other), // watched topic in capitals, still the same address
				build(7, other, other),   // neither side is watched
			}
			logs[2].Removed = true
			logs[3].Topics = logs[3].Topics[:2]
			logs[4].Data = "0xzz"
			logs[5].Topics[watchedTopic] = "0x" + strings.ToUpper(strings.TrimPrefix(logs[5].Topics[watchedTopic], "0x"))

			got := logsToTransfers("bsc", logs, map[string]struct{}{watched: {}}, outgoing)
			if len(got) != 2 || got[0].TxHash != hash(1) || got[1].TxHash != hash(6) {
				t.Fatalf("wrong logs kept: %+v", got)
			}
			want := Transfer{Chain: "bsc", TxHash: hash(1), LogIndex: 1, From: other, To: watched, Token: splitToken,
				Amount: big.NewInt(101), BlockNumber: 151, BlockHash: blockHash}
			if outgoing {
				want.From, want.To = watched, other
			}
			if !sameTransfers(got[:1], []Transfer{want}) {
				t.Fatalf("converted fields differ: %+v want %+v", got[0], want)
			}
		})
	}
}

// Outflow security must finish before any deposit log is fetched, so a deposit
// is never credited ahead of the check that could veto it. The scan itself is
// concurrent now, which makes this ordering worth pinning.
func TestMultiBatchDepositScanStartsOnlyAfterSecurityScanReturned(t *testing.T) {
	pinConcurrency(t, 8)
	fixture := newRPCFixture(testTransfer())
	fixture.logs = nil
	worker, mock, handler := workerFixture(t, fixture)
	recipients := splitRecipients(3 * batchSize)
	worker.addresses = func(context.Context, string) ([]string, error) { return recipients, nil }
	var logsBeforeGuard []int
	worker.SetSecurityGuards(nil, func(context.Context, *EVMScanner, *rpc.Pool, ChainConfig, uint64, uint64) error {
		before := fixture.callCount("eth_getLogs")
		logsBeforeGuard = append(logsBeforeGuard, before)
		time.Sleep(30 * time.Millisecond)
		if fixture.callCount("eth_getLogs") != before {
			t.Error("a deposit log request ran while the security scan was still running")
		}
		return nil
	})
	mock.ExpectQuery(`SELECT last_block`).WithArgs("bsc").WillReturnRows(sqlmock.NewRows([]string{"last_block"}).AddRow(100))
	mock.ExpectExec(`INSERT INTO crypto_scan_cursors`).WithArgs("bsc", int64(105)).WillReturnResult(sqlmock.NewResult(0, 1))
	if err := worker.ScanOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	// Head window, then the deposit range: neither had fetched a deposit log yet.
	if !slices.Equal(logsBeforeGuard, []int{0, 0}) || fixture.callCount("eth_getLogs") != 3 {
		t.Fatalf("security/deposit ordering broke: guard saw %v prior getLogs, total getLogs=%d",
			logsBeforeGuard, fixture.callCount("eth_getLogs"))
	}
	if len(handler.pending)+len(handler.processed) != 0 {
		t.Fatal("an empty scan produced a payment")
	}
}

func TestUnscannedBatchErrorFailsClosedEvenWithoutAContextError(t *testing.T) {
	err := unscannedBatchError(t.Context(), "bsc", 100, 200, 2, 2*batchSize+7)
	var logsErr *LogsError
	if !errors.Is(err, errBatchNotScanned) || !errors.As(err, &logsErr) || logsErr.Batch != 2 || logsErr.Addresses != 7 {
		t.Fatalf("an unscanned batch must always be an error: %v", err)
	}
}
