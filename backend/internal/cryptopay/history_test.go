package cryptopay

import (
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestDepositHistoryKeepsActualOrderAmountAfterRateChange(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectQuery(`(?s)COALESCE\(o.amount, 0\).*LEFT JOIN payment_orders`).
		WithArgs(int64(1), 50).
		WillReturnRows(sqlmock.NewRows([]string{"chain", "tx_hash", "token", "amount", "status", "reason", "created_at", "credited", "log_index", "order_id"}).
			AddRow("bsc", "0xfixture", "0xtoken", "10.000000000000000000", "credited", "", time.Now(), 67, 2, 10))
	s := &Service{db: db, config: CryptoConfig{CreditMultiplier: 6}}
	rows, err := s.UserDepositHistory(t.Context(), 1, 50)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, 67.0, rows[0].Credited)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLegacyHistoryResolvesOriginalOrderInOneBatch(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectQuery(`(?s)COALESCE\(o.amount, 0\).*LEFT JOIN payment_orders`).WithArgs(int64(1), 50).
		WillReturnRows(sqlmock.NewRows([]string{"chain", "tx_hash", "token", "amount", "status", "reason", "created_at", "credited", "log_index", "order_id"}).
			AddRow("bsc", "0xlegacy", "0xtoken", "10", "credited", "", time.Now(), 0, 0, nil))
	_, identity := depositOrderIdentity("bsc", "0xlegacy", 0)
	mock.ExpectQuery(`SELECT out_trade_no, amount FROM payment_orders`).WithArgs(int64(1), identity).
		WillReturnRows(sqlmock.NewRows([]string{"out_trade_no", "amount"}).AddRow(identity, 67))
	s := &Service{db: db, config: CryptoConfig{CreditMultiplier: 99}}
	rows, err := s.UserDepositHistory(t.Context(), 1, 50)
	require.NoError(t, err)
	require.Equal(t, 67.0, rows[0].Credited)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestListDepositsExportPageUsesPrimaryKeyCursorWithoutSummaryScan(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	created := time.Date(2026, 10, 21, 8, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	mock.ExpectQuery(`(?s)SELECT id, chain, tx_hash, log_index.*WHERE id < \$1 AND chain = \$2 AND status = \$3 ORDER BY id DESC LIMIT \$4`).
		WithArgs(int64(150), "bsc", "credited", 100).
		WillReturnRows(sqlmock.NewRows([]string{"id", "chain", "tx_hash", "log_index", "from_address", "to_address", "token", "amount", "status", "reason", "created_at"}).
			AddRow(149, "bsc", "0xhash", 3, "from", "to", "token", "2.500000000000000000", "credited", "", created))

	s := &Service{db: db}
	rows, err := s.ListDepositsExportPage(t.Context(), "bsc", "credited", 150, 100)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, int64(149), rows[0].ID)
	require.Equal(t, "2.5", rows[0].Amount)
	require.Equal(t, "2026-10-21T00:00:00Z", rows[0].CreatedAt)
	require.NoError(t, mock.ExpectationsWereMet())
}
