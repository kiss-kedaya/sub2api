package sweeper

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestResetRetryableFailuresMovesOnlyUnbroadcastRows(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectQuery(`(?s)UPDATE sweep_records SET status='planned'.*RETURNING id`).
		WithArgs(200).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(1)).AddRow(int64(2)))

	e := &Executor{db: db}
	n, err := e.ResetRetryableFailures(context.Background(), 200)
	require.NoError(t, err)
	require.Equal(t, 2, n)
	require.NoError(t, mock.ExpectationsWereMet())
}
