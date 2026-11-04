package scanner

import (
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
	"github.com/stretchr/testify/require"
)

func cursorRow(block int64) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"last_block"}).AddRow(block)
}

func TestScanProgressReportsCursorAndConfirmedHead(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectQuery(`SELECT last_block FROM crypto_scan_cursors`).WithArgs("bsc").WillReturnRows(cursorRow(900))

	w := &Worker{
		cursor: NewCursorStore(db),
		pools:  map[string]*rpc.Pool{"bsc": rpc.NewPool("https://rpc.example.invalid")},
		chains: []ChainConfig{{Chain: "bsc", Confirmations: 15}},
	}
	w.noteHead("bsc", 1015)

	progress, err := w.ScanProgress(t.Context())
	require.NoError(t, err)
	require.Equal(t, []ChainScanProgress{{Chain: "bsc", Cursor: 900, ConfirmedHead: 1000, HeadKnown: true}}, progress)
	require.EqualValues(t, 100, progress[0].Remaining())
	require.False(t, progress[0].CaughtUp())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestScanProgressWithoutASampledHeadIsNeverCaughtUp(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectQuery(`SELECT last_block FROM crypto_scan_cursors`).WithArgs("eth").WillReturnRows(cursorRow(50))

	w := &Worker{
		cursor: NewCursorStore(db),
		pools:  map[string]*rpc.Pool{"eth": rpc.NewPool("https://rpc.example.invalid")},
		chains: []ChainConfig{{Chain: "eth", Confirmations: 12}},
	}
	progress, err := w.ScanProgress(t.Context())
	require.NoError(t, err)
	require.Len(t, progress, 1)
	require.False(t, progress[0].HeadKnown)
	require.False(t, progress[0].CaughtUp(), "an unmeasured chain must not look finished")
	require.Zero(t, progress[0].Remaining())
}

func TestScanProgressHeadBelowConfirmationsHasNothingToScan(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectQuery(`SELECT last_block FROM crypto_scan_cursors`).WithArgs("eth").WillReturnRows(sqlmock.NewRows([]string{"last_block"}))

	w := &Worker{
		cursor: NewCursorStore(db),
		pools:  map[string]*rpc.Pool{"eth": rpc.NewPool("https://rpc.example.invalid")},
		chains: []ChainConfig{{Chain: "eth", Confirmations: 12}},
	}
	w.noteHead("eth", 5)
	progress, err := w.ScanProgress(t.Context())
	require.NoError(t, err)
	require.Equal(t, []ChainScanProgress{{Chain: "eth", HeadKnown: true}}, progress)
	require.True(t, progress[0].CaughtUp())
}

func TestScanProgressCursorAheadOfConfirmedHeadIsCaughtUp(t *testing.T) {
	require.True(t, ChainScanProgress{Cursor: 2000, ConfirmedHead: 1990, HeadKnown: true}.CaughtUp())
	require.Zero(t, ChainScanProgress{Cursor: 2000, ConfirmedHead: 1990, HeadKnown: true}.Remaining())
}

func TestScanProgressSkipsChainsWithoutAPool(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	// Only the chain with a pool is read; a pass skips the other one too.
	mock.ExpectQuery(`SELECT last_block FROM crypto_scan_cursors`).WithArgs("bsc").WillReturnRows(cursorRow(10))

	w := &Worker{
		cursor: NewCursorStore(db),
		pools:  map[string]*rpc.Pool{"bsc": rpc.NewPool("https://rpc.example.invalid")},
		chains: []ChainConfig{{Chain: "eth"}, {Chain: "bsc"}},
	}
	progress, err := w.ScanProgress(t.Context())
	require.NoError(t, err)
	require.Len(t, progress, 1)
	require.Equal(t, "bsc", progress[0].Chain)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestScanProgressCursorErrorIsReturnedNotHidden(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectQuery(`SELECT last_block FROM crypto_scan_cursors`).WithArgs("bsc").WillReturnError(errors.New("connection refused"))

	w := &Worker{
		cursor: NewCursorStore(db),
		pools:  map[string]*rpc.Pool{"bsc": rpc.NewPool("https://rpc.example.invalid")},
		chains: []ChainConfig{{Chain: "bsc"}},
	}
	progress, err := w.ScanProgress(t.Context())
	require.Error(t, err)
	require.Nil(t, progress)
}

func TestScanProgressNilWorkerAndMissingCursorAreErrors(t *testing.T) {
	var nilWorker *Worker
	_, err := nilWorker.ScanProgress(t.Context())
	require.Error(t, err)
	_, err = (&Worker{}).ScanProgress(t.Context())
	require.Error(t, err)
}
