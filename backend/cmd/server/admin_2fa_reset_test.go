package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"flag"
	"io"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func TestDispatchAdmin2FAResetRequiresExplicitIsolatedConfirmedMode(t *testing.T) {
	for _, tc := range []struct {
		name    string
		args    []string
		handled bool
		run     bool
	}{
		{name: "normal_server"},
		{name: "normal_migration", args: []string{"-migrate-only"}},
		{name: "valid", args: []string{"-reset-admin-2fa", "-user-id", "41", "-confirm"}, handled: true, run: true},
		{name: "missing_confirmation", args: []string{"-reset-admin-2fa", "-user-id", "41"}, handled: true},
		{name: "missing_user", args: []string{"-reset-admin-2fa", "-confirm"}, handled: true},
		{name: "zero_user", args: []string{"-reset-admin-2fa", "-user-id", "0", "-confirm"}, handled: true},
		{name: "negative_user", args: []string{"-reset-admin-2fa", "-user-id", "-1", "-confirm"}, handled: true},
		{name: "user_without_mode", args: []string{"-user-id", "41", "-confirm"}, handled: true},
		{name: "explicit_zero_without_mode", args: []string{"-user-id=0"}, handled: true},
		{name: "explicit_false_mode", args: []string{"-reset-admin-2fa=false"}, handled: true},
		{name: "confirmation_without_mode", args: []string{"-confirm"}, handled: true},
		{name: "false_confirmation", args: []string{"-reset-admin-2fa", "-user-id", "41", "-confirm=false"}, handled: true},
		{name: "conflicting_setup", args: []string{"-reset-admin-2fa", "-user-id", "41", "-confirm", "-setup"}, handled: true},
		{name: "conflicting_version", args: []string{"-reset-admin-2fa", "-user-id", "41", "-confirm", "-version"}, handled: true},
		{name: "conflicting_migration", args: []string{"-reset-admin-2fa", "-user-id", "41", "-confirm", "-migrate-only"}, handled: true},
		{name: "conflicting_validation", args: []string{"-reset-admin-2fa", "-user-id", "41", "-confirm", "-check-migrations"}, handled: true},
		{name: "conflicting_false_setup", args: []string{"-reset-admin-2fa", "-user-id", "41", "-confirm", "-setup=false"}, handled: true},
		{name: "positional_arguments", args: []string{"-reset-admin-2fa", "-user-id", "41", "-confirm", "unexpected"}, handled: true},
		{name: "leading_positional", args: []string{"unexpected", "-reset-admin-2fa", "-user-id", "41", "-confirm"}, handled: true},
		{name: "mode_after_separator", args: []string{"--", "-reset-admin-2fa", "-user-id", "41", "-confirm"}, handled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fs := flag.NewFlagSet("recovery-test", flag.ContinueOnError)
			fs.SetOutput(io.Discard)
			enabled := fs.Bool("reset-admin-2fa", false, "")
			userID := fs.Int64("user-id", 0, "")
			confirmed := fs.Bool("confirm", false, "")
			for _, name := range []string{"setup", "version", "migrate-only", "check-migrations"} {
				fs.Bool(name, false, "")
			}
			require.NoError(t, fs.Parse(tc.args))
			calls := 0
			handled, err := dispatchAdmin2FAReset(fs, *enabled, *userID, *confirmed, func(id int64) error {
				calls++
				require.EqualValues(t, 41, id)
				return nil
			})
			require.Equal(t, tc.handled, handled)
			if tc.run {
				require.NoError(t, err)
				require.Equal(t, 1, calls)
			} else {
				require.Zero(t, calls, "invalid recovery flags must never execute configuration, database, or server work")
				if tc.handled {
					require.ErrorIs(t, err, errAdmin2FAResetArguments)
				} else {
					require.NoError(t, err)
				}
			}
		})
	}
}

func TestAdmin2FAResetDatabaseTransactionsAndStaticErrors(t *testing.T) {
	privateError := errors.New("password=PRIVATE database connection string PRIVATE")
	for _, tc := range []struct {
		name string
		want error
		mock func(sqlmock.Sqlmock)
	}{
		{name: "success", mock: func(mock sqlmock.Sqlmock) {
			mock.ExpectBegin()
			mock.ExpectQuery(admin2FAResetSQL).WithArgs(int64(41)).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(41)))
			mock.ExpectCommit()
		}},
		{name: "begin_failure", want: errAdmin2FAResetDatabase, mock: func(mock sqlmock.Sqlmock) { mock.ExpectBegin().WillReturnError(privateError) }},
		{name: "identity_not_matched", want: errAdmin2FAResetIdentity, mock: func(mock sqlmock.Sqlmock) {
			mock.ExpectBegin()
			mock.ExpectQuery(admin2FAResetSQL).WithArgs(int64(41)).WillReturnRows(sqlmock.NewRows([]string{"id"}))
			mock.ExpectRollback()
		}},
		{name: "query_failure", want: errAdmin2FAResetDatabase, mock: func(mock sqlmock.Sqlmock) {
			mock.ExpectBegin()
			mock.ExpectQuery(admin2FAResetSQL).WithArgs(int64(41)).WillReturnError(privateError)
			mock.ExpectRollback()
		}},
		{name: "unexpected_identity", want: errAdmin2FAResetDatabase, mock: func(mock sqlmock.Sqlmock) {
			mock.ExpectBegin()
			mock.ExpectQuery(admin2FAResetSQL).WithArgs(int64(41)).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(42)))
			mock.ExpectRollback()
		}},
		{name: "commit_not_confirmed", want: errAdmin2FAResetCommit, mock: func(mock sqlmock.Sqlmock) {
			mock.ExpectBegin()
			mock.ExpectQuery(admin2FAResetSQL).WithArgs(int64(41)).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(41)))
			mock.ExpectCommit().WillReturnError(privateError)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			tc.mock(mock)
			err = resetAdmin2FAInDatabase(context.Background(), db, 41)
			if tc.want == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tc.want)
				require.NotContains(t, err.Error(), "PRIVATE")
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestAdmin2FAResetOnlyChangesActiveNonDeletedAdministratorTotp(t *testing.T) {
	for _, tc := range []struct {
		name, role, status string
		deleted            bool
		wantReset          bool
	}{
		{name: "active_admin", role: "admin", status: "active", wantReset: true},
		{name: "regular_user", role: "user", status: "active"},
		{name: "disabled_admin", role: "admin", status: "disabled"},
		{name: "deleted_admin", role: "admin", status: "active", deleted: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, err := sql.Open("sqlite", ":memory:")
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			db.SetMaxOpenConns(1)
			_, err = db.Exec(`CREATE TABLE users (id INTEGER PRIMARY KEY, role TEXT, status TEXT, email TEXT, password_hash TEXT, totp_enabled BOOLEAN, totp_secret_encrypted TEXT, totp_enabled_at TEXT, updated_at TEXT, deleted_at TEXT)`)
			require.NoError(t, err)
			var deletedAt any
			if tc.deleted {
				deletedAt = "2026-10-20"
			}
			_, err = db.Exec(`INSERT INTO users VALUES (41, $1, $2, 'admin@example.com', 'unchanged-password-hash', TRUE, 'PRIVATE-SECRET', '2026-10-20', '2026-10-20', $3)`, tc.role, tc.status, deletedAt)
			require.NoError(t, err)
			err = resetAdmin2FAInDatabase(context.Background(), db, 41)
			if tc.wantReset {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, errAdmin2FAResetIdentity)
			}
			var role, status, email, password string
			var enabled bool
			var secret, enabledAt sql.NullString
			require.NoError(t, db.QueryRow(`SELECT role, status, email, password_hash, totp_enabled, totp_secret_encrypted, totp_enabled_at FROM users WHERE id = 41`).Scan(&role, &status, &email, &password, &enabled, &secret, &enabledAt))
			require.Equal(t, tc.role, role)
			require.Equal(t, tc.status, status)
			require.Equal(t, "admin@example.com", email)
			require.Equal(t, "unchanged-password-hash", password)
			require.Equal(t, !tc.wantReset, enabled)
			require.Equal(t, !tc.wantReset, secret.Valid)
			require.Equal(t, !tc.wantReset, enabledAt.Valid)
		})
	}
}

func TestExecuteAdmin2FAResetReportsCommittedRecoveryAndJWTLimit(t *testing.T) {
	for _, cleanupFails := range []bool{false, true} {
		t.Run(map[bool]string{false: "sessions_cleared", true: "redis_unavailable"}[cleanupFails], func(t *testing.T) {
			db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual), sqlmock.MonitorPingsOption(true))
			require.NoError(t, err)
			cleanupCalls := 0
			mock.ExpectBegin()
			mock.ExpectQuery(admin2FAResetSQL).WithArgs(int64(41)).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(41)))
			mock.ExpectCommit()
			mock.ExpectPing()
			mock.ExpectClose()
			var out bytes.Buffer
			err = executeAdmin2FAReset(context.Background(), 41, &out,
				func() (*config.Config, error) { return &config.Config{}, nil },
				func(*config.Config) (*sql.DB, error) { return db, nil },
				func(_ context.Context, _ *config.Config, userID int64) error {
					require.EqualValues(t, 41, userID)
					// The ordered Ping expectation proves cleanup starts after commit.
					require.NoError(t, db.PingContext(context.Background()))
					cleanupCalls++
					if cleanupFails {
						return errors.New("redis://password=PRIVATE")
					}
					return nil
				})
			require.NoError(t, err)
			require.Equal(t, 1, cleanupCalls)
			require.Contains(t, out.String(), "has been reset")
			require.Contains(t, out.String(), "Existing access JWTs remain valid until their current expiry")
			require.NotContains(t, out.String(), "PRIVATE")
			if cleanupFails {
				require.Contains(t, out.String(), "database recovery completed, but")
				require.Contains(t, out.String(), "some sessions may remain active")
			} else {
				require.Contains(t, out.String(), "discoverable step-up grants have been revoked")
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestExecuteAdmin2FAResetRejectsInvalidIDAndRedactsConfigOrConnectionErrors(t *testing.T) {
	for _, stage := range []string{"invalid_id", "config", "database"} {
		t.Run(stage, func(t *testing.T) {
			var out bytes.Buffer
			userID := int64(41)
			if stage == "invalid_id" {
				userID = 0
			}
			loadCalls, openCalls, cleanupCalls := 0, 0, 0
			err := executeAdmin2FAReset(context.Background(), userID, &out,
				func() (*config.Config, error) {
					loadCalls++
					if stage == "config" {
						return nil, errors.New("config PRIVATE")
					}
					return &config.Config{}, nil
				},
				func(*config.Config) (*sql.DB, error) {
					openCalls++
					return nil, errors.New("postgres://password=PRIVATE")
				},
				func(context.Context, *config.Config, int64) error { cleanupCalls++; return nil })
			require.Error(t, err)
			require.NotContains(t, err.Error(), "PRIVATE")
			require.Empty(t, out.String())
			require.Zero(t, cleanupCalls)
			if stage == "invalid_id" {
				require.Zero(t, loadCalls)
			}
			if stage != "database" {
				require.Zero(t, openCalls)
			}
		})
	}
}

func TestAdmin2FAResetSessionCleanupOnlyTouchesTargetUserIndexedSessions(t *testing.T) {
	server := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer func() { _ = rdb.Close() }()
	cache := repository.NewRefreshTokenCache(rdb)
	ctx := context.Background()
	for _, session := range []struct {
		userID       int64
		hash, family string
	}{{41, "own-a", "family-a"}, {41, "own-b", "family-b"}, {42, "other", "family-other"}} {
		require.NoError(t, cache.StoreRefreshToken(ctx, session.hash, &service.RefreshTokenData{UserID: session.userID, FamilyID: session.family}, time.Hour))
		require.NoError(t, cache.AddToUserTokenSet(ctx, session.userID, session.hash, time.Hour))
	}
	for _, key := range []string{"totp:stepup:41:u41", "totp:stepup:41:family-a", "totp:stepup:41:family-b", "totp:setup:41", "totp:attempts:41", "totp:stepup:42:family-other", "sched:acc:123"} {
		require.NoError(t, rdb.Set(ctx, key, "PRIVATE", time.Hour).Err())
	}
	require.NoError(t, clearAdmin2FAResetSessionsWithCache(ctx, cache, rdb, 41))
	for _, key := range []string{"refresh_token:own-a", "refresh_token:own-b", "user_refresh_tokens:41", "totp:stepup:41:u41", "totp:stepup:41:family-a", "totp:stepup:41:family-b", "totp:setup:41", "totp:attempts:41"} {
		require.False(t, server.Exists(key), key)
	}
	for _, key := range []string{"refresh_token:other", "user_refresh_tokens:42", "totp:stepup:42:family-other", "sched:acc:123"} {
		require.True(t, server.Exists(key), key)
	}
}

func TestAdmin2FAResetCorruptSessionIndexCannotRevokeAnotherUser(t *testing.T) {
	server := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer func() { _ = rdb.Close() }()
	cache := repository.NewRefreshTokenCache(rdb)
	ctx := context.Background()
	require.NoError(t, cache.StoreRefreshToken(ctx, "foreign", &service.RefreshTokenData{UserID: 42, FamilyID: "other-family"}, time.Hour))
	require.NoError(t, cache.AddToUserTokenSet(ctx, 41, "foreign", time.Hour))
	require.NoError(t, rdb.Set(ctx, "totp:stepup:42:other-family", "grant", time.Hour).Err())
	err := clearAdmin2FAResetSessionsWithCache(ctx, cache, rdb, 41)
	require.Error(t, err)
	require.True(t, server.Exists("refresh_token:foreign"))
	require.True(t, server.Exists("totp:stepup:42:other-family"))
}
