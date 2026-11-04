package cryptopay

import (
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/addresses"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/mode"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/wallet"
	"github.com/stretchr/testify/require"
)

func TestNormalizeDepositConfirmIDs(t *testing.T) {
	got, err := NormalizeDepositConfirmIDs([]int64{302, 321, 302, 5, 321})
	require.NoError(t, err)
	require.Equal(t, []int64{302, 321, 5}, got, "duplicates dropped, first-seen order kept")

	distinct := make([]int64, MaxDepositConfirmIDs)
	for i := range distinct {
		distinct[i] = int64(i + 1)
	}
	got, err = NormalizeDepositConfirmIDs(append(distinct, distinct...))
	require.NoError(t, err, "50 distinct ids are fine however often they repeat")
	require.Len(t, got, MaxDepositConfirmIDs)

	for name, ids := range map[string][]int64{
		"nil": nil, "empty": {}, "zero": {0}, "negative": {-3}, "zero among valid": {1, 2, 0},
		"too many": append(distinct, int64(MaxDepositConfirmIDs+1)),
	} {
		_, err := NormalizeDepositConfirmIDs(ids)
		require.ErrorIs(t, err, ErrDepositConfirmInvalid, name)
	}
}

func gateService(t *testing.T) (*Service, sqlmock.Sqlmock) {
	t.Helper()
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "")
	t.Setenv("CRYPTO_ADMIN_TEST_WRITE_ENABLED", "")
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return &Service{db: db, creditor: &DepositCreditor{}, addresses: &addresses.Service{}, pools: map[string]*rpc.Pool{}}, mock
}

func TestConfirmDepositsRefusesWholeRequestsBeforeTouchingTheDatabase(t *testing.T) {
	var absent *Service
	_, err := absent.ConfirmDeposits(t.Context(), 1, []int64{1}, "")
	require.ErrorIs(t, err, ErrDepositConfirmUnavailable)

	for name, mutate := range map[string]func(*Service){
		"no database":        func(s *Service) { s.db = nil },
		"no creditor":        func(s *Service) { s.creditor = nil },
		"no address service": func(s *Service) { s.addresses = nil },
	} {
		t.Run(name, func(t *testing.T) {
			s, _ := gateService(t)
			mutate(s)
			_, err := s.ConfirmDeposits(t.Context(), 1, []int64{1}, "")
			require.ErrorIs(t, err, ErrDepositConfirmUnavailable)
		})
	}

	t.Run("read-only review instance", func(t *testing.T) {
		s, mock := gateService(t)
		t.Setenv("CRYPTO_ADMIN_TEST_MODE", "true")
		_, err := s.ConfirmDeposits(t.Context(), 1, []int64{1}, "")
		require.ErrorIs(t, err, ErrDepositConfirmReadOnly)
		require.ErrorIs(t, err, mode.ErrReadOnly)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("invalid operator or ids", func(t *testing.T) {
		s, mock := gateService(t)
		_, err := s.ConfirmDeposits(t.Context(), 0, []int64{1}, "")
		require.ErrorIs(t, err, ErrDepositConfirmInvalid)
		_, err = s.ConfirmDeposits(t.Context(), -4, []int64{1}, "")
		require.ErrorIs(t, err, ErrDepositConfirmInvalid)
		for _, ids := range [][]int64{nil, {}, {0}, {-1}, {1, 0}} {
			_, err = s.ConfirmDeposits(t.Context(), 1, ids, "")
			require.ErrorIs(t, err, ErrDepositConfirmInvalid)
		}
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("security pause", func(t *testing.T) {
		s, mock := gateService(t)
		s.securityPaused.Store(true)
		_, err := s.ConfirmDeposits(t.Context(), 1, []int64{1}, "")
		require.ErrorIs(t, err, ErrDepositConfirmSecurityLocked)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestConfirmDepositsFailsClosedOnTheWalletSecurityState(t *testing.T) {
	newWallet := func(t *testing.T, s *Service) {
		t.Helper()
		s.wallet = wallet.New(s.db, strings.Repeat("ab", 32))
		s.wallet.EnableSecurityMonitoring()
	}
	const vaultQuery = `SELECT value FROM settings`

	t.Run("security state cannot be read", func(t *testing.T) {
		s, mock := gateService(t)
		newWallet(t, s)
		mock.ExpectQuery(vaultQuery).WillReturnError(errors.New("connection refused by 10.1.2.3 as secret-user"))
		_, err := s.ConfirmDeposits(t.Context(), 1, []int64{1}, "")
		require.ErrorIs(t, err, ErrDepositConfirmUnavailable)
		require.NotContains(t, err.Error(), "secret-user")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("vault is present but unreadable counts as locked", func(t *testing.T) {
		s, mock := gateService(t)
		newWallet(t, s)
		mock.ExpectQuery(vaultQuery).WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow("not-a-vault"))
		_, err := s.ConfirmDeposits(t.Context(), 1, []int64{1}, "")
		require.ErrorIs(t, err, ErrDepositConfirmSecurityLocked)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	// With no vault yet the lock is not engaged, so the request proceeds. The
	// per-id work then reaches the PostgreSQL advisory lock, which proves the
	// real locker is the one wired in. Its failure is one failed id, not a failed
	// request.
	t.Run("no vault yet passes the gate and reaches the real locker", func(t *testing.T) {
		s, mock := gateService(t)
		newWallet(t, s)
		mock.ExpectQuery(vaultQuery).WillReturnRows(sqlmock.NewRows([]string{"value"}))
		mock.ExpectQuery(`pg_try_advisory_lock`).WillReturnError(errors.New("lock backend down"))
		summary, err := s.ConfirmDeposits(t.Context(), 7, []int64{11, 11}, "  check  ")
		require.NoError(t, err)
		require.Len(t, summary.Results, 1, "duplicate ids are processed once")
		require.Equal(t, DepositConfirmError, summary.Results[0].Status)
		require.EqualValues(t, 11, summary.Results[0].ID)
		require.Equal(t, 1, summary.Failed)
		require.Zero(t, summary.Credited)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func newLockerMock(t *testing.T) (pgDepositLocker, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return pgDepositLocker{db: db}, mock
}

func TestPGDepositLockerHoldsOneSessionLockPerDepositAndReleasesIt(t *testing.T) {
	locker, mock := newLockerMock(t)
	space, key := depositLockKeys(77)
	mock.ExpectQuery(`SELECT pg_try_advisory_lock\(\$1, \$2\)`).WithArgs(int64(space), int64(key)).
		WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(true))
	mock.ExpectQuery(`SELECT pg_advisory_unlock\(\$1, \$2\)`).WithArgs(int64(space), int64(key)).
		WillReturnRows(sqlmock.NewRows([]string{"unlocked"}).AddRow(true))
	release, err := locker.TryLock(t.Context(), 77)
	require.NoError(t, err)
	require.NotNil(t, release)
	require.NoError(t, release())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPGDepositLockerReportsContentionAsNilWithoutAnError(t *testing.T) {
	locker, mock := newLockerMock(t)
	mock.ExpectQuery(`pg_try_advisory_lock`).WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(false))
	release, err := locker.TryLock(t.Context(), 5)
	require.NoError(t, err)
	require.Nil(t, release, "another confirmation holds this deposit")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPGDepositLockerErrors(t *testing.T) {
	t.Run("acquire failure", func(t *testing.T) {
		locker, mock := newLockerMock(t)
		mock.ExpectQuery(`pg_try_advisory_lock`).WillReturnError(errors.New("driver exploded"))
		release, err := locker.TryLock(t.Context(), 5)
		require.Nil(t, release)
		require.ErrorContains(t, err, "acquire deposit lock")
	})
	t.Run("unlock reports not held", func(t *testing.T) {
		locker, mock := newLockerMock(t)
		mock.ExpectQuery(`pg_try_advisory_lock`).WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(true))
		mock.ExpectQuery(`pg_advisory_unlock`).WillReturnRows(sqlmock.NewRows([]string{"unlocked"}).AddRow(false))
		release, err := locker.TryLock(t.Context(), 5)
		require.NoError(t, err)
		require.ErrorContains(t, release(), "was not held")
	})
	t.Run("unlock query fails", func(t *testing.T) {
		locker, mock := newLockerMock(t)
		mock.ExpectQuery(`pg_try_advisory_lock`).WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(true))
		mock.ExpectQuery(`pg_advisory_unlock`).WillReturnError(errors.New("link down"))
		release, err := locker.TryLock(t.Context(), 5)
		require.NoError(t, err)
		require.ErrorContains(t, release(), "release deposit lock")
	})
	t.Run("a one-connection pool cannot hold the lock and still run the ledger", func(t *testing.T) {
		locker, mock := newLockerMock(t)
		locker.db.SetMaxOpenConns(1)
		release, err := locker.TryLock(t.Context(), 5)
		require.Nil(t, release)
		require.ErrorContains(t, err, "at least two database connections")
		require.NoError(t, mock.ExpectationsWereMet(), "no query may be issued")
	})
	t.Run("no database", func(t *testing.T) {
		release, err := pgDepositLocker{}.TryLock(t.Context(), 5)
		require.Nil(t, release)
		require.Error(t, err)
	})
}

func TestDepositLockKeysAreStableInRangeAndDistinctForRealisticIDs(t *testing.T) {
	seen := map[int32]int64{}
	for id := int64(1); id <= 5000; id++ {
		space, key := depositLockKeys(id)
		againSpace, againKey := depositLockKeys(id)
		require.Equal(t, space, againSpace)
		require.Equal(t, key, againKey)
		require.Equal(t, depositConfirmLockNamespace, space)
		require.GreaterOrEqual(t, key, int32(0))
		if other, duplicate := seen[key]; duplicate {
			t.Fatalf("ids %d and %d share advisory key %d", other, id, key)
		}
		seen[key] = id
	}
	for _, id := range []int64{1 << 31, 1<<32 + 5, 1 << 62, math.MaxInt64} {
		_, key := depositLockKeys(id)
		require.GreaterOrEqual(t, key, int32(0), "huge ids still fold into a valid int4")
	}
}

func TestSelectDepositRPCPoolPrefersTheSharedPoolAndFallsBackToStoredEndpoints(t *testing.T) {
	shared := rpc.NewPool("https://shared.example.test")
	got, err := selectDepositRPCPool(shared, "https://configured.example.test")
	require.NoError(t, err)
	require.Same(t, shared, got, "the shared pool keeps its discovery provenance and policy")

	got, err = selectDepositRPCPool(rpc.NewPool(""), " https://a.example.test , https://b.example.test ")
	require.NoError(t, err)
	require.Equal(t, []string{"https://a.example.test", "https://b.example.test"}, got.Endpoints())
	require.False(t, got.RequiresIndependentVerification(), "stored endpoints are operator-trusted like any manual node")

	got, err = selectDepositRPCPool(nil, "https://only.example.test")
	require.NoError(t, err)
	require.Equal(t, []string{"https://only.example.test"}, got.Endpoints())

	for _, configured := range []string{"", "   ", "\n"} {
		_, err = selectDepositRPCPool(nil, configured)
		require.ErrorIs(t, err, errDepositNoRPC)
		_, err = selectDepositRPCPool(rpc.NewPool(""), configured)
		require.ErrorIs(t, err, errDepositNoRPC)
	}
}

// BSC is not in crypto.config.chains, so the worker has no pool for it. The
// feature must still work from the stored cfg.RPC entry without touching the
// shared pool map.
func TestServiceDepositPoolServesAChainThatIsNotScanned(t *testing.T) {
	scanned := rpc.NewPool("https://scanned.example.test")
	s := &Service{
		pools:  map[string]*rpc.Pool{ChainEthereum: scanned},
		config: CryptoConfig{RPC: map[string]string{ChainBSC: "https://bsc.example.test"}},
	}
	got, err := s.depositPool(ChainEthereum)
	require.NoError(t, err)
	require.Same(t, scanned, got)

	first, err := s.depositPool(ChainBSC)
	require.NoError(t, err)
	require.Equal(t, []string{"https://bsc.example.test"}, first.Endpoints())
	second, err := s.depositPool(ChainBSC)
	require.NoError(t, err)
	require.NotSame(t, first, second, "a temporary pool is built per call, never cached or shared")
	require.NotContains(t, s.pools, ChainBSC, "the shared pool map is never mutated")

	_, err = s.depositPool(ChainPolygon)
	require.ErrorIs(t, err, errDepositNoRPC)
}
