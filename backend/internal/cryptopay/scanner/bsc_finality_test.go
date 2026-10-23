package scanner

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

// Exercise the worker's actual live-scan and saved-pending paths, including
// receipt/log/canonical checks and the call into its ledger handler. A green
// helper test alone would not catch one entry point keeping its old policy.
func TestBSCFinalizedPolicyAcrossLiveAndPendingCredit(t *testing.T) {
	tests := []struct {
		name           string
		finalized      any
		fail, httpFail string
		safe           any
		confirmations  uint64
		credit         bool
		pendingError   bool
	}{
		{"finalized reaches deposit", map[string]any{"number": "0x65"}, "", "", nil, 5, true, false},
		{"finalized behind while safe ahead", map[string]any{"number": "0x64"}, "", "", map[string]any{"number": "0x6e"}, 5, false, false},
		{"finalized RPC unavailable", nil, "finalized", "", map[string]any{"number": "0x6e"}, 5, false, true},
		{"finalized HTTP unavailable", nil, "", "finalized", map[string]any{"number": "0x6e"}, 5, false, true},
		{"finalized null cannot fall back to depth", nil, "", "", map[string]any{"number": "0x6e"}, 5, false, true},
		{"finalized malformed height", map[string]any{"number": "0x65junk"}, "", "", nil, 5, false, true},
		{"finalized malformed envelope", []any{}, "", "", nil, 5, false, true},
		{"safe failure irrelevant", map[string]any{"number": "0x65"}, "safe", "", nil, 5, true, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, path := range []string{"live", "pending"} {
				t.Run(path, func(t *testing.T) {
					transfer := testTransfer()
					fixture := newRPCFixture(transfer)
					fixture.finalized, fixture.safe, fixture.fail, fixture.httpFail = test.finalized, test.safe, test.fail, test.httpFail
					worker, mock, handler := workerFixture(t, fixture)
					worker.chains[0].Confirmations = test.confirmations
					if path == "live" {
						mock.ExpectQuery(`SELECT last_block`).WillReturnRows(sqlmock.NewRows([]string{"last_block"}).AddRow(100))
						mock.ExpectExec(`INSERT INTO crypto_scan_cursors`).WithArgs("bsc", int64(105)).WillReturnResult(sqlmock.NewResult(0, 1))
						if err := worker.scanChain(t.Context(), worker.chains[0]); err != nil {
							t.Fatal(err)
						}
						if !test.credit && len(handler.pending) != 1 {
							t.Fatal("unfinalized observation was lost instead of saved pending")
						}
					} else {
						expectObservation(mock, transfer, "1", "")
						expectObservationUpdate(mock, transfer, "pending", "", "pending", "").WillReturnResult(sqlmock.NewResult(0, 1))
						err := worker.retryPending(t.Context())
						if (err != nil) != test.pendingError {
							t.Fatalf("pending retry error=%v wantError=%t", err, test.pendingError)
						}
					}
					if (len(handler.processed) == 1) != test.credit {
						t.Fatalf("ledger calls=%d wantCredit=%t", len(handler.processed), test.credit)
					}
					if fixture.callCount("finalized") != 1 || fixture.callCount("safe") != 0 {
						t.Fatal("BSC used safe or skipped explicit finalized proof")
					}
				})
			}
		})
	}
}

func TestBSCFinalizedDoesNotOverrideConfiguredDepth(t *testing.T) {
	transfer := testTransfer()
	fixture := newRPCFixture(transfer)
	worker, mock, handler := workerFixture(t, fixture)
	worker.chains[0].Confirmations = 10 // Head 110, deposit 101: only nine blocks.
	expectObservation(mock, transfer, "1", "")
	expectObservationUpdate(mock, transfer, "pending", "", "pending", "").WillReturnResult(sqlmock.NewResult(0, 1))
	if err := worker.retryPending(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(handler.processed) != 0 {
		t.Fatal("finalized bypassed the operator's required block depth")
	}
	// Live scanning excludes the same insufficient-depth deposit at its range
	// boundary, so it cannot invoke the ledger early either.
	mock.ExpectQuery(`SELECT last_block`).WillReturnRows(sqlmock.NewRows([]string{"last_block"}).AddRow(100))
	if err := worker.scanChain(t.Context(), worker.chains[0]); err != nil {
		t.Fatal(err)
	}
	if fixture.callCount("eth_getLogs") != 0 || len(handler.processed) != 0 {
		t.Fatal("live scan bypassed configured depth")
	}
}

func TestBSCFinalizedStillRequiresAvailableHead(t *testing.T) {
	fixture := newRPCFixture(testTransfer())
	fixture.fail = "eth_blockNumber"
	worker, mock, handler := workerFixture(t, fixture)
	expectObservation(mock, testTransfer(), "1", "")
	expectObservationUpdate(mock, testTransfer(), "pending", "", "pending", "").WillReturnResult(sqlmock.NewResult(0, 1))
	if err := worker.retryPending(t.Context()); err == nil || len(handler.processed) != 0 {
		t.Fatal("finalized with unknown head granted credit")
	}
}

func TestBSCWaitingDepositBecomesCreditableAsFinalizedAdvances(t *testing.T) {
	transfer := testTransfer()
	fixture := newRPCFixture(transfer)
	fixture.finalized = map[string]any{"number": "0x64"}
	worker, mock, handler := workerFixture(t, fixture)
	for round := 0; round < 2; round++ {
		expectObservation(mock, transfer, "1", "")
		expectObservationUpdate(mock, transfer, "pending", "", "pending", "").WillReturnResult(sqlmock.NewResult(0, 1))
		if err := worker.retryPending(t.Context()); err != nil {
			t.Fatal(err)
		}
		if len(handler.processed) != round {
			t.Fatalf("round %d credited %d times", round, len(handler.processed))
		}
		fixture.finalized = map[string]any{"number": "0x65"}
	}
}

func TestPendingRetryShortensOnlyBSCWait(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	mock.ExpectQuery(`(?s)status = 'pending'.*CASE WHEN chain = 'bsc' THEN INTERVAL '5 seconds' ELSE INTERVAL '1 minute' END.*LIMIT 50`).WillReturnRows(observationRows())
	worker := &Worker{db: db}
	if err := worker.retryPending(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestLaggingChainYieldsAndResumesAtExactCursor(t *testing.T) {
	fixture := newRPCFixture(testTransfer())
	fixture.logs = nil
	fixture.head = "0x3e8" // 1000, far ahead of the durable cursor.
	worker, mock, _ := workerFixture(t, fixture)
	worker.chains[0].MaxRange = 1
	for _, start := range []int64{100, 104} {
		mock.ExpectQuery(`SELECT last_block`).WithArgs("bsc").WillReturnRows(sqlmock.NewRows([]string{"last_block"}).AddRow(start))
		for offset := int64(1); offset <= maxScanBatchesPerChain; offset++ {
			mock.ExpectExec(`INSERT INTO crypto_scan_cursors`).WithArgs("bsc", start+offset).WillReturnResult(sqlmock.NewResult(0, 1))
		}
		if err := worker.scanChain(t.Context(), worker.chains[0]); err != nil {
			t.Fatal(err)
		}
	}
	fixture.mu.Lock()
	ranges := append([]getLogsParams(nil), fixture.logRanges...)
	fixture.mu.Unlock()
	if len(ranges) != 10 {
		t.Fatalf("unbounded or skipped catch-up ranges: %+v", ranges)
	}
	for pass := range 2 {
		if ranges[pass*5].FromBlock != "0x3e3" || ranges[pass*5].ToBlock != "0x3e3" {
			t.Fatal("lagging scanner failed to check its latest live window first")
		}
		for offset := range 4 {
			blockRange := ranges[pass*5+offset+1]
			want := fmt.Sprintf("0x%x", 101+pass*4+offset)
			if blockRange.FromBlock != want || blockRange.ToBlock != want {
				t.Fatalf("cursor skipped a block: %+v", ranges)
			}
		}
	}
}

func TestOneChainRPCFailureDoesNotBlockFollowingChain(t *testing.T) {
	first, second := newRPCFixture(testTransfer()), newRPCFixture(testTransfer())
	first.fail = "eth_getLogs"
	second.chainID, second.logs = "0x89", nil
	worker, mock, _ := workerFixture(t, first)
	worker.pools["polygon"] = second.pool(t)
	other := worker.chains[0]
	other.Chain = "polygon"
	worker.chains = append(worker.chains, other)
	mock.ExpectQuery(`SELECT last_block`).WithArgs("bsc").WillReturnRows(sqlmock.NewRows([]string{"last_block"}).AddRow(100))
	mock.ExpectQuery(`SELECT last_block`).WithArgs("polygon").WillReturnRows(sqlmock.NewRows([]string{"last_block"}).AddRow(100))
	mock.ExpectExec(`INSERT INTO crypto_scan_cursors`).WithArgs("polygon", int64(105)).WillReturnResult(sqlmock.NewResult(0, 1))
	if err := worker.ScanOnce(t.Context()); err == nil {
		t.Fatal("failed chain was reported successful")
	}
	if first.callCount("eth_getLogs") != 1 || second.callCount("eth_getLogs") != 1 {
		t.Fatal("a failed chain starved the following network")
	}
}

func TestBackloggedChainsYieldBeforeReleasingSharedScanLock(t *testing.T) {
	pg := &scannerMockPG{}
	worker, first := scannerLockFixture(t, pg)
	first.head = "0x3e8"
	worker.chains[0].MaxRange = 1
	second := newRPCFixture(testTransfer())
	second.chainID, second.head, second.logs = "0x89", "0x3e8", nil
	worker.pools["polygon"] = second.pool(t)
	other := worker.chains[0]
	other.Chain = "polygon"
	worker.chains = append(worker.chains, other)
	if err := worker.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if first.callCount("eth_getLogs") != 5 || second.callCount("eth_getLogs") != 5 {
		t.Fatal("catch-up was unbounded or starved another configured chain")
	}
	scannerAssertIdle(t, worker, pg, false)
	scannerAssertSteps(t, pg, "acquire", "pending", "recheck", "load cursor",
		"save cursor", "save cursor", "save cursor", "save cursor", "load cursor",
		"save cursor", "save cursor", "save cursor", "save cursor", "release")
}

func TestScanInstallsPerChainDeadline(t *testing.T) {
	fixture := newRPCFixture(testTransfer())
	fixture.logs = nil
	worker, mock, _ := workerFixture(t, fixture)
	worker.addresses = func(ctx context.Context, _ string) ([]string, error) {
		deadline, exists := ctx.Deadline()
		if !exists || time.Until(deadline) <= 0 || time.Until(deadline) > maxScanTimePerChain {
			t.Error("chain scan has no bounded time budget")
		}
		return nil, nil
	}
	mock.ExpectQuery(`SELECT last_block`).WillReturnRows(sqlmock.NewRows([]string{"last_block"}).AddRow(100))
	mock.ExpectExec(`INSERT INTO crypto_scan_cursors`).WillReturnResult(sqlmock.NewResult(0, 1))
	if err := worker.scanChain(t.Context(), worker.chains[0]); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerImmediatelyScansBeforeLongPollInterval(t *testing.T) {
	started := make(chan struct{}, 1)
	pg := &scannerMockPG{beforeWork: func(_ context.Context, step string) error {
		if step == "pending" {
			select {
			case started <- struct{}{}:
			default:
			}
		}
		return nil
	}}
	worker, _ := scannerLockFixture(t, pg)
	worker.interval = time.Hour
	worker.Start()
	t.Cleanup(worker.Stop)
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("startup waited for the polling interval instead of scanning immediately")
	}
}
