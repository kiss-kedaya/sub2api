package scanner

import (
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestRecentReplayFailureKeepsCursorAndIsNotThrottled(t *testing.T) {
	transfer := testTransfer()
	fixture := newRPCFixture(transfer)
	worker, mock, handler := workerFixture(t, fixture)
	worker.recentReplay = true
	failed := errors.New("injected payment persistence failure")
	handler.processErr = failed
	// A late indexed payment is behind cursor 102. Advancing to 105 while
	// persistence fails would shift the overlap away from the missed range.
	for range 2 {
		mock.ExpectQuery(`SELECT last_block`).WithArgs("bsc").
			WillReturnRows(sqlmock.NewRows([]string{"last_block"}).AddRow(102))
		if err := worker.scanChain(t.Context(), worker.chains[0]); !errors.Is(err, failed) {
			t.Fatal("failed replay did not stop forward cursor work", err)
		}
		if !worker.replayedAt["bsc"].IsZero() {
			t.Fatal("failed replay was marked successful and throttled")
		}
	}
	if len(handler.processed) != 2 {
		t.Fatal("a failed transfer did not get the next immediate retry")
	}
}

func TestRecentReplaySuccessIsBoundedAndThrottled(t *testing.T) {
	fixture := newRPCFixture(testTransfer())
	worker, mock, handler := workerFixture(t, fixture)
	worker.recentReplay = true
	for range 2 {
		mock.ExpectQuery(`SELECT last_block`).WithArgs("bsc").
			WillReturnRows(sqlmock.NewRows([]string{"last_block"}).AddRow(105))
		if err := worker.scanChain(t.Context(), worker.chains[0]); err != nil {
			t.Fatal(err)
		}
	}
	if len(handler.processed) != 1 || time.Since(worker.replayedAt["bsc"]) > time.Second {
		t.Fatal("successful recent replay was skipped or repeated immediately")
	}
}
