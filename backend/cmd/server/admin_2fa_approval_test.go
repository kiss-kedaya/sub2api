package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"flag"
	"io"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestDispatchAdmin2FAApprovalIsIsolatedFromRecoveryAndStartup(t *testing.T) {
	for _, tc := range []struct {
		name         string
		args         []string
		handled, run bool
	}{
		{name: "server"},
		{name: "legacy_recovery", args: []string{"-reset-admin-2fa", "-user-id", "41", "-confirm"}},
		{name: "valid", args: []string{"-issue-admin-2fa-code", "-user-id", "41"}, handled: true, run: true},
		{name: "missing_user", args: []string{"-issue-admin-2fa-code"}, handled: true},
		{name: "invalid_user", args: []string{"-issue-admin-2fa-code", "-user-id", "0"}, handled: true},
		{name: "false_issue", args: []string{"-issue-admin-2fa-code=false", "-user-id", "41"}, handled: true},
		{name: "recovery_conflict", args: []string{"-issue-admin-2fa-code", "-user-id", "41", "-reset-admin-2fa", "-confirm"}, handled: true},
		{name: "migration_conflict", args: []string{"-issue-admin-2fa-code", "-user-id", "41", "-migrate-only"}, handled: true},
		{name: "false_setup_conflict", args: []string{"-issue-admin-2fa-code", "-user-id", "41", "-setup=false"}, handled: true},
		{name: "leading_positional", args: []string{"unexpected", "-issue-admin-2fa-code", "-user-id", "41"}, handled: true},
		{name: "separator", args: []string{"--", "-issue-admin-2fa-code", "-user-id", "41"}, handled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fs := flag.NewFlagSet("approval-test", flag.ContinueOnError)
			fs.SetOutput(io.Discard)
			enabled := fs.Bool("issue-admin-2fa-code", false, "")
			userID := fs.Int64("user-id", 0, "")
			for _, name := range []string{"reset-admin-2fa", "confirm", "setup", "migrate-only"} {
				fs.Bool(name, false, "")
			}
			require.NoError(t, fs.Parse(tc.args))
			calls := 0
			handled, err := dispatchAdmin2FAApproval(fs, *enabled, *userID, func(id int64) error { calls++; require.EqualValues(t, 41, id); return nil })
			require.Equal(t, tc.handled, handled)
			if tc.run {
				require.NoError(t, err)
				require.Equal(t, 1, calls)
			} else {
				require.Zero(t, calls)
				if tc.handled {
					require.ErrorIs(t, err, errAdmin2FAApprovalArguments)
				} else {
					require.NoError(t, err)
				}
			}
		})
	}
}

func TestAdmin2FAApprovalCLIReadsOnlyAndPrintsCodeWithoutIdentityData(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	mock.ExpectQuery(admin2FAApprovalUserSQL).WithArgs(int64(41)).WillReturnRows(sqlmock.NewRows([]string{"id", "role", "status", "totp_enabled", "totp_secret_encrypted"}).AddRow(int64(41), "admin", "active", true, "PRIVATE-encrypted-secret"))
	mock.ExpectClose()
	server := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer func() { _ = rdb.Close() }()
	store, ok := repository.NewTotpCache(rdb).(service.Admin2FAApprovalStore)
	require.True(t, ok)
	closed := false
	var out bytes.Buffer
	err = executeAdmin2FAApproval(context.Background(), 41, &out,
		func() (*config.Config, error) { return &config.Config{}, nil },
		func(*config.Config) (*sql.DB, error) { return db, nil },
		func(*config.Config) (service.Admin2FAApprovalStore, func(), error) {
			return store, func() { closed = true }, nil
		})
	require.NoError(t, err)
	require.True(t, closed)
	require.NoError(t, mock.ExpectationsWereMet())
	match := regexp.MustCompile(`authorization code: ([a-f0-9]{64})\n`).FindStringSubmatch(out.String())
	require.Len(t, match, 2)
	digest, err := server.Get("totp:admin-approval:{41}")
	require.NoError(t, err)
	require.NotEqual(t, match[1], digest)
	require.NotContains(t, out.String(), "PRIVATE")
	require.NotContains(t, out.String(), "@")
}

func TestAdmin2FAApprovalCLIErrorsNeverPrintConfigSecrets(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	var out bytes.Buffer
	err := executeAdmin2FAApproval(context.Background(), 41, &out,
		func() (*config.Config, error) { return nil, errors.New("PRIVATE connection password") },
		func(*config.Config) (*sql.DB, error) { t.Fatal("database must not open"); return nil, nil },
		func(*config.Config) (service.Admin2FAApprovalStore, func(), error) {
			t.Fatal("Redis must not open")
			return nil, nil, nil
		})
	require.ErrorIs(t, err, errAdmin2FAApprovalConfig)
	require.NotContains(t, err.Error(), "PRIVATE")
	require.Empty(t, out.String())
}

func TestAdmin2FAApprovalCLIReadonlyRefusesBeforeLoadingConfig(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "true")
	err := executeAdmin2FAApproval(context.Background(), 41, io.Discard, nil, nil, nil)
	require.ErrorIs(t, err, service.ErrAdmin2FAApprovalReadOnly)
}
