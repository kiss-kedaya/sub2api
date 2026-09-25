package repository

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestProcessPendingEmailClaimsBeforeDeliveryAndDeletesOnSuccess(t *testing.T) {
	repo, mock := ticketSQLMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT key, value FROM settings`).WithArgs("ticket_reply_email:", "ticket_reply_email;").WillReturnRows(
		rowsWithColumns([]string{"key", "value"}, "ticket_reply_email:17", `{"ticket_id":10,"message_id":17}`),
	)
	mock.ExpectExec(`UPDATE settings SET updated_at = NOW\(\) \+ INTERVAL '60 seconds'`).WithArgs("ticket_reply_email:17").WillReturnResult(resultOne())
	mock.ExpectCommit()
	delivered := false
	mock.ExpectBegin()
	mock.ExpectExec(`DELETE FROM settings WHERE key = \$1 AND value = \$2`).WithArgs("ticket_reply_email:17", `{"ticket_id":10,"message_id":17}`).WillReturnResult(resultOne())
	mock.ExpectCommit()
	processed, err := repo.ProcessPendingEmail(context.Background(), func(_ context.Context, ticketID, messageID int64) error {
		delivered = true
		require.Equal(t, int64(10), ticketID)
		require.Equal(t, int64(17), messageID)
		return nil
	})
	require.NoError(t, err)
	require.True(t, processed)
	require.True(t, delivered)
}

func TestProcessPendingEmailKeepsFailedJobForBackoff(t *testing.T) {
	repo, mock := ticketSQLMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT key, value FROM settings`).WithArgs("ticket_reply_email:", "ticket_reply_email;").WillReturnRows(
		rowsWithColumns([]string{"key", "value"}, "ticket_reply_email:17", `{"ticket_id":10,"message_id":17,"attempts":2}`),
	)
	mock.ExpectExec(`UPDATE settings SET updated_at = NOW\(\) \+ INTERVAL '60 seconds'`).WithArgs("ticket_reply_email:17").WillReturnResult(resultOne())
	mock.ExpectCommit()
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE settings SET value = \$2, updated_at = NOW\(\) \+ \(\$3 \* INTERVAL '1 second'\) WHERE key = \$1 AND value = \$4`).WithArgs("ticket_reply_email:17", `{"ticket_id":10,"message_id":17,"attempts":3}`, 240, `{"ticket_id":10,"message_id":17,"attempts":2}`).WillReturnResult(resultOne())
	mock.ExpectCommit()
	processed, err := repo.ProcessPendingEmail(context.Background(), func(context.Context, int64, int64) error { return errors.New("smtp unavailable") })
	require.NoError(t, err)
	require.True(t, processed)
}

func TestProcessPendingEmailIgnoresEmptyQueue(t *testing.T) {
	repo, mock := ticketSQLMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT key, value FROM settings`).WithArgs("ticket_reply_email:", "ticket_reply_email;").WillReturnError(sql.ErrNoRows)
	mock.ExpectRollback()
	processed, err := repo.ProcessPendingEmail(context.Background(), func(context.Context, int64, int64) error { t.Fatal("must not deliver"); return nil })
	require.NoError(t, err)
	require.False(t, processed)
}

func rowsWithColumns(columns []string, values ...driver.Value) *sqlmock.Rows {
	return sqlmock.NewRows(columns).AddRow(values...)
}

func resultOne() sql.Result {
	return sqlmock.NewResult(0, 1)
}
