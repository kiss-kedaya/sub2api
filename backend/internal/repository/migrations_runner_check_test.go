package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"testing/fstest"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestCheckMigrationsNilDB(t *testing.T) {
	require.EqualError(t, CheckMigrations(context.Background(), nil), "nil sql db")
}

func TestCheckMigrationsFSCurrentAndAllowsHistoricalRows(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	first := "CREATE TABLE first_table(id bigint);"
	second := "ALTER TABLE first_table ADD COLUMN name text;"
	mock.ExpectQuery("SELECT filename, checksum FROM schema_migrations").
		WillReturnRows(sqlmock.NewRows([]string{"filename", "checksum"}).
			AddRow("000_historical_custom.sql", "historical-checksum").
			AddRow("001_first.sql", migrationTestChecksum(first)).
			AddRow("002_second.sql", migrationTestChecksum(second)))

	err = checkMigrationsFS(context.Background(), db, fstest.MapFS{
		"001_first.sql":  &fstest.MapFile{Data: []byte(first)},
		"002_second.sql": &fstest.MapFile{Data: []byte(second)},
		"003_empty.sql":  &fstest.MapFile{Data: []byte(" \n\t")},
	})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCheckMigrationsFSPendingFailsClosed(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	first := "SELECT 1;"
	mock.ExpectQuery("SELECT filename, checksum FROM schema_migrations").
		WillReturnRows(sqlmock.NewRows([]string{"filename", "checksum"}).
			AddRow("001_first.sql", migrationTestChecksum(first)))

	err = checkMigrationsFS(context.Background(), db, fstest.MapFS{
		"001_first.sql":   &fstest.MapFile{Data: []byte(first)},
		"002_pending.sql": &fstest.MapFile{Data: []byte("SELECT 2;")},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "1 pending migration")
	require.Contains(t, err.Error(), "002_pending.sql")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCheckMigrationsFSChecksumMismatchFailsClosed(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectQuery("SELECT filename, checksum FROM schema_migrations").
		WillReturnRows(sqlmock.NewRows([]string{"filename", "checksum"}).
			AddRow("001_first.sql", "wrong"))

	err = checkMigrationsFS(context.Background(), db, fstest.MapFS{
		"001_first.sql": &fstest.MapFile{Data: []byte("SELECT 1;")},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "checksum mismatch")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCheckMigrationsFSHistoryReadFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectQuery("SELECT filename, checksum FROM schema_migrations").
		WillReturnError(errors.New("relation does not exist"))

	err = checkMigrationsFS(context.Background(), db, fstest.MapFS{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "run migrations before starting in validate mode")
	require.NoError(t, mock.ExpectationsWereMet())
}

func migrationTestChecksum(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}
