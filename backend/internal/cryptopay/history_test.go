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
		WillReturnRows(sqlmock.NewRows([]string{"chain", "tx_hash", "token", "amount", "status", "reason", "created_at", "credited", "log_index", "order_id"}).
			AddRow("bsc", hash, "0xtoken", "10", "credited", "", time.Now(), 67, 2, 10))
	s := &Service{db: db}
	rows, err := s.UserDepositHistory(t.Context(), 42, 50)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, hash, rows[0].TxHash)
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
			expected.WillReturnRows(sqlmock.NewRows([]string{"id", "chain", "tx_hash", "log_index", "from_address", "to_address", "token", "amount", "status", "reason", "created_at"}).
				AddRow(250, "bsc", "0xhash", 0, "from", "to", "token", "1.000", "credited", "", time.Now()))
			s := &Service{db: db}
			rows, err := s.ListDepositsExportPage(t.Context(), chain, status, 0, 200)
			require.NoError(t, err)
			require.Len(t, rows, 1)
			require.Equal(t, int64(250), rows[0].ID)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
