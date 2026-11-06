package cryptopay

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

var sweepPageColumns = []string{"id", "chain", "from_address", "to_address", "token", "amount", "gas_tx_hash",
	"sweep_tx_hash", "status", "error", "created_at", "updated_at", "job_version"}

const sweepPageSelect = `(?s)SELECT id, chain, from_address, to_address, token, amount::text,\s+gas_tx_hash, sweep_tx_hash, status, error, created_at, updated_at, job_version\s+FROM sweep_records WHERE 1=1`

func newSweepPageService(t *testing.T) (*Service, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return &Service{db: db}, mock
}

func TestListSweepRecordsPageDefaultsToNewestHundredWithoutFilters(t *testing.T) {
	s, mock := newSweepPageService(t)
	mock.ExpectQuery(sweepPageSelect + ` ORDER BY id DESC LIMIT \$1$`).
		WithArgs(100).
		WillReturnRows(sqlmock.NewRows(sweepPageColumns))
	rows, err := s.ListSweepRecordsPage(t.Context(), "", 0, 0)
	require.NoError(t, err)
	require.NotNil(t, rows, "an empty page is an empty array, never null")
	require.Empty(t, rows)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestListSweepRecordsPageAppliesChainAndCursorAsBoundParameters(t *testing.T) {
	s, mock := newSweepPageService(t)
	mock.ExpectQuery(sweepPageSelect+` AND chain = \$1 AND id < \$2 ORDER BY id DESC LIMIT \$3$`).
		WithArgs("bsc", int64(900), 25).
		WillReturnRows(sqlmock.NewRows(sweepPageColumns))
	_, err := s.ListSweepRecordsPage(t.Context(), "bsc", 25, 900)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestListSweepRecordsPageCursorWithoutChainUsesFirstPlaceholder(t *testing.T) {
	s, mock := newSweepPageService(t)
	mock.ExpectQuery(sweepPageSelect+` AND id < \$1 ORDER BY id DESC LIMIT \$2$`).
		WithArgs(int64(7), 100).
		WillReturnRows(sqlmock.NewRows(sweepPageColumns))
	_, err := s.ListSweepRecordsPage(t.Context(), "", 100, 7)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestListSweepRecordsPageIgnoresNonPositiveCursor(t *testing.T) {
	for _, before := range []int64{0, -1} {
		s, mock := newSweepPageService(t)
		mock.ExpectQuery(sweepPageSelect + ` ORDER BY id DESC LIMIT \$1$`).
			WithArgs(100).
			WillReturnRows(sqlmock.NewRows(sweepPageColumns))
		_, err := s.ListSweepRecordsPage(t.Context(), "", 100, before)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	}
}

func TestListSweepRecordsPageLimitBounds(t *testing.T) {
	for _, tc := range []struct {
		name     string
		in, want int
	}{
		{"lowest accepted", 1, 1},
		{"highest accepted", 200, 200},
		{"zero falls back to default", 0, 100},
		{"negative falls back to default", -5, 100},
		{"above the cap falls back to default", 201, 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, mock := newSweepPageService(t)
			mock.ExpectQuery(sweepPageSelect + ` ORDER BY id DESC LIMIT \$1$`).
				WithArgs(tc.want).
				WillReturnRows(sqlmock.NewRows(sweepPageColumns))
			_, err := s.ListSweepRecordsPage(t.Context(), "", tc.in, 0)
			require.NoError(t, err)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestListSweepRecordsPageAddsTokenSymbolAndUpdatedAt(t *testing.T) {
	s, mock := newSweepPageService(t)
	created := time.Date(2026, 11, 5, 8, 0, 0, 0, time.FixedZone("CST", 8*3600))
	updated := created.Add(90 * time.Minute)
	usdt := "0xdac17f958d2ee523a2206206994597c13d831ec7"
	mock.ExpectQuery(sweepPageSelect).WillReturnRows(sqlmock.NewRows(sweepPageColumns).
		AddRow(12, "ethereum", "0xfrom", "0xto", usdt, "123.450000000000000000", "0xgas", "0xsweep", "swept", "", created, updated, 1).
		AddRow(11, "ethereum", "0xfrom", "0xto", "0x0000000000000000000000000000000000000001", "1", "", "", "planned", "", created, created, 1).
		AddRow(10, "solana", "from", "to", "SomeMint111", "5.0", "", "", "failed", "", created, created, 0))
	rows, err := s.ListSweepRecordsPage(t.Context(), "", 100, 0)
	require.NoError(t, err)
	require.Len(t, rows, 3)

	require.Equal(t, "USDT", rows[0].TokenSymbol)
	require.Equal(t, "123.45", rows[0].Amount, "padding zeros are trimmed as before")
	require.Equal(t, "2026-11-05T00:00:00Z", rows[0].CreatedAt)
	require.Equal(t, "2026-11-05T01:30:00Z", rows[0].UpdatedAt)
	require.Equal(t, "", rows[1].TokenSymbol, "an unwatched contract has no symbol rather than a guess")
	require.Equal(t, "", rows[2].TokenSymbol, "a non-EVM mint has no symbol")

	encoded, err := json.Marshal(rows[0])
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"token_symbol":"USDT"`)
	require.Contains(t, string(encoded), `"updated_at":"2026-11-05T01:30:00Z"`)
	require.Contains(t, string(encoded), `"token":"`+usdt+`"`, "the contract stays available next to the symbol")
	require.Contains(t, string(encoded), `"job_version":1`, "the console needs the version to hide legacy rows")
	require.Zero(t, rows[2].JobVersion, "a legacy row keeps its zero version so the console can archive it")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestListSweepRecordsPageStillRedactsStoredErrors(t *testing.T) {
	s, mock := newSweepPageService(t)
	now := time.Now()
	mock.ExpectQuery(sweepPageSelect).WillReturnRows(sqlmock.NewRows(sweepPageColumns).
		AddRow(1, "bsc", "from", "to", "token", "1", "", "", "failed",
			"rpc https://example.test/fixture-secret-path?key=fixture-secret-query: fixture-node-message", now, now, 1))
	rows, err := s.ListSweepRecordsPage(t.Context(), "", 100, 0)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	encoded, err := json.Marshal(rows)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "fixture-")
	require.NotContains(t, string(encoded), "https://")
	require.Equal(t, "Payment operation failed; reconcile transaction status before retrying", rows[0].Error)
}

func TestListSweepRecordsPageReportsQueryFailure(t *testing.T) {
	s, mock := newSweepPageService(t)
	mock.ExpectQuery(sweepPageSelect).WillReturnError(errors.New("connection reset"))
	rows, err := s.ListSweepRecordsPage(t.Context(), "", 100, 0)
	require.Error(t, err)
	require.Nil(t, rows)
	require.Contains(t, err.Error(), "list sweep records")
}

func TestLegacyListSweepRecordsKeepsItsOwnShape(t *testing.T) {
	// The original function is unchanged: it neither selects updated_at nor
	// resolves a symbol, so the zero values below are what an old caller sees.
	s, mock := newSweepPageService(t)
	mock.ExpectQuery(`(?s)SELECT id, chain, from_address, to_address, token, amount::text,\s+gas_tx_hash, sweep_tx_hash, status, error, created_at, job_version\s+FROM sweep_records ORDER BY id DESC LIMIT 100`).
		WillReturnRows(sqlmock.NewRows(append(sweepPageColumns[:11:11], "job_version")).
			AddRow(1, "bsc", "from", "to", "token", "1", "", "", "swept", "", time.Now(), 0))
	rows, err := s.ListSweepRecords(t.Context(), "", 100)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "", rows[0].TokenSymbol)
	require.Equal(t, "", rows[0].UpdatedAt)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLookupTokenSymbolOnlyAnswersForWatchedContracts(t *testing.T) {
	require.Equal(t, "USDT", lookupTokenSymbol(ChainEthereum, "0xdac17f958d2ee523a2206206994597c13d831ec7"))
	require.Equal(t, "USDT", lookupTokenSymbol(ChainEthereum, "0xDAC17F958D2EE523A2206206994597C13D831EC7"), "contract match ignores case")
	require.Equal(t, "USDC.e", lookupTokenSymbol(ChainXLayer, "0x74b7f16337b8972027f6196a17a631ac6de26d22"))
	require.Equal(t, "", lookupTokenSymbol(ChainEthereum, "0x74b7f16337b8972027f6196a17a631ac6de26d22"), "a contract is only known on its own chain")
	require.Equal(t, "", lookupTokenSymbol("unknown-chain", "0xdac17f958d2ee523a2206206994597c13d831ec7"))
	require.Equal(t, "", lookupTokenSymbol(ChainSolana, "SomeMint111"))
	// The deposit path keeps its historical "USDT" fallback; this change must not touch it.
	require.Equal(t, "USDT", tokenSymbol(ChainEthereum, "0x0000000000000000000000000000000000000001"))
}
