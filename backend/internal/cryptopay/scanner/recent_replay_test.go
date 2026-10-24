package scanner

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
)

type replayStateArgument struct{ want recentReplayState }

func TestZeroValueTransferDoesNotConsumeVerificationBudget(t *testing.T) {
	fixture := newRPCFixture(testTransfer())
	worker, _, handler := workerFixture(t, fixture)
	transfer := testTransfer()
	transfer.Amount = big.NewInt(0)
	if err := worker.processTransfer(t.Context(), worker.pools["bsc"], worker.chains[0], transfer, 6); err != nil {
		t.Fatal(err)
	}
	if fixture.callCount("eth_getTransactionReceipt") != 0 || len(handler.processed)+len(handler.pending) != 0 {
		t.Fatal("a zero-value event consumed payment verification or persistence work")
	}
}

func (a replayStateArgument) Match(value driver.Value) bool {
	encoded, ok := value.(string)
	if !ok {
		return false
	}
	var got recentReplayState
	return json.Unmarshal([]byte(encoded), &got) == nil && got == a.want
}

func expectReplaySave(mock sqlmock.Sqlmock, state recentReplayState) *sqlmock.ExpectedExec {
	return mock.ExpectExec(`INSERT INTO crypto_scan_cursors`).
		WithArgs(recentReplayPrefix+"bsc", int64(state.Next-1), replayStateArgument{state})
}

func expectReplayLoad(t *testing.T, mock sqlmock.Sqlmock, state *recentReplayState, updated time.Time) {
	t.Helper()
	rows := sqlmock.NewRows([]string{"last_signature", "updated_at"})
	if state != nil {
		encoded, err := json.Marshal(state)
		if err != nil {
			t.Fatal(err)
		}
		rows.AddRow(string(encoded), updated)
	}
	mock.ExpectQuery(`SELECT last_signature, updated_at`).WithArgs(recentReplayPrefix + "bsc").WillReturnRows(rows)
}

func TestRecentReplayFailureDoesNotBlockForwardCursor(t *testing.T) {
	fixture := newRPCFixture(testTransfer())
	worker, mock, handler := workerFixture(t, fixture)
	worker.recentReplay = true
	failed := errors.New("injected replay deposit persistence failure")
	handler.processErr = failed
	mock.ExpectQuery(`SELECT last_block`).WithArgs("bsc").
		WillReturnRows(sqlmock.NewRows([]string{"last_block"}).AddRow(102))
	expectReplayLoad(t, mock, nil, time.Time{})
	// Pin the old overlap before allowing the ordinary cursor to move.
	expectReplaySave(mock, recentReplayState{Through: 102, Next: 1, After: -1}).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO crypto_scan_cursors`).WithArgs("bsc", int64(105)).WillReturnResult(sqlmock.NewResult(0, 1))
	expectReplaySave(mock, recentReplayState{Through: 102, Next: 51, After: -1}).WillReturnResult(sqlmock.NewResult(0, 1))
	expectReplaySave(mock, recentReplayState{Through: 102, Next: 101, After: -1}).WillReturnResult(sqlmock.NewResult(0, 1))
	if err := worker.scanChain(t.Context(), worker.chains[0]); !errors.Is(err, failed) {
		t.Fatal("overlap failure was hidden or stopped the forward range first", err)
	}
	if len(handler.processed) != 1 || !worker.replayedAt["bsc"].IsZero() {
		t.Fatal("failed overlap was lost or marked complete")
	}
	// A replacement instance must retain the unfinished 102 boundary even
	// though the ordinary cursor is now 105.
	resumed, resumedMock, resumedHandler := workerFixture(t, newRPCFixture(testTransfer()))
	resumed.recentReplay = true
	resumedHandler.processErr = failed
	resumedMock.ExpectQuery(`SELECT last_block`).WithArgs("bsc").
		WillReturnRows(sqlmock.NewRows([]string{"last_block"}).AddRow(105))
	expectReplayLoad(t, resumedMock, &recentReplayState{Through: 102, Next: 101, After: -1}, time.Now())
	if err := resumed.scanChain(t.Context(), resumed.chains[0]); !errors.Is(err, failed) || len(resumedHandler.processed) != 1 {
		t.Fatal("worker replacement skipped unfinished overlap", err)
	}
}

func TestRecentReplayInitialCheckpointFailurePreventsForwardProgress(t *testing.T) {
	fixture := newRPCFixture(testTransfer())
	worker, mock, handler := workerFixture(t, fixture)
	worker.recentReplay = true
	failed := errors.New("overlap checkpoint unavailable")
	mock.ExpectQuery(`SELECT last_block`).WithArgs("bsc").
		WillReturnRows(sqlmock.NewRows([]string{"last_block"}).AddRow(102))
	expectReplayLoad(t, mock, nil, time.Time{})
	expectReplaySave(mock, recentReplayState{Through: 102, Next: 1, After: -1}).WillReturnError(failed)
	if err := worker.scanChain(t.Context(), worker.chains[0]); !errors.Is(err, failed) {
		t.Fatal("forward work started without a durable overlap boundary", err)
	}
	if fixture.callCount("eth_getLogs") != 0 || len(handler.processed)+len(handler.pending) != 0 {
		t.Fatal("checkpoint failure reached unprotected scanning or payment work")
	}
}

func TestRecentReplayTimeoutHasAnIndependentBudgetAfterForwardProgress(t *testing.T) {
	fixture := newRPCFixture(testTransfer())
	worker, mock, _ := workerFixture(t, fixture)
	worker.recentReplay = true
	var forwardDeadline, replayDeadline time.Time
	worker.SetSecurityGuards(nil, func(ctx context.Context, _ *EVMScanner, _ *rpc.Pool, _ ChainConfig, from, to uint64) error {
		if from == 103 {
			forwardDeadline, _ = ctx.Deadline()
		}
		if from == 1 && to == 50 {
			replayDeadline, _ = ctx.Deadline()
			return context.DeadlineExceeded
		}
		return nil
	})
	mock.ExpectQuery(`SELECT last_block`).WithArgs("bsc").WillReturnRows(sqlmock.NewRows([]string{"last_block"}).AddRow(102))
	expectReplayLoad(t, mock, nil, time.Time{})
	expectReplaySave(mock, recentReplayState{Through: 102, Next: 1, After: -1}).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO crypto_scan_cursors`).WithArgs("bsc", int64(105)).WillReturnResult(sqlmock.NewResult(0, 1))
	if err := worker.scanChain(t.Context(), worker.chains[0]); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("replay timeout was hidden or prevented forward completion", err)
	}
	if forwardDeadline.IsZero() || replayDeadline.IsZero() || !replayDeadline.Before(forwardDeadline) {
		t.Fatal("replay inherited the forward deadline instead of its bounded independent budget")
	}
}

func TestRecentReplayWalksTheGapInsteadOfJumpingToNewForwardCursor(t *testing.T) {
	worker, mock, _ := workerFixture(t, newRPCFixture(testTransfer()))
	previous := recentReplayState{Through: 100, Next: 101, After: -1}
	expectReplayLoad(t, mock, &previous, time.Now().Add(-time.Minute))
	want := recentReplayState{Through: 200, Next: 1, After: -1}
	expectReplaySave(mock, want).WillReturnResult(sqlmock.NewResult(0, 1))
	state, err := worker.prepareRecentReplay(t.Context(), "bsc", 10000, 100, []string{testTransfer().To})
	if err != nil || state == nil || *state != want {
		t.Fatal("lagging overlap jumped over late-indexed payments after forward progress", state, err)
	}
}

func denseReplayFixture(count int) *rpcFixture {
	base := testTransfer()
	fixture := newRPCFixture(base)
	fixture.logs = make([]evmLog, 0, count)
	for i := count - 1; i >= 0; i-- {
		tr := base
		tr.LogIndex = i
		fixture.logs = append(fixture.logs, logFor(tr))
	}
	setFixtureMapValue(fixture.receipt, "logs", fixture.logs)
	return fixture
}

func TestRecentReplayDenseSingleBlockResumesAcrossWorkers(t *testing.T) {
	const count = recentReplayTransfersPerPass + 17
	fixture := denseReplayFixture(count)
	first, firstMock, firstHandler := workerFixture(t, fixture)
	initial := recentReplayState{Through: 101, Next: 101, After: -1}
	expectReplayLoad(t, firstMock, nil, time.Time{})
	expectReplaySave(firstMock, initial).WillReturnResult(sqlmock.NewResult(0, 1))
	state, err := first.prepareRecentReplay(t.Context(), "bsc", 101, 1, []string{testTransfer().To})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < recentReplayTransfersPerPass; i++ {
		expectReplaySave(firstMock, recentReplayState{Through: 101, Next: 101, After: i, Hash: testTransfer().BlockHash}).
			WillReturnResult(sqlmock.NewResult(0, 1))
	}
	pool := first.pools["bsc"]
	if err := first.replayRecentRange(t.Context(), NewEVMScanner(pool), pool, first.chains[0], state, 1, []string{testTransfer().To}); err != nil {
		t.Fatal(err)
	}
	if len(firstHandler.processed) != recentReplayTransfersPerPass || state.After != recentReplayTransfersPerPass-1 || state.Next != 101 {
		t.Fatal("dense block did not yield at a durable intra-block checkpoint", len(firstHandler.processed), state)
	}
	if !first.replayedAt["bsc"].IsZero() {
		t.Fatal("a yielded window was throttled as though complete")
	}

	second, secondMock, secondHandler := workerFixture(t, fixture)
	durable := *state
	expectReplayLoad(t, secondMock, &durable, time.Now())
	// Forward progress to 500 must not replace this unfinished block 101.
	resumed, err := second.prepareRecentReplay(t.Context(), "bsc", 500, 1, []string{testTransfer().To})
	if err != nil || resumed == nil || *resumed != durable {
		t.Fatal("replacement worker discarded dense-block checkpoint", resumed, err)
	}
	for i := recentReplayTransfersPerPass; i < count; i++ {
		expectReplaySave(secondMock, recentReplayState{Through: 101, Next: 101, After: i, Hash: testTransfer().BlockHash}).
			WillReturnResult(sqlmock.NewResult(0, 1))
	}
	expectReplaySave(secondMock, recentReplayState{Through: 101, Next: 102, After: -1}).WillReturnResult(sqlmock.NewResult(0, 1))
	pool = second.pools["bsc"]
	if err := second.replayRecentRange(t.Context(), NewEVMScanner(pool), pool, second.chains[0], resumed, 1, []string{testTransfer().To}); err != nil {
		t.Fatal(err)
	}
	all := append(firstHandler.processed, secondHandler.processed...)
	if len(all) != count {
		t.Fatal("dense single block lost or repeated transfers", len(all))
	}
	for i, transfer := range all {
		if transfer.LogIndex != i {
			t.Fatal("out-of-order RPC logs skipped or duplicated a transfer", i, transfer.LogIndex)
		}
	}
	if resumed.Next != 102 || time.Since(second.replayedAt["bsc"]) > time.Second {
		t.Fatal("completed dense block was not recorded or throttled")
	}
}

func TestRecentReplayCheckpointSaveFailureCannotSkipPersistedTransfer(t *testing.T) {
	fixture := denseReplayFixture(2)
	first, firstMock, firstHandler := workerFixture(t, fixture)
	durable := recentReplayState{Through: 101, Next: 101, After: -1}
	state := durable
	failed := errors.New("checkpoint save failed after deposit persistence")
	expectReplaySave(firstMock, recentReplayState{Through: 101, Next: 101, After: 0, Hash: testTransfer().BlockHash}).WillReturnError(failed)
	pool := first.pools["bsc"]
	if err := first.replayRecentRange(t.Context(), NewEVMScanner(pool), pool, first.chains[0], &state, 1, []string{testTransfer().To}); !errors.Is(err, failed) {
		t.Fatal("checkpoint save failure was hidden", err)
	}
	if len(firstHandler.processed) != 1 || firstHandler.processed[0].LogIndex != 0 {
		t.Fatal("replay crossed a failed checkpoint")
	}
	second, secondMock, secondHandler := workerFixture(t, fixture)
	expectReplayLoad(t, secondMock, &durable, time.Now())
	resumed, err := second.prepareRecentReplay(t.Context(), "bsc", 500, 1, []string{testTransfer().To})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		expectReplaySave(secondMock, recentReplayState{Through: 101, Next: 101, After: i, Hash: testTransfer().BlockHash}).WillReturnResult(sqlmock.NewResult(0, 1))
	}
	expectReplaySave(secondMock, recentReplayState{Through: 101, Next: 102, After: -1}).WillReturnResult(sqlmock.NewResult(0, 1))
	pool = second.pools["bsc"]
	if err := second.replayRecentRange(t.Context(), NewEVMScanner(pool), pool, second.chains[0], resumed, 1, []string{testTransfer().To}); err != nil {
		t.Fatal(err)
	}
	if len(secondHandler.processed) != 2 || secondHandler.processed[0].LogIndex != 0 {
		t.Fatal("failed checkpoint advanced the next worker past an uncheckpointed log")
	}
}

func TestRecentReplayReorgResetsLogCheckpointBeforeProcessing(t *testing.T) {
	for _, saveFails := range []bool{false, true} {
		t.Run(map[bool]string{false: "replacement block replays every log", true: "reset must persist first"}[saveFails], func(t *testing.T) {
			fixture := denseReplayFixture(1)
			worker, mock, handler := workerFixture(t, fixture)
			state := recentReplayState{Through: 101, Next: 101, After: 7, Hash: "0x" + strings.Repeat("66", 32)}
			reset := expectReplaySave(mock, recentReplayState{Through: 101, Next: 101, After: -1})
			failed := errors.New("reorg reset cannot be saved")
			if saveFails {
				reset.WillReturnError(failed)
			} else {
				reset.WillReturnResult(sqlmock.NewResult(0, 1))
				expectReplaySave(mock, recentReplayState{Through: 101, Next: 101, After: 0, Hash: testTransfer().BlockHash}).WillReturnResult(sqlmock.NewResult(0, 1))
				expectReplaySave(mock, recentReplayState{Through: 101, Next: 102, After: -1}).WillReturnResult(sqlmock.NewResult(0, 1))
			}
			pool := worker.pools["bsc"]
			err := worker.replayRecentRange(t.Context(), NewEVMScanner(pool), pool, worker.chains[0], &state, 1, []string{testTransfer().To})
			if saveFails {
				if !errors.Is(err, failed) || fixture.callCount("eth_getLogs") != 0 || len(handler.processed) != 0 {
					t.Fatal("reorg checkpoint failure reached unsafe replay", err)
				}
			} else if err != nil || len(handler.processed) != 1 || handler.processed[0].LogIndex != 0 {
				t.Fatal("old block's log checkpoint skipped replacement block's lower log indices", err)
			}
		})
	}
}

func TestRecentReplayCompletionIsThrottledAcrossWorkerReplacement(t *testing.T) {
	fixture := newRPCFixture(testTransfer())
	worker, mock, handler := workerFixture(t, fixture)
	worker.recentReplay = true
	worker.maxRange = 100
	mock.ExpectQuery(`SELECT last_block`).WithArgs("bsc").WillReturnRows(sqlmock.NewRows([]string{"last_block"}).AddRow(105))
	expectReplayLoad(t, mock, nil, time.Time{})
	expectReplaySave(mock, recentReplayState{Through: 105, Next: 6, After: -1}).WillReturnResult(sqlmock.NewResult(0, 1))
	expectReplaySave(mock, recentReplayState{Through: 105, Next: 56, After: -1}).WillReturnResult(sqlmock.NewResult(0, 1))
	expectReplaySave(mock, recentReplayState{Through: 105, Next: 101, After: 7, Hash: testTransfer().BlockHash}).WillReturnResult(sqlmock.NewResult(0, 1))
	expectReplaySave(mock, recentReplayState{Through: 105, Next: 106, After: -1}).WillReturnResult(sqlmock.NewResult(0, 1))
	if err := worker.scanChain(t.Context(), worker.chains[0]); err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery(`SELECT last_block`).WithArgs("bsc").WillReturnRows(sqlmock.NewRows([]string{"last_block"}).AddRow(105))
	if err := worker.scanChain(t.Context(), worker.chains[0]); err != nil || len(handler.processed) != 1 {
		t.Fatal("completed overlap ran again immediately", err)
	}
	second, secondMock, secondHandler := workerFixture(t, newRPCFixture(testTransfer()))
	second.recentReplay = true
	secondMock.ExpectQuery(`SELECT last_block`).WithArgs("bsc").WillReturnRows(sqlmock.NewRows([]string{"last_block"}).AddRow(105))
	expectReplayLoad(t, secondMock, &recentReplayState{Through: 105, Next: 106, After: -1}, time.Now())
	if err := second.scanChain(t.Context(), second.chains[0]); err != nil || len(secondHandler.processed) != 0 {
		t.Fatal("worker replacement bypassed durable overlap throttle", err)
	}
}

type knownReplayHandler struct {
	*recordingHandler
	checks int
}

func (h *knownReplayHandler) HasRecordedTransfer(context.Context, Transfer, int) (bool, error) {
	h.checks++
	return true, nil
}

func TestRecordedOverlapStillUsesIndependentPendingAndReorgValidation(t *testing.T) {
	for _, status := range []string{"pending", "credited"} {
		t.Run(status, func(t *testing.T) {
			transfer := testTransfer()
			fixture := newRPCFixture(transfer)
			worker, mock, handler := workerFixture(t, fixture)
			known := &knownReplayHandler{recordingHandler: handler}
			worker.handler = known
			if err := worker.processTransfer(t.Context(), worker.pools["bsc"], worker.chains[0], transfer, 6); err != nil {
				t.Fatal(err)
			}
			if known.checks != 1 || fixture.callCount("eth_getTransactionReceipt") != 0 {
				t.Fatal("durable overlap repeated receipt work")
			}
			expectObservation(mock, transfer, "1", "")
			expectObservationUpdate(mock, transfer, status, "", status, "").WillReturnResult(sqlmock.NewResult(0, 1))
			var err error
			if status == "pending" {
				err = worker.retryPending(t.Context())
			} else {
				err = worker.recheckCredited(t.Context())
			}
			if err != nil || fixture.callCount("eth_getTransactionReceipt") != 1 || known.checks != 1 {
				t.Fatal("overlap optimization bypassed the independent financial validation queue", err)
			}
			if (len(handler.processed) == 1) != (status == "pending") {
				t.Fatal("pending payment was dropped or credited payment was fulfilled twice")
			}
		})
	}
}
