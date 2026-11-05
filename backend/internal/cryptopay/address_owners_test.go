package cryptopay

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// Every value below is a made-up fixture. Addresses are built from a digit so
// two of them never collide by accident.
func fixtureAddress(digit byte) string {
	return "0x" + strings.Repeat(string(digit), 40)
}

func mixedCase(address string) string {
	out := []byte(address)
	for i := 2; i < len(out); i++ {
		if out[i] >= 'a' && out[i] <= 'f' && i%2 == 0 {
			out[i] -= 'a' - 'A'
		}
	}
	return string(out)
}

var ownerColumns = []string{"lower", "assigned_user_id", "username", "email", "deleted"}

const ownersQueryPattern = `(?s)SELECT lower\(p\.address\), p\.assigned_user_id,\s+COALESCE\(u\.username, ''\), COALESCE\(u\.email, ''\),\s+\(u\.id IS NULL OR u\.deleted_at IS NOT NULL\)\s+FROM crypto_address_pool p\s+LEFT JOIN users u ON u\.id = p\.assigned_user_id\s+WHERE lower\(p\.address\) = ANY\(\$1\) AND p\.assigned_user_id IS NOT NULL\s+ORDER BY p\.id`

func newOwnersService(t *testing.T) (*Service, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return &Service{db: db}, mock
}

func TestNormalizeOwnerAddressesValidatesLowercasesAndDeduplicates(t *testing.T) {
	a, b := fixtureAddress('a'), fixtureAddress('b')
	got := normalizeOwnerAddresses([]string{
		mixedCase(a), a, strings.ToUpper(a[2:]), // same address three ways; the bare-hex form is invalid (no 0x)
		"  " + b + "  ",                                                          // surrounding whitespace is tolerated
		"", "0x1234", "not-an-address", "0x" + strings.Repeat("g", 40), a + "00", // invalid: dropped
		"SoLaNaAddressIsNotEVM1111111111111111111111",
	})
	require.Equal(t, []string{a, b}, got, "first-seen order, lower-case, no duplicates, invalid ignored")
	require.Empty(t, normalizeOwnerAddresses(nil))
}

func TestLookupAddressOwnersMatchesAcrossCaseAndKeysByLowercase(t *testing.T) {
	s, mock := newOwnersService(t)
	a, b := fixtureAddress('a'), fixtureAddress('b')
	// The caller sends checksummed/upper-case forms; the stored value is
	// lower-cased inside SQL, and the query receives lower-case parameters.
	mock.ExpectQuery(ownersQueryPattern).
		WithArgs(pq.Array([]string{a, b})).
		WillReturnRows(sqlmock.NewRows(ownerColumns).
			AddRow(a, 11, "fixture-name", "fixture-user@example.test", false).
			AddRow(strings.ToUpper(b[:2])+b[2:], 12, "", "other@example.test", false))
	got, err := s.LookupAddressOwners(t.Context(), []string{mixedCase(a), b, a})
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, AddressOwner{UserID: 11, Username: "fixture-name", Email: "fixture-user@example.test"}, got[a])
	require.Equal(t, AddressOwner{UserID: 12, Email: "other@example.test"}, got[b])
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLookupAddressOwnersReportsDeletedUsersWithoutDroppingTheRow(t *testing.T) {
	s, mock := newOwnersService(t)
	a, b := fixtureAddress('a'), fixtureAddress('b')
	mock.ExpectQuery(ownersQueryPattern).
		WithArgs(pq.Array([]string{a, b})).
		WillReturnRows(sqlmock.NewRows(ownerColumns).
			AddRow(a, 21, "gone", "gone@example.test", true). // soft-deleted
			AddRow(b, 22, "", "", true))                      // user row no longer exists
	got, err := s.LookupAddressOwners(t.Context(), []string{a, b})
	require.NoError(t, err)
	require.True(t, got[a].Deleted)
	require.Equal(t, int64(21), got[a].UserID)
	require.True(t, got[b].Deleted)
	require.Equal(t, AddressOwner{UserID: 22, Deleted: true}, got[b])
}

func TestLookupAddressOwnersQueryDoesNotFilterOutSoftDeletedUsers(t *testing.T) {
	// A deleted_at filter on the join (or in WHERE) would silently drop owners
	// that the console must still show as "deleted".
	require.NotContains(t, addressOwnersQuery, "deleted_at IS NULL")
	require.Contains(t, addressOwnersQuery, "LEFT JOIN users")
	require.Contains(t, addressOwnersQuery, "lower(p.address) = ANY($1)")
}

func TestLookupAddressOwnersOmitsAddressesWithoutAnOwner(t *testing.T) {
	s, mock := newOwnersService(t)
	a, b := fixtureAddress('a'), fixtureAddress('b')
	mock.ExpectQuery(ownersQueryPattern).
		WithArgs(pq.Array([]string{a, b})).
		WillReturnRows(sqlmock.NewRows(ownerColumns).AddRow(a, 1, "x", "x@example.test", false))
	got, err := s.LookupAddressOwners(t.Context(), []string{a, b})
	require.NoError(t, err)
	require.Contains(t, got, a)
	require.NotContains(t, got, b, "an address with no row is absent, not an empty owner")
}

func TestLookupAddressOwnersKeepsTheFirstRowForADuplicatedAddress(t *testing.T) {
	s, mock := newOwnersService(t)
	a := fixtureAddress('a')
	mock.ExpectQuery(ownersQueryPattern).WithArgs(pq.Array([]string{a})).
		WillReturnRows(sqlmock.NewRows(ownerColumns).
			AddRow(a, 1, "first", "first@example.test", false).
			AddRow(a, 2, "second", "second@example.test", false))
	got, err := s.LookupAddressOwners(t.Context(), []string{a})
	require.NoError(t, err)
	require.Equal(t, int64(1), got[a].UserID, "rows arrive ordered by pool id; the earliest assignment wins")
}

func TestLookupAddressOwnersDeduplicatesBeforeCountingTheCap(t *testing.T) {
	s, mock := newOwnersService(t)
	distinct := make([]string, 0, MaxOwnerLookupAddresses)
	for i := 0; i < MaxOwnerLookupAddresses; i++ {
		distinct = append(distinct, fmt.Sprintf("0x%040x", i+1))
	}
	// 100 distinct + a repeat + garbage is still a valid request.
	request := append(append([]string{}, distinct...), mixedCase(distinct[0]), distinct[1], "junk", "0x12")
	mock.ExpectQuery(ownersQueryPattern).WithArgs(pq.Array(distinct)).WillReturnRows(sqlmock.NewRows(ownerColumns))
	got, err := s.LookupAddressOwners(t.Context(), request)
	require.NoError(t, err)
	require.Empty(t, got)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLookupAddressOwnersRejectsMoreThanTheCapWithoutQuerying(t *testing.T) {
	s, mock := newOwnersService(t) // no expectation: any query would fail the test
	tooMany := make([]string, 0, MaxOwnerLookupAddresses+1)
	for i := 0; i < MaxOwnerLookupAddresses+1; i++ {
		tooMany = append(tooMany, fmt.Sprintf("0x%040x", i+1))
	}
	got, err := s.LookupAddressOwners(t.Context(), tooMany)
	require.ErrorIs(t, err, ErrTooManyOwnerAddresses)
	require.Nil(t, got)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLookupAddressOwnersIgnoresInvalidInputWithoutQuerying(t *testing.T) {
	s, mock := newOwnersService(t)
	junk := make([]string, 0, 400)
	for i := 0; i < 400; i++ {
		junk = append(junk, fmt.Sprintf("invalid-%d", i)) // many, but none valid: not a "too many"
	}
	got, err := s.LookupAddressOwners(t.Context(), junk)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Empty(t, got)
	got, err = s.LookupAddressOwners(t.Context(), nil)
	require.NoError(t, err)
	require.Empty(t, got)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLookupAddressOwnersWithoutADatabaseFailsClosed(t *testing.T) {
	_, err := (&Service{}).LookupAddressOwners(t.Context(), []string{fixtureAddress('a')})
	require.ErrorIs(t, err, ErrOwnerLookupFailed)
	var nilService *Service
	_, err = nilService.LookupAddressOwners(t.Context(), []string{fixtureAddress('a')})
	require.ErrorIs(t, err, ErrOwnerLookupFailed)
}

func TestLookupAddressOwnersNeverEchoesUserDataInErrors(t *testing.T) {
	const email = "leaky-person@example.test"
	a := fixtureAddress('a')

	t.Run("query failure", func(t *testing.T) {
		s, mock := newOwnersService(t)
		mock.ExpectQuery(ownersQueryPattern).WillReturnError(errors.New("pq: failure while reading " + email))
		got, err := s.LookupAddressOwners(t.Context(), []string{a})
		require.ErrorIs(t, err, ErrOwnerLookupFailed)
		require.Nil(t, got)
		require.Equal(t, ErrOwnerLookupFailed.Error(), err.Error())
		require.NotContains(t, err.Error(), email)
	})

	t.Run("scan failure", func(t *testing.T) {
		// database/sql embeds the offending value in a conversion error. A
		// non-numeric user id column makes that happen with a value we control.
		s, mock := newOwnersService(t)
		mock.ExpectQuery(ownersQueryPattern).WillReturnRows(sqlmock.NewRows(ownerColumns).
			AddRow(a, email, "name", email, false))
		got, err := s.LookupAddressOwners(t.Context(), []string{a})
		require.ErrorIs(t, err, ErrOwnerLookupFailed)
		require.Nil(t, got)
		require.Equal(t, ErrOwnerLookupFailed.Error(), err.Error())
		require.NotContains(t, err.Error(), email)
	})

	t.Run("iteration failure", func(t *testing.T) {
		s, mock := newOwnersService(t)
		mock.ExpectQuery(ownersQueryPattern).WillReturnRows(sqlmock.NewRows(ownerColumns).
			AddRow(a, 1, "name", email, false).
			RowError(0, errors.New("broken row for "+email)))
		got, err := s.LookupAddressOwners(t.Context(), []string{a})
		require.ErrorIs(t, err, ErrOwnerLookupFailed)
		require.Nil(t, got)
		require.NotContains(t, err.Error(), email)
	})
}

func TestAddressOwnerJSONShape(t *testing.T) {
	encoded, err := json.Marshal(map[string]AddressOwner{
		fixtureAddress('a'): {UserID: 5, Username: "fixture-name", Email: "fixture@example.test", Deleted: true},
	})
	require.NoError(t, err)
	require.JSONEq(t, `{"`+fixtureAddress('a')+`":{"user_id":5,"username":"fixture-name","email":"fixture@example.test","deleted":true}}`, string(encoded))
}
