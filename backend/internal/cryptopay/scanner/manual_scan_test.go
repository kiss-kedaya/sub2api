package scanner

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/mode"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
	"github.com/stretchr/testify/require"
)

func TestManualBackfillRejectsUnconfirmedAndUnboundedRanges(t *testing.T) {
	for _, from := range []uint64{106, 111, ^uint64(0)} {
		pg := &scannerMockPG{}
		w, _ := scannerLockFixture(t, pg)
		if err := w.RunOnceFrom(t.Context(), from); err == nil {
			t.Fatalf("accepted unconfirmed block %d", from)
		}
		scannerAssertSteps(t, pg, "acquire", "release")
		scannerAssertIdle(t, w, pg, false)
	}
	pg := &scannerMockPG{}
	w, fixture := scannerLockFixture(t, pg)
	fixture.head = "0x30d40" // 200000
	if err := w.RunOnceFrom(t.Context(), 1); err == nil {
		t.Fatal("unbounded historical range accepted")
	}
	scannerAssertSteps(t, pg, "acquire", "release")
}

func TestManualBackfillRewindsAndPreservesEarlierUnfinishedCursor(t *testing.T) {
	for _, from := range []uint64{90, 103} {
		pg := &scannerMockPG{}
		w, _ := scannerLockFixture(t, pg)
		if err := w.RunOnceFrom(t.Context(), from); err != nil {
			t.Fatal(err)
		}
		_, _, events, _ := pg.snapshot()
		saves := 0
		for _, event := range events {
			if strings.Contains(event.query, "INSERT INTO crypto_scan_cursors") {
				saves++
			}
		}
		want := 1
		if from == 90 {
			want = 3 // rewind, explicit start cursor and ordinary progress
		}
		if saves != want {
			t.Fatalf("from %d saved %d cursors, want %d", from, saves, want)
		}
		scannerAssertIdle(t, w, pg, false)
	}
}

func TestManualScanRunsFullPassUnderSharedLock(t *testing.T) {
	pg := &scannerMockPG{}
	w, _ := scannerLockFixture(t, pg)
	if err := w.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	scannerAssertSteps(t, pg, "acquire", "load cursor", "save cursor", "pending", "recheck", "release")
	scannerAssertIdle(t, w, pg, false)
}

func TestManualScanReturnsContentionWithoutChainWork(t *testing.T) {
	pg := &scannerMockPG{holder: -1, depth: 1}
	w, fixture := scannerLockFixture(t, pg)
	if err := w.RunOnce(t.Context()); !errors.Is(err, ErrScanBusy) {
		t.Fatalf("want busy: %v", err)
	}
	scannerAssertSteps(t, pg, "acquire")
	if fixture.callCount("eth_chainId") != 0 {
		t.Fatal("contender reached RPC")
	}
}

func TestManualScanReturnsFailuresAndAlwaysUnlocks(t *testing.T) {
	problem := errors.New("injected pending failure")
	pg := &scannerMockPG{beforeWork: func(_ context.Context, step string) error {
		if step == "pending" {
			return problem
		}
		return nil
	}}
	w, _ := scannerLockFixture(t, pg)
	if err := w.RunOnce(t.Context()); !errors.Is(err, problem) {
		t.Fatalf("pending error hidden: %v", err)
	}
	scannerAssertSteps(t, pg, "acquire", "load cursor", "save cursor", "pending", "recheck", "release")
	scannerAssertIdle(t, w, pg, false)
}

func TestManualScanOnlyScansSelectedChain(t *testing.T) {
	pg := &scannerMockPG{}
	w, selected := scannerLockFixture(t, pg)
	other := newRPCFixture(testTransfer())
	w.pools["ethereum"] = other.pool(t)
	w.chains = append(w.chains, ChainConfig{Chain: "ethereum", Confirmations: 5})

	require.NoError(t, w.RunOnceFrom(t.Context(), 0, "bsc"))
	require.Positive(t, selected.callCount("eth_blockNumber"))
	require.Zero(t, other.callCount("eth_chainId"))
	scannerAssertSteps(t, pg, "acquire", "load cursor", "save cursor", "pending", "recheck", "release")
	_, _, events, _ := pg.snapshot()
	for _, event := range events {
		if event.step == "pending" || event.step == "recheck" {
			require.Contains(t, event.query, "chain = ANY($1)", "maintenance must also stay within the selected chain")
		}
	}
	scannerAssertIdle(t, w, pg, false)
}

func TestScopedScanMaintenanceUsesOnlySelectedChain(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectQuery(`(?s)status = 'pending'.*AND chain = ANY\(\$1\).*LIMIT 50`).
		WithArgs(`{"bsc"}`).WillReturnRows(observationRows())
	mock.ExpectQuery(`(?s)d.status = 'credited'.*AND d.chain = ANY\(\$1\).*LIMIT 200`).
		WithArgs(`{"bsc"}`).WillReturnRows(observationRows())
	w := &Worker{db: db}
	require.NoError(t, w.retryPending(t.Context(), "bsc"))
	require.NoError(t, w.recheckCredited(t.Context(), "bsc"))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestManualScanRejectsUnknownOrDisabledChainBeforeWork(t *testing.T) {
	for _, chain := range []string{"ethereum", "solana", "unknown"} {
		t.Run(chain, func(t *testing.T) {
			pg := &scannerMockPG{}
			w, fixture := scannerLockFixture(t, pg)
			w.pools["ethereum"] = rpc.NewPool("http://127.0.0.1:1") // Configured RPC alone does not enable scanning.
			require.ErrorIs(t, w.RunOnceFrom(t.Context(), 0, "bsc", chain), ErrInvalidScanChain)
			scannerAssertSteps(t, pg)
			require.Zero(t, fixture.callCount("eth_chainId"))
		})
	}
}

func TestManualScanRejectsReadOnlyBeforeAcquiringLock(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "true")
	t.Setenv("CRYPTO_ADMIN_TEST_WRITE_ENABLED", "false")
	var w *Worker
	if err := w.RunOnce(t.Context()); !errors.Is(err, mode.ErrReadOnly) {
		t.Fatalf("want read-only: %v", err)
	}
}
