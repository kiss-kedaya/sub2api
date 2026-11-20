package addresses

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

// q builds a regexp that matches the whitespace-normalised SQL sqlmock sees.
func q(sql string) string { return regexp.QuoteMeta(sql) }

func TestRotateKeepsOldRowDisabledAndActivatesFreshPoolRow(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	mock.ExpectQuery(q(`SELECT id, user_id, chain, address, derivation_index, status
		FROM deposit_addresses WHERE id = $1 FOR UPDATE`)).
		WithArgs(int64(673)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "chain", "address", "derivation_index", "status"}).
			AddRow(673, 8624, "evm", "0xoldaddress", 337, "active"))
	mock.ExpectQuery(q(`SELECT id, derivation_index, address
		FROM crypto_address_pool
		WHERE chain_family = $1 AND status = 'free'
		ORDER BY derivation_index
		FOR UPDATE SKIP LOCKED
		LIMIT 1`)).
		WithArgs("evm").
		WillReturnRows(sqlmock.NewRows([]string{"id", "derivation_index", "address"}).
			AddRow(7001, 400, "0xnewaddress"))
	mock.ExpectExec(q(`UPDATE deposit_addresses SET status = 'disabled' WHERE id = $1`)).
		WithArgs(int64(673)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(q(`UPDATE crypto_address_pool
		SET status = 'retired'
		WHERE chain_family = $1 AND derivation_index = $2 AND status = 'assigned'`)).
		WithArgs("evm", int64(337)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(q(`INSERT INTO deposit_addresses (user_id, chain, address, derivation_index, status)
		VALUES ($1, $2, $3, $4, 'active')
		RETURNING id`)).
		WithArgs(int64(8624), "evm", "0xnewaddress", int64(400)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(9001))
	mock.ExpectExec(q(`UPDATE crypto_address_pool
		SET status = 'assigned', assigned_user_id = $1, assigned_at = NOW()
		WHERE id = $2`)).
		WithArgs(int64(8624), int64(7001)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	oldAssignment, newAssignment, err := NewRepo(db).Rotate(t.Context(), 673)
	require.NoError(t, err)

	// The retired row must survive as disabled history, still owned by the user.
	require.Equal(t, int64(673), oldAssignment.ID)
	require.Equal(t, "0xoldaddress", oldAssignment.Address)
	require.Equal(t, "disabled", oldAssignment.Status)
	require.Equal(t, int64(8624), oldAssignment.UserID)

	// The new row is the active assignment and points at the freshly bound pool row.
	require.Equal(t, int64(9001), newAssignment.ID)
	require.Equal(t, "0xnewaddress", newAssignment.Address)
	require.Equal(t, int64(400), newAssignment.DerivationIndex)
	require.Equal(t, "active", newAssignment.Status)
	require.Equal(t, "evm", newAssignment.Family)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRotateFailsWhenPoolHasNoFreeAddress(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	mock.ExpectQuery(q(`SELECT id, user_id, chain, address, derivation_index, status
		FROM deposit_addresses WHERE id = $1 FOR UPDATE`)).
		WithArgs(int64(673)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "chain", "address", "derivation_index", "status"}).
			AddRow(673, 8624, "evm", "0xoldaddress", 337, "active"))
	mock.ExpectQuery(`FROM crypto_address_pool`).
		WithArgs("evm").
		WillReturnRows(sqlmock.NewRows([]string{"id", "derivation_index", "address"}))
	mock.ExpectRollback()

	_, _, err = NewRepo(db).Rotate(t.Context(), 673)
	require.ErrorIs(t, err, ErrNoFreeAddress)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRotateRejectsAlreadyDisabledAssignment(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	mock.ExpectQuery(q(`SELECT id, user_id, chain, address, derivation_index, status
		FROM deposit_addresses WHERE id = $1 FOR UPDATE`)).
		WithArgs(int64(673)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "chain", "address", "derivation_index", "status"}).
			AddRow(673, 8624, "evm", "0xoldaddress", 337, "disabled"))
	mock.ExpectRollback()

	_, _, err = NewRepo(db).Rotate(t.Context(), 673)
	require.ErrorIs(t, err, ErrAssignmentNotActive)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRotateRejectsUnknownAssignment(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	mock.ExpectQuery(`FROM deposit_addresses WHERE id = \$1 FOR UPDATE`).
		WithArgs(int64(404)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "chain", "address", "derivation_index", "status"}))
	mock.ExpectRollback()

	_, _, err = NewRepo(db).Rotate(t.Context(), 404)
	require.ErrorIs(t, err, ErrAssignmentNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

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
	require.False(t, errors.Is(err, ErrAssignmentNotFound))
}
