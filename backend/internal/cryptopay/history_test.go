package cryptopay

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestUserDepositHistoryReturnsFullTransactionHashForCaller(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	hash := "0x" + strings.Repeat("ab", 32)
	mock.ExpectQuery(`(?s)SELECT d.chain, d.tx_hash,.*WHERE a.user_id = \$1.*ORDER BY d.id DESC LIMIT \$2`).
		WithArgs(int64(42), 50).
		WillReturnRows(sqlmock.NewRows([]string{"chain", "tx_hash", "token", "amount", "status", "reason", "created_at", "credited", "log_index", "order_id", "block_number", "confirmations", "provisional", "credited_at", "revoked_at", "revoke_reason"}).
			AddRow("bsc", hash, "0xtoken", "10", "credited", "", time.Now(), 67, 2, 10, 101, 0, false, nil, nil, ""))
	s := &Service{db: db}
	rows, err := s.UserDepositHistory(t.Context(), 42, 50)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, hash, rows[0].TxHash)
	require.NotNil(t, rows[0].CreditedKnown)
	require.True(t, *rows[0].CreditedKnown)
	encoded, err := json.Marshal(rows)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"tx_hash":"`+hash+`"`)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDepositHistoryKeepsActualOrderAmountAfterRateChange(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectQuery(`(?s)COALESCE\(o.amount, 0\).*LEFT JOIN payment_orders`).
		WithArgs(int64(1), 50).
		WillReturnRows(sqlmock.NewRows([]string{"chain", "tx_hash", "token", "amount", "status", "reason", "created_at", "credited", "log_index", "order_id", "block_number", "confirmations", "provisional", "credited_at", "revoked_at", "revoke_reason"}).
			AddRow("bsc", "0xfixture", "0xtoken", "10.000000000000000000", "credited", "", time.Now(), 67, 2, 10, 101, 0, false, nil, nil, ""))
	s := &Service{db: db, config: CryptoConfig{CreditMultiplier: 6}}
	rows, err := s.UserDepositHistory(t.Context(), 1, 50)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, 67.0, rows[0].Credited)
	require.NotNil(t, rows[0].CreditedKnown)
	require.True(t, *rows[0].CreditedKnown)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLegacyHistoryResolvesOriginalOrderInOneBatch(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectQuery(`(?s)COALESCE\(o.amount, 0\).*LEFT JOIN payment_orders`).WithArgs(int64(1), 50).
		WillReturnRows(sqlmock.NewRows([]string{"chain", "tx_hash", "token", "amount", "status", "reason", "created_at", "credited", "log_index", "order_id", "block_number", "confirmations", "provisional", "credited_at", "revoked_at", "revoke_reason"}).
			AddRow("bsc", "0xlegacy", "0xtoken", "10", "credited", "", time.Now(), 0, 0, nil, 101, 0, false, nil, nil, ""))
	_, identity := depositOrderIdentity("bsc", "0xlegacy", 0)
	mock.ExpectQuery(`SELECT out_trade_no, amount FROM payment_orders`).WithArgs(int64(1), identity).
		WillReturnRows(sqlmock.NewRows([]string{"out_trade_no", "amount"}).AddRow(identity, 67))
	s := &Service{db: db, config: CryptoConfig{CreditMultiplier: 99}}
	rows, err := s.UserDepositHistory(t.Context(), 1, 50)
	require.NoError(t, err)
	require.Equal(t, 67.0, rows[0].Credited)
	require.NotNil(t, rows[0].CreditedKnown)
	require.True(t, *rows[0].CreditedKnown)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestListDepositsExportPageUsesPrimaryKeyCursorWithoutSummaryScan(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	created := time.Date(2026, 10, 21, 8, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	mock.ExpectQuery(`(?s)SELECT id, chain, tx_hash, log_index.*WHERE id < \$1 AND chain = \$2 AND status = \$3 ORDER BY id DESC LIMIT \$4`).
		WithArgs(int64(150), "bsc", "credited", 100).
		WillReturnRows(sqlmock.NewRows([]string{"id", "chain", "tx_hash", "log_index", "from_address", "to_address", "token", "amount", "status", "reason", "created_at", "native_price_usd", "native_price_at", "native_price_source", "provisional", "provisional_amount", "credited_at", "revoked_at", "revoke_reason"}).
			AddRow(149, "bsc", "0xhash", 3, "from", "to", "token", "2.500000000000000000", "credited", "", created, "", nil, "", false, 0, nil, nil, ""))

	s := &Service{db: db}
	rows, err := s.ListDepositsExportPage(t.Context(), "bsc", "credited", 150, 100)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, int64(149), rows[0].ID)
	require.Equal(t, "2.5", rows[0].Amount)
	require.Equal(t, "2026-10-21T00:00:00Z", rows[0].CreatedAt)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestListDepositsExportPageStartsWithNewestRecord(t *testing.T) {
	for _, filtered := range []bool{false, true} {
		t.Run(map[bool]string{false: "all", true: "filtered"}[filtered], func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			query := `(?s)SELECT id, chain, tx_hash, log_index.*WHERE TRUE ORDER BY id DESC LIMIT \$1`
			if filtered {
				query = `(?s)SELECT id, chain, tx_hash, log_index.*WHERE TRUE AND chain = \$1 AND status = \$2 ORDER BY id DESC LIMIT \$3`
			}
			expected := mock.ExpectQuery(query)
			chain, status := "", ""
			if filtered {
				chain, status = "bsc", "credited"
				expected.WithArgs(chain, status, 200)
			} else {
				expected.WithArgs(200)
			}
			expected.WillReturnRows(sqlmock.NewRows([]string{"id", "chain", "tx_hash", "log_index", "from_address", "to_address", "token", "amount", "status", "reason", "created_at", "native_price_usd", "native_price_at", "native_price_source", "provisional", "provisional_amount", "credited_at", "revoked_at", "revoke_reason"}).
				AddRow(250, "bsc", "0xhash", 0, "from", "to", "native", "1.000", "credited", "", time.Now(), "2000.500000000000000000", time.Now(), "stub", false, 0, nil, nil, ""))
			s := &Service{db: db}
			rows, err := s.ListDepositsExportPage(t.Context(), chain, status, 0, 200)
			require.NoError(t, err)
			require.Len(t, rows, 1)
			require.Equal(t, int64(250), rows[0].ID)
			require.Equal(t, "2000.5", rows[0].NativePriceUSD)
			require.NotEmpty(t, rows[0].NativePriceAt)
			require.Equal(t, "stub", rows[0].NativePriceSource)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// Instant credits reach the user's own history as "provisional" until final
// confirmation, and as "revoked" once the chain invalidated them. The UI keys
// its badges off this status instead of re-deriving it from ledger columns.
func TestUserDepositHistoryDerivesProvisionalAndRevokedStatus(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	created := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	creditedAt := created.Add(2 * time.Second)
	revokedAt := created.Add(30 * time.Second)
	mock.ExpectQuery(`(?s)SELECT d.chain, d.tx_hash,.*provisional.*WHERE a.user_id = \$1.*ORDER BY d.id DESC LIMIT \$2`).
		WithArgs(int64(7), 50).
		WillReturnRows(sqlmock.NewRows([]string{"chain", "tx_hash", "token", "amount", "status", "reason", "created_at", "credited", "log_index", "order_id", "block_number", "confirmations", "provisional", "credited_at", "revoked_at", "revoke_reason"}).
			AddRow("bsc", "0xprov", "0xtoken", "10", "credited", "", created, 67, 2, 10, 101, 0, true, creditedAt, nil, "").
			AddRow("bsc", "0xrev", "0xtoken", "10", "reverted", "", created, 0, 3, nil, 101, 0, true, creditedAt, revokedAt, "reorg_noncanonical_block"))

	s := &Service{db: db}
	rows, err := s.UserDepositHistory(t.Context(), 7, 50)
	require.NoError(t, err)
	require.Len(t, rows, 2)

	require.Equal(t, userDepositStatusProvisional, rows[0].Status)
	require.True(t, rows[0].Provisional)
	require.Equal(t, creditedAt.Format(time.RFC3339), rows[0].CreditedAt)
	require.Empty(t, rows[0].RevokedAt)
	require.Equal(t, 67.0, rows[0].Credited)

	require.Equal(t, userDepositStatusRevoked, rows[1].Status)
	require.True(t, rows[1].Provisional)
	require.Equal(t, "reorg_noncanonical_block", rows[1].RevokeReason)
	require.Equal(t, revokedAt.Format(time.RFC3339), rows[1].RevokedAt)
	require.Zero(t, rows[1].Credited, "a revoked credit must not show as credited")
	require.NoError(t, mock.ExpectationsWereMet())
}
