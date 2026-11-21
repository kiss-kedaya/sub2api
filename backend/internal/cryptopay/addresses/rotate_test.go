package addresses

import (
	"context"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

// q builds a regexp that matches the whitespace-normalised SQL sqlmock sees.
func q(sql string) string { return regexp.QuoteMeta(sql) }

// After a rotation the user has one disabled history row and one active row.
// The lookup must hand the active one to GetOrAssign, never the retired one.
func TestExistingAssignmentPrefersActiveRowAfterRotation(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectQuery(q(`SELECT id, user_id, chain, address, derivation_index, status
		FROM deposit_addresses WHERE user_id = $1 AND chain = $2
		ORDER BY (status = 'active') DESC, id DESC
		LIMIT 1`)).
		WithArgs(int64(8624), FamilyEVM).
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "chain", "address", "derivation_index", "status"}).
			AddRow(9001, 8624, "evm", "0xnewaddress", 400, "active"))

	got, err := NewRepo(db).ExistingAssignment(context.Background(), 8624, FamilyEVM)
	require.NoError(t, err)
	require.Equal(t, "0xnewaddress", got.Address)
	require.Equal(t, "active", got.Status)
	require.NoError(t, mock.ExpectationsWereMet())
}

// Guard against silently reintroducing a lookup that can pick the retired row.
func TestExistingAssignmentQueryOrdersActiveFirst(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectQuery(`ORDER BY \(status = 'active'\) DESC, id DESC LIMIT 1`).
		WithArgs(int64(1), FamilyEVM).
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "chain", "address", "derivation_index", "status"}).
			AddRow(1, 1, "evm", "0xactive", 1, "active"))
	_, err = NewRepo(db).ExistingAssignment(context.Background(), 1, FamilyEVM)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}
