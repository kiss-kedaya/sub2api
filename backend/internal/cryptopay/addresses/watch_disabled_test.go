package addresses

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestScannerStillWatchesDisabledAssignments(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectQuery(`status IN \('active', 'disabled'\)`).WithArgs(FamilyEVM).
		WillReturnRows(sqlmock.NewRows([]string{"address"}).AddRow("0xactive").AddRow("0xdisabled"))
	got, err := NewRepo(db).ActiveAddresses(t.Context(), FamilyEVM)
	require.NoError(t, err)
	require.Equal(t, []string{"0xactive", "0xdisabled"}, got)
	require.NoError(t, mock.ExpectationsWereMet())
}
