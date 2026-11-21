package repository

import (
	"context"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestIPWhitelistAssistRepositoryPrefersConnectionTraces(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT client_ip, COUNT(*) AS request_count
		FROM (
			SELECT client_ip
			FROM request_connection_traces
			WHERE user_id = $1
			  AND started_at >= $2
			  AND COALESCE(client_ip, '') <> ''
			ORDER BY started_at DESC
			LIMIT $3
		) recent
		GROUP BY client_ip
		ORDER BY request_count DESC, client_ip ASC
		LIMIT $4`)).
		WithArgs(int64(42), sqlmock.AnyArg(), ipWhitelistAssistScanLimit, 5).
		WillReturnRows(sqlmock.NewRows([]string{"client_ip", "request_count"}).
			AddRow("203.0.113.7", 12).
			AddRow("198.51.100.9", 3))

	repo := NewIPWhitelistAssistRepository(db)
	ips, err := repo.ListRecentClientIPs(context.Background(), 42, 5)
	require.NoError(t, err)
	require.Len(t, ips, 2)
	require.Equal(t, "203.0.113.7", ips[0].IP)
	require.Equal(t, 12, ips[0].Count)
	require.Equal(t, "198.51.100.9", ips[1].IP)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestIPWhitelistAssistRepositoryFallsBackToAuditLogsWhenEmpty(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectQuery("FROM request_connection_traces").
		WithArgs(int64(42), sqlmock.AnyArg(), ipWhitelistAssistScanLimit, 5).
		WillReturnRows(sqlmock.NewRows([]string{"client_ip", "request_count"}))
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT client_ip, COUNT(*) AS request_count
		FROM (
			SELECT client_ip
			FROM audit_logs
			WHERE actor_user_id = $1
			  AND created_at >= $2
			  AND COALESCE(client_ip, '') <> ''
			ORDER BY created_at DESC
			LIMIT $3
		) recent
		GROUP BY client_ip
		ORDER BY request_count DESC, client_ip ASC
		LIMIT $4`)).
		WithArgs(int64(42), sqlmock.AnyArg(), ipWhitelistAssistScanLimit, 5).
		WillReturnRows(sqlmock.NewRows([]string{"client_ip", "request_count"}).
			AddRow("192.0.2.44", 7))

	repo := NewIPWhitelistAssistRepository(db)
	ips, err := repo.ListRecentClientIPs(context.Background(), 42, 5)
	require.NoError(t, err)
	require.Len(t, ips, 1)
	require.Equal(t, "192.0.2.44", ips[0].IP)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestIPWhitelistAssistRepositoryFallsBackToAuditLogsOnTraceError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectQuery("FROM request_connection_traces").
		WithArgs(int64(42), sqlmock.AnyArg(), ipWhitelistAssistScanLimit, 5).
		WillReturnError(&sqlTestTimeoutError{})
	mock.ExpectQuery("FROM audit_logs").
		WithArgs(int64(42), sqlmock.AnyArg(), ipWhitelistAssistScanLimit, 5).
		WillReturnRows(sqlmock.NewRows([]string{"client_ip", "request_count"}).
			AddRow("192.0.2.44", 1))

	repo := NewIPWhitelistAssistRepository(db)
	ips, err := repo.ListRecentClientIPs(context.Background(), 42, 5)
	require.NoError(t, err)
	require.Len(t, ips, 1)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestIPWhitelistAssistRepositoryReturnsErrorWhenBothSourcesFail(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectQuery("FROM request_connection_traces").WillReturnError(&sqlTestTimeoutError{})
	mock.ExpectQuery("FROM audit_logs").WillReturnError(&sqlTestTimeoutError{})

	repo := NewIPWhitelistAssistRepository(db)
	_, err = repo.ListRecentClientIPs(context.Background(), 42, 5)
	require.Error(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestIPWhitelistAssistRepositoryRejectsInvalidUsersWithoutQuery(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	repo := NewIPWhitelistAssistRepository(db)
	ips, err := repo.ListRecentClientIPs(context.Background(), 0, 5)
	require.NoError(t, err)
	require.Empty(t, ips)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestIPWhitelistAssistRepositoryDropsNonIPValues(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	// The list is offered for one-click insertion into the whitelist, so rows
	// that are not plain addresses (legacy host:port, hostnames) must not be
	// handed to the UI even if they exist in the trace table.
	mock.ExpectQuery("FROM request_connection_traces").
		WithArgs(int64(42), sqlmock.AnyArg(), ipWhitelistAssistScanLimit, 5).
		WillReturnRows(sqlmock.NewRows([]string{"client_ip", "request_count"}).
			AddRow("192.0.2.44", 7).
			AddRow("192.0.2.44:443", 3).
			AddRow("proxy.local", 2).
			AddRow("2001:db8::1", 1))

	repo := NewIPWhitelistAssistRepository(db)
	ips, err := repo.ListRecentClientIPs(context.Background(), 42, 5)
	require.NoError(t, err)
	require.Equal(t, []string{"192.0.2.44", "2001:db8::1"}, []string{ips[0].IP, ips[1].IP})
	require.Len(t, ips, 2)
	require.NoError(t, mock.ExpectationsWereMet())
}

type sqlTestTimeoutError struct{}

func (*sqlTestTimeoutError) Error() string { return "query canceled" }
