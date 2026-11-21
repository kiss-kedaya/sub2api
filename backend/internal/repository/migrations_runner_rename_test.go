package repository

import (
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

func TestCheckMigrationsRecognizesRenamedHistory(t *testing.T) {
	for _, pair := range [][2]string{
		{"324_user_debt.sql", "323_user_debt.sql"},
		{"325_crypto_signer_audit_actor.sql", "323_crypto_signer_audit_actor.sql"},
	} {
		current, previous := pair[0], pair[1]
		t.Run(current, func(t *testing.T) {
			content, err := migrations.FS.ReadFile(current)
			require.NoError(t, err)
			checksum := migrationTestChecksum(strings.TrimSpace(string(content)))
			for _, tc := range []struct {
				name, old, currentChecksum, wantErr string
				modified                            bool
			}{
				{name: "previous filename only", old: checksum},
				{name: "both valid", old: checksum, currentChecksum: checksum},
				{name: "current filename only", currentChecksum: checksum},
				{name: "no history", wantErr: "pending migration"},
				{name: "previous checksum mismatch", old: "wrong", wantErr: "checksum mismatch for previous name"},
				{name: "current mismatch cannot fall back", currentChecksum: "wrong", old: checksum, wantErr: "checksum mismatch"},
				{name: "matching hash of different SQL", old: migrationTestChecksum("SELECT 1;"), modified: true, wantErr: "checksum mismatch for previous name"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					db, mock, err := sqlmock.New()
					require.NoError(t, err)
					defer func() { _ = db.Close() }()
					rows := sqlmock.NewRows([]string{"filename", "checksum"}).AddRow("323_unrelated.sql", checksum)
					if tc.old != "" {
						rows.AddRow(previous, tc.old)
					}
					if tc.currentChecksum != "" {
						rows.AddRow(current, tc.currentChecksum)
					}
					mock.ExpectQuery("SELECT filename, checksum FROM schema_migrations").WillReturnRows(rows)
					data := content
					if tc.modified {
						data = []byte("SELECT 1;")
					}
					err = checkMigrationsFS(t.Context(), db, fstest.MapFS{current: {Data: data}})
					if tc.wantErr != "" {
						require.ErrorContains(t, err, tc.wantErr)
					} else {
						require.NoError(t, err)
					}
					require.NoError(t, mock.ExpectationsWereMet())
				})
			}
		})
	}
}

func TestApplyMigrationsRenamedHistory(t *testing.T) {
	const current = "324_user_debt.sql"
	const previous = "323_user_debt.sql"
	const lookup = "SELECT checksum FROM schema_migrations WHERE filename = $1"
	content, err := migrations.FS.ReadFile(current)
	require.NoError(t, err)
	checksum := migrationTestChecksum(strings.TrimSpace(string(content)))
	lookupErr := errors.New("history read failed")
	for _, tc := range []struct {
		name, oldChecksum, wantErr string
		oldErr                     error
		fresh                      bool
	}{
		{name: "keep previous entry and skip SQL", oldChecksum: checksum},
		{name: "modified previous entry stops execution", oldChecksum: "wrong", wantErr: "checksum mismatch for previous name"},
		{name: "history read failure stops execution", oldErr: lookupErr, wantErr: lookupErr.Error()},
		{name: "fresh database applies and records current name", oldErr: sql.ErrNoRows, fresh: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			prepareMigrationsBootstrapExpectations(mock)
			mock.ExpectQuery(regexp.QuoteMeta(lookup)).WithArgs(current).WillReturnError(sql.ErrNoRows)
			old := mock.ExpectQuery(regexp.QuoteMeta(lookup)).WithArgs(previous)
			if tc.oldErr == nil {
				old.WillReturnRows(sqlmock.NewRows([]string{"checksum"}).AddRow(tc.oldChecksum))
			} else {
				old.WillReturnError(tc.oldErr)
			}
			if tc.fresh {
				mock.ExpectBegin()
				mock.ExpectExec(regexp.QuoteMeta(strings.TrimSpace(string(content)))).WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectExec(regexp.QuoteMeta("INSERT INTO schema_migrations (filename, checksum) VALUES ($1, $2)")).
					WithArgs(current, checksum).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit()
			}
			mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_unlock($1)")).WithArgs(migrationsAdvisoryLockID).WillReturnResult(sqlmock.NewResult(0, 1))
			err = applyMigrationsFS(t.Context(), db, fstest.MapFS{current: {Data: content}})
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
			} else {
				require.NoError(t, err)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
