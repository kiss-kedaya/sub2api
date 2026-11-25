package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

// 这里的 SQL 与输出故意写成字面量而不是引用生产常量：
// 改动生产常量（例如去掉 user_id 条件）必须让测试变红，而不是跟着一起变。
const (
	literalAdmin2FAResetSQL       = "UPDATE users SET totp_enabled = FALSE, totp_secret_encrypted = NULL, totp_enabled_at = NULL, updated_at = CURRENT_TIMESTAMP WHERE id = $1 AND role = 'admin' AND status = 'active' AND deleted_at IS NULL RETURNING id"
	literalClearPasskeysSQL       = "DELETE FROM passkey_credentials WHERE user_id = $1"
	passkeyClearedOutputTemplate  = "Administrator two-factor authentication has been reset.\nRemoved %d Passkey credential(s) of this administrator.\nThe administrator now has neither TOTP nor a Passkey and must enroll a factor again through the existing flow (enable TOTP or register a new Passkey).\nRefresh sessions and discoverable step-up grants have been revoked.\nExisting access JWTs remain valid until their current expiry; this command does not revoke them.\n"
	defaultResetOutputRevoked     = "Administrator two-factor authentication has been reset.\nRefresh sessions and discoverable step-up grants have been revoked.\nExisting access JWTs remain valid until their current expiry; this command does not revoke them.\n"
	defaultResetOutputCleanupWarn = "Administrator two-factor authentication has been reset.\nWarning: database recovery completed, but refresh-session and step-up cleanup could not be completed; some sessions may remain active.\nExisting access JWTs remain valid until their current expiry; this command does not revoke them.\n"
)

func newPasskeyRecoveryFlagSet() (*flag.FlagSet, *bool, *int64, *bool, *bool) {
	fs := flag.NewFlagSet("passkey-recovery-test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	enabled := fs.Bool("reset-admin-2fa", false, "")
	userID := fs.Int64("user-id", 0, "")
	confirmed := fs.Bool("confirm", false, "")
	clearPasskeys := fs.Bool("clear-admin-passkeys", false, "")
	for _, name := range []string{"setup", "version", "migrate-only", "check-migrations", "issue-admin-2fa-code", "export-totp-key"} {
		fs.Bool(name, false, "")
	}
	return fs, enabled, userID, confirmed, clearPasskeys
}

// -clear-admin-passkeys 只允许和 -reset-admin-2fa -user-id <正整数> -confirm 一起用；
// 其余任何组合都是参数错误，且错误信息写清带开关时的正确用法。
func TestDispatchAdmin2FAResetClearPasskeysArgumentTable(t *testing.T) {
	const passkeyUsage = "-reset-admin-2fa -user-id <positive ID> -confirm -clear-admin-passkeys"
	type run struct {
		userID        int64
		clearPasskeys bool
	}
	valid := func(userID int64, clearPasskeys bool) *run { return &run{userID, clearPasskeys} }
	for _, tc := range []struct {
		name string
		args []string
		// want != nil：合法，run 必须恰好被调用一次且参数一致。
		want *run
		// wantErr != nil：必须被接管并返回该错误，run 一次都不能执行。
		wantErr error
	}{
		// 合法组合。
		{name: "valid_with_switch", args: []string{"-reset-admin-2fa", "-user-id", "41", "-confirm", "-clear-admin-passkeys"}, want: valid(41, true)},
		{name: "valid_switch_first", args: []string{"-clear-admin-passkeys", "-confirm", "-user-id", "41", "-reset-admin-2fa"}, want: valid(41, true)},
		{name: "valid_explicit_true_values", args: []string{"-reset-admin-2fa=true", "-user-id=41", "-confirm=true", "-clear-admin-passkeys=true"}, want: valid(41, true)},
		{name: "valid_without_switch_keeps_default", args: []string{"-reset-admin-2fa", "-user-id", "41", "-confirm"}, want: valid(41, false)},

		// 开关单独用 / 缺任何一项 / 取值不对。
		{name: "switch_alone", args: []string{"-clear-admin-passkeys"}, wantErr: errAdmin2FAResetPasskeyArguments},
		{name: "switch_explicit_false_alone", args: []string{"-clear-admin-passkeys=false"}, wantErr: errAdmin2FAResetPasskeyArguments},
		{name: "switch_with_confirm_only", args: []string{"-clear-admin-passkeys", "-confirm"}, wantErr: errAdmin2FAResetPasskeyArguments},
		{name: "switch_with_user_and_confirm_without_mode", args: []string{"-clear-admin-passkeys", "-user-id", "41", "-confirm"}, wantErr: errAdmin2FAResetPasskeyArguments},
		{name: "switch_without_user", args: []string{"-reset-admin-2fa", "-confirm", "-clear-admin-passkeys"}, wantErr: errAdmin2FAResetPasskeyArguments},
		{name: "switch_without_confirm", args: []string{"-reset-admin-2fa", "-user-id", "41", "-clear-admin-passkeys"}, wantErr: errAdmin2FAResetPasskeyArguments},
		{name: "switch_zero_user", args: []string{"-reset-admin-2fa", "-user-id", "0", "-confirm", "-clear-admin-passkeys"}, wantErr: errAdmin2FAResetPasskeyArguments},
		{name: "switch_negative_user", args: []string{"-reset-admin-2fa", "-user-id", "-1", "-confirm", "-clear-admin-passkeys"}, wantErr: errAdmin2FAResetPasskeyArguments},
		{name: "switch_false_confirmation", args: []string{"-reset-admin-2fa", "-user-id", "41", "-confirm=false", "-clear-admin-passkeys"}, wantErr: errAdmin2FAResetPasskeyArguments},
		{name: "switch_false_mode", args: []string{"-reset-admin-2fa=false", "-user-id", "41", "-confirm", "-clear-admin-passkeys"}, wantErr: errAdmin2FAResetPasskeyArguments},
		{name: "explicit_false_switch_is_rejected", args: []string{"-reset-admin-2fa", "-user-id", "41", "-confirm", "-clear-admin-passkeys=false"}, wantErr: errAdmin2FAResetPasskeyArguments},

		// 与其它命令 / 启动参数组合。
		{name: "switch_with_setup", args: []string{"-reset-admin-2fa", "-user-id", "41", "-confirm", "-clear-admin-passkeys", "-setup"}, wantErr: errAdmin2FAResetPasskeyArguments},
		{name: "switch_with_false_setup", args: []string{"-reset-admin-2fa", "-user-id", "41", "-confirm", "-clear-admin-passkeys", "-setup=false"}, wantErr: errAdmin2FAResetPasskeyArguments},
		{name: "switch_with_version", args: []string{"-reset-admin-2fa", "-user-id", "41", "-confirm", "-clear-admin-passkeys", "-version"}, wantErr: errAdmin2FAResetPasskeyArguments},
		{name: "switch_with_migrate_only", args: []string{"-reset-admin-2fa", "-user-id", "41", "-confirm", "-clear-admin-passkeys", "-migrate-only"}, wantErr: errAdmin2FAResetPasskeyArguments},
		{name: "switch_with_check_migrations", args: []string{"-reset-admin-2fa", "-user-id", "41", "-confirm", "-clear-admin-passkeys", "-check-migrations"}, wantErr: errAdmin2FAResetPasskeyArguments},
		{name: "switch_with_issue_code", args: []string{"-reset-admin-2fa", "-user-id", "41", "-confirm", "-clear-admin-passkeys", "-issue-admin-2fa-code"}, wantErr: errAdmin2FAResetPasskeyArguments},
		{name: "switch_with_export_key", args: []string{"-reset-admin-2fa", "-user-id", "41", "-confirm", "-clear-admin-passkeys", "-export-totp-key"}, wantErr: errAdmin2FAResetPasskeyArguments},

		// 位置参数 / flag 解析在 "--" 或第一个位置参数处停止：错位的开关也算恢复意图。
		{name: "switch_with_trailing_positional", args: []string{"-reset-admin-2fa", "-user-id", "41", "-confirm", "-clear-admin-passkeys", "unexpected"}, wantErr: errAdmin2FAResetPasskeyArguments},
		{name: "switch_with_leading_positional", args: []string{"unexpected", "-reset-admin-2fa", "-user-id", "41", "-confirm", "-clear-admin-passkeys"}, wantErr: errAdmin2FAResetPasskeyArguments},
		{name: "switch_after_separator", args: []string{"-reset-admin-2fa", "-user-id", "41", "-confirm", "--", "-clear-admin-passkeys"}, wantErr: errAdmin2FAResetPasskeyArguments},
		{name: "everything_after_separator", args: []string{"--", "-reset-admin-2fa", "-user-id", "41", "-confirm", "-clear-admin-passkeys"}, wantErr: errAdmin2FAResetPasskeyArguments},
		{name: "switch_misplaced_after_positional", args: []string{"unexpected", "-clear-admin-passkeys"}, wantErr: errAdmin2FAResetPasskeyArguments},

		// 不带开关：错误必须仍是原来的那一个（逐位一致）。
		{name: "no_switch_missing_confirm", args: []string{"-reset-admin-2fa", "-user-id", "41"}, wantErr: errAdmin2FAResetArguments},
		{name: "no_switch_missing_user", args: []string{"-reset-admin-2fa", "-confirm"}, wantErr: errAdmin2FAResetArguments},
		{name: "no_switch_with_setup", args: []string{"-reset-admin-2fa", "-user-id", "41", "-confirm", "-setup"}, wantErr: errAdmin2FAResetArguments},
		{name: "no_switch_with_positional", args: []string{"-reset-admin-2fa", "-user-id", "41", "-confirm", "unexpected"}, wantErr: errAdmin2FAResetArguments},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fs, enabled, userID, confirmed, clearPasskeys := newPasskeyRecoveryFlagSet()
			require.NoError(t, fs.Parse(tc.args))
			var calls []run
			handled, err := dispatchAdmin2FAReset(fs, *enabled, *userID, *confirmed, *clearPasskeys, func(id int64, withPasskeys bool) error {
				calls = append(calls, run{id, withPasskeys})
				return nil
			})
			require.True(t, handled, "a recovery-related flag must always consume the command")
			if tc.want != nil {
				require.NoError(t, err)
				require.Equal(t, []run{*tc.want}, calls)
				return
			}
			require.Empty(t, calls, "invalid recovery flags must never execute configuration, database, or server work")
			require.ErrorIs(t, err, tc.wantErr)
			if tc.wantErr == errAdmin2FAResetPasskeyArguments {
				require.NotErrorIs(t, err, errAdmin2FAResetArguments)
				require.Contains(t, err.Error(), passkeyUsage, "the error must spell out the correct usage")
			} else {
				require.NotErrorIs(t, err, errAdmin2FAResetPasskeyArguments)
				require.Equal(t, "administrator TOTP recovery requires only -reset-admin-2fa -user-id <positive ID> -confirm", err.Error())
			}
		})
	}
}

// 新开关不能把别的恢复命令带偏：它们各自把它当成冲突参数，
// 而 -reset-admin-2fa 组合并不归它们接管。
func TestClearAdminPasskeysSwitchConflictsWithOtherRecoveryCommands(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		// 期望被哪个分发器接管（都为 false 表示留给 -reset-admin-2fa 分发器）。
		approval, export bool
	}{
		{name: "issue_code_with_switch", args: []string{"-issue-admin-2fa-code", "-user-id", "41", "-clear-admin-passkeys"}, approval: true},
		{name: "export_key_with_switch", args: []string{"-export-totp-key", "-confirm", "-clear-admin-passkeys"}, export: true},
		{name: "reset_triple_with_switch_is_not_theirs", args: []string{"-reset-admin-2fa", "-user-id", "41", "-confirm", "-clear-admin-passkeys"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fs := flag.NewFlagSet("recovery-conflict-test", flag.ContinueOnError)
			fs.SetOutput(io.Discard)
			issue := fs.Bool("issue-admin-2fa-code", false, "")
			export := fs.Bool("export-totp-key", false, "")
			userID := fs.Int64("user-id", 0, "")
			confirmed := fs.Bool("confirm", false, "")
			for _, name := range []string{"reset-admin-2fa", "clear-admin-passkeys"} {
				fs.Bool(name, false, "")
			}
			require.NoError(t, fs.Parse(tc.args))

			approvalHandled, approvalErr := dispatchAdmin2FAApproval(fs, *issue, *userID, func(int64) error {
				t.Fatal("authorization code issuing must not run")
				return nil
			})
			require.Equal(t, tc.approval, approvalHandled)
			if tc.approval {
				require.ErrorIs(t, approvalErr, errAdmin2FAApprovalArguments)
			} else {
				require.NoError(t, approvalErr)
			}
			exportHandled, exportErr := dispatchTOTPKeyExport(fs, *export, *confirmed, func() error {
				t.Fatal("key export must not run")
				return nil
			})
			require.Equal(t, tc.export, exportHandled)
			if tc.export {
				require.ErrorIs(t, exportErr, errTOTPKeyExportArguments)
			} else {
				require.NoError(t, exportErr)
			}
		})
	}
}

// 不带开关时：SQL 与输出必须与引入开关之前逐位一致，也绝不碰 passkey_credentials
// （任何额外的 Exec 在 sqlmock 里都是未预期调用，会直接失败）。
func TestExecuteAdmin2FAResetWithoutSwitchKeepsSQLAndOutputBitForBit(t *testing.T) {
	for _, cleanupFails := range []bool{false, true} {
		t.Run(map[bool]string{false: "sessions_cleared", true: "redis_unavailable"}[cleanupFails], func(t *testing.T) {
			t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
			db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
			require.NoError(t, err)
			mock.ExpectBegin()
			mock.ExpectQuery(literalAdmin2FAResetSQL).WithArgs(int64(41)).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(41)))
			mock.ExpectCommit()
			mock.ExpectClose()
			var out bytes.Buffer
			err = executeAdmin2FAReset(context.Background(), 41, false, &out,
				func() (*config.Config, error) { return &config.Config{}, nil },
				func(*config.Config) (*sql.DB, error) { return db, nil },
				func(context.Context, *config.Config, int64) error {
					if cleanupFails {
						return errors.New("redis://password=PRIVATE")
					}
					return nil
				})
			require.NoError(t, err)
			require.NoError(t, mock.ExpectationsWereMet())
			want := defaultResetOutputRevoked
			if cleanupFails {
				want = defaultResetOutputCleanupWarn
			}
			require.Equal(t, want, out.String())
		})
	}
}

// 带开关：事务内先重置 TOTP、再删 Passkey、然后提交；提交之后才做 Redis 清理，
// 清理按 AllFactors 枚举，所以 Passkey 因子的 step-up 授权同样被撤销，旁观者不受影响。
func TestExecuteAdmin2FAResetWithPasskeySwitchRunsEveryStepInOrder(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	server := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer func() { _ = rdb.Close() }()
	refreshCache, totpCache := seedStepUpGrants(t, rdb)

	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual), sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	mock.ExpectBegin()
	mock.ExpectQuery(literalAdmin2FAResetSQL).WithArgs(int64(41)).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(41)))
	mock.ExpectExec(literalClearPasskeysSQL).WithArgs(int64(41)).WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectCommit()
	mock.ExpectPing() // 有序的 Ping 证明 Redis 清理发生在提交之后
	mock.ExpectClose()

	cleanupCalls := 0
	var out bytes.Buffer
	err = executeAdmin2FAReset(context.Background(), 41, true, &out,
		func() (*config.Config, error) { return &config.Config{}, nil },
		func(*config.Config) (*sql.DB, error) { return db, nil },
		func(ctx context.Context, _ *config.Config, userID int64) error {
			require.EqualValues(t, 41, userID)
			require.NoError(t, db.PingContext(ctx))
			cleanupCalls++
			return clearAdmin2FAResetSessionsWithCache(ctx, refreshCache, rdb, userID)
		})
	require.NoError(t, err)
	require.Equal(t, 1, cleanupCalls)
	require.NoError(t, mock.ExpectationsWereMet())
	require.Equal(t, fmt.Sprintf(passkeyClearedOutputTemplate, 3), out.String())

	ctx := context.Background()
	for _, session := range []string{"family-a", "family-b", "u41"} {
		granted, err := totpCache.HasStepUpGrant(ctx, 41, session, service.FactorPasskey)
		require.NoError(t, err)
		require.False(t, granted, "the Passkey factor's step-up grant must be revoked: session=%s", session)
	}
	require.Zero(t, countRemainingTargetGrants(t, totpCache))
	for _, factor := range stepUpGrantFactors {
		granted, err := totpCache.HasStepUpGrant(ctx, 42, "family-other", factor)
		require.NoError(t, err)
		require.True(t, granted, "other users' grants must survive: factor=%s", factor)
	}
}

// 失败必须整体回滚、不打印成功、不做 Redis 清理，错误里不带底层细节；
// 身份校验没通过（不存在 / 不是管理员等）时绝不会执行 DELETE。
func TestExecuteAdmin2FAResetWithPasskeySwitchFailsClosedAndRollsBack(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	privateError := errors.New("password=PRIVATE database connection string PRIVATE")
	identityRows := func() *sqlmock.Rows { return sqlmock.NewRows([]string{"id"}).AddRow(int64(41)) }
	for _, tc := range []struct {
		name string
		want error
		mock func(sqlmock.Sqlmock)
	}{
		{name: "begin_failure", want: errAdmin2FAResetDatabase, mock: func(mock sqlmock.Sqlmock) { mock.ExpectBegin().WillReturnError(privateError) }},
		{name: "identity_not_matched_never_deletes", want: errAdmin2FAResetIdentity, mock: func(mock sqlmock.Sqlmock) {
			mock.ExpectBegin()
			mock.ExpectQuery(literalAdmin2FAResetSQL).WithArgs(int64(41)).WillReturnRows(sqlmock.NewRows([]string{"id"}))
			mock.ExpectRollback()
		}},
		{name: "reset_query_failure_never_deletes", want: errAdmin2FAResetDatabase, mock: func(mock sqlmock.Sqlmock) {
			mock.ExpectBegin()
			mock.ExpectQuery(literalAdmin2FAResetSQL).WithArgs(int64(41)).WillReturnError(privateError)
			mock.ExpectRollback()
		}},
		{name: "unexpected_identity_never_deletes", want: errAdmin2FAResetDatabase, mock: func(mock sqlmock.Sqlmock) {
			mock.ExpectBegin()
			mock.ExpectQuery(literalAdmin2FAResetSQL).WithArgs(int64(41)).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(42)))
			mock.ExpectRollback()
		}},
		{name: "delete_failure_rolls_back", want: errAdmin2FAResetDatabase, mock: func(mock sqlmock.Sqlmock) {
			mock.ExpectBegin()
			mock.ExpectQuery(literalAdmin2FAResetSQL).WithArgs(int64(41)).WillReturnRows(identityRows())
			mock.ExpectExec(literalClearPasskeysSQL).WithArgs(int64(41)).WillReturnError(privateError)
			mock.ExpectRollback()
		}},
		{name: "rows_affected_failure_rolls_back", want: errAdmin2FAResetDatabase, mock: func(mock sqlmock.Sqlmock) {
			mock.ExpectBegin()
			mock.ExpectQuery(literalAdmin2FAResetSQL).WithArgs(int64(41)).WillReturnRows(identityRows())
			mock.ExpectExec(literalClearPasskeysSQL).WithArgs(int64(41)).WillReturnResult(sqlmock.NewErrorResult(privateError))
			mock.ExpectRollback()
		}},
		{name: "commit_not_confirmed", want: errAdmin2FAResetCommit, mock: func(mock sqlmock.Sqlmock) {
			mock.ExpectBegin()
			mock.ExpectQuery(literalAdmin2FAResetSQL).WithArgs(int64(41)).WillReturnRows(identityRows())
			mock.ExpectExec(literalClearPasskeysSQL).WithArgs(int64(41)).WillReturnResult(sqlmock.NewResult(0, 2))
			mock.ExpectCommit().WillReturnError(privateError)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
			require.NoError(t, err)
			tc.mock(mock)
			mock.ExpectClose()
			cleanupCalls := 0
			var out bytes.Buffer
			err = executeAdmin2FAReset(context.Background(), 41, true, &out,
				func() (*config.Config, error) { return &config.Config{}, nil },
				func(*config.Config) (*sql.DB, error) { return db, nil },
				func(context.Context, *config.Config, int64) error { cleanupCalls++; return nil })
			require.ErrorIs(t, err, tc.want)
			require.NotContains(t, err.Error(), "PRIVATE")
			require.Empty(t, out.String(), "a failed recovery must not print a success message")
			require.Zero(t, cleanupCalls, "nothing may be revoked when the database change did not commit")
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestAdmin2FAResetInvalidIDNeverReachesDatabaseEvenWithPasskeySwitch(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	for _, id := range []int64{0, -1} {
		cleared, err := resetAdmin2FAInDatabase(context.Background(), db, id, true)
		require.ErrorIs(t, err, errAdmin2FAResetArguments)
		require.Zero(t, cleared)
		loadCalls := 0
		err = executeAdmin2FAReset(context.Background(), id, true, io.Discard,
			func() (*config.Config, error) { loadCalls++; return &config.Config{}, nil },
			func(*config.Config) (*sql.DB, error) { return db, nil },
			func(context.Context, *config.Config, int64) error { return nil })
		require.ErrorIs(t, err, errAdmin2FAResetArguments)
		require.Zero(t, loadCalls)
	}
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestExecuteAdmin2FAResetWithPasskeySwitchIsDisabledInReadOnlyCryptoTestMode(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "true")
	loadCalls, openCalls, cleanupCalls := 0, 0, 0
	err := executeAdmin2FAReset(context.Background(), 41, true, io.Discard,
		func() (*config.Config, error) { loadCalls++; return &config.Config{}, nil },
		func(*config.Config) (*sql.DB, error) { openCalls++; return nil, nil },
		func(context.Context, *config.Config, int64) error { cleanupCalls++; return nil })
	require.ErrorIs(t, err, errAdmin2FAResetReadOnly)
	require.Zero(t, loadCalls)
	require.Zero(t, openCalls)
	require.Zero(t, cleanupCalls)
}

// ---- 真实 SQL 语义（SQLite）：多把 Passkey、别的用户、回滚 ----

type passkeyRecoverySeed struct {
	id           int64
	role, status string
	deleted      bool
	passkeys     int
}

var passkeyRecoverySeeds = []passkeyRecoverySeed{
	{id: 41, role: "admin", status: "active", passkeys: 3},                // 目标：多把 Passkey
	{id: 42, role: "admin", status: "active", passkeys: 2},                // 旁观管理员
	{id: 43, role: "user", status: "active", passkeys: 1},                 // 普通用户
	{id: 44, role: "admin", status: "disabled", passkeys: 1},              // 已停用管理员
	{id: 45, role: "admin", status: "active", deleted: true, passkeys: 1}, // 已删除管理员
	{id: 46, role: "admin", status: "active"},                             // 没有任何 Passkey 的管理员
}

// 用命名的共享缓存内存库，不落磁盘：Windows 上回滚日志文件会让 TempDir 清理偶发失败
// （directory is not empty）。keeper 连接常驻到测试结束，执行体关闭自己的句柄后库仍在。
func seedPasskeyRecoveryDatabase(t *testing.T) string {
	t.Helper()
	dsn := "file:" + strings.NewReplacer("/", "_", " ", "_").Replace(t.Name()) + "?mode=memory&cache=shared"
	db, err := sql.Open("sqlite", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`CREATE TABLE users (id INTEGER PRIMARY KEY, role TEXT, status TEXT, email TEXT, password_hash TEXT, totp_enabled BOOLEAN, totp_secret_encrypted TEXT, totp_enabled_at TEXT, updated_at TEXT, deleted_at TEXT)`)
	require.NoError(t, err)
	_, err = db.Exec(`CREATE TABLE passkey_credentials (id INTEGER PRIMARY KEY AUTOINCREMENT, user_id INTEGER NOT NULL, credential_id BLOB NOT NULL UNIQUE, name TEXT NOT NULL, credential_data TEXT NOT NULL)`)
	require.NoError(t, err)
	for _, seed := range passkeyRecoverySeeds {
		var deletedAt any
		if seed.deleted {
			deletedAt = "2026-10-20"
		}
		_, err = db.Exec(`INSERT INTO users VALUES ($1, $2, $3, 'admin@example.com', 'unchanged-password-hash', TRUE, 'PRIVATE-SECRET', '2026-10-20', '2026-10-20', $4)`, seed.id, seed.role, seed.status, deletedAt)
		require.NoError(t, err)
		for index := 0; index < seed.passkeys; index++ {
			_, err = db.Exec(`INSERT INTO passkey_credentials (user_id, credential_id, name, credential_data) VALUES ($1, $2, 'PRIVATE-name', '{"secret":"PRIVATE-credential"}')`,
				seed.id, []byte(fmt.Sprintf("credential-%d-%d", seed.id, index)))
			require.NoError(t, err)
		}
	}
	return dsn
}

// 每个种子用户当前的 Passkey 条数与 TOTP 是否仍保留。
//
// 读连接开 read_uncommitted：共享缓存库里一旦有事务没结束（例如回滚被误删），
// 普通读连接会无限等它的表锁（sqlite3_unlock_notify，不受 ctx 控制）而把测试挂死；
// 允许脏读后，漏掉的回滚表现为断言失败（TOTP 已被重置 / Passkey 已被删），而不是超时。
func readPasskeyRecoveryState(t *testing.T, dsn string) (passkeys map[int64]int, totpKept map[int64]bool) {
	t.Helper()
	db, err := sql.Open("sqlite", dsn+"&_pragma=read_uncommitted(1)")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	passkeys, totpKept = map[int64]int{}, map[int64]bool{}
	for _, seed := range passkeyRecoverySeeds {
		var count int
		require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM passkey_credentials WHERE user_id = $1`, seed.id).Scan(&count))
		passkeys[seed.id] = count
		var enabled bool
		var secret, enabledAt sql.NullString
		require.NoError(t, db.QueryRow(`SELECT totp_enabled, totp_secret_encrypted, totp_enabled_at FROM users WHERE id = $1`, seed.id).Scan(&enabled, &secret, &enabledAt))
		require.Equal(t, enabled, secret.Valid, "TOTP flag and secret must stay consistent: user=%d", seed.id)
		require.Equal(t, enabled, enabledAt.Valid, "TOTP flag and timestamp must stay consistent: user=%d", seed.id)
		totpKept[seed.id] = enabled
	}
	return passkeys, totpKept
}

func runPasskeyRecovery(t *testing.T, dsn string, userID int64, clearPasskeys bool) (output string, cleanupCalls int, err error) {
	t.Helper()
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	var out bytes.Buffer
	err = executeAdmin2FAReset(context.Background(), userID, clearPasskeys, &out,
		func() (*config.Config, error) { return &config.Config{}, nil },
		func(*config.Config) (*sql.DB, error) { return sql.Open("sqlite", dsn) },
		func(context.Context, *config.Config, int64) error { cleanupCalls++; return nil })
	return out.String(), cleanupCalls, err
}

func TestAdmin2FAResetWithPasskeySwitchDeletesEveryPasskeyOfTheTargetOnly(t *testing.T) {
	dsn := seedPasskeyRecoveryDatabase(t)
	output, cleanupCalls, err := runPasskeyRecovery(t, dsn, 41, true)
	require.NoError(t, err)
	require.Equal(t, 1, cleanupCalls)
	passkeys, totpKept := readPasskeyRecoveryState(t, dsn)
	require.Equal(t, map[int64]int{41: 0, 42: 2, 43: 1, 44: 1, 45: 1, 46: 0}, passkeys, "all 3 credentials of the target go; every other user's stay")
	require.Equal(t, map[int64]bool{41: false, 42: true, 43: true, 44: true, 45: true, 46: true}, totpKept, "only the target's TOTP is reset")
	require.Equal(t, fmt.Sprintf(passkeyClearedOutputTemplate, 3), output)
	for _, secret := range []string{"PRIVATE", "credential-41", "secret"} {
		require.NotContains(t, output, secret, "the output may carry the count only, never credential content")
	}
}

func TestAdmin2FAResetWithPasskeySwitchOnAdministratorWithoutPasskeys(t *testing.T) {
	dsn := seedPasskeyRecoveryDatabase(t)
	output, _, err := runPasskeyRecovery(t, dsn, 46, true)
	require.NoError(t, err)
	passkeys, totpKept := readPasskeyRecoveryState(t, dsn)
	require.Equal(t, map[int64]int{41: 3, 42: 2, 43: 1, 44: 1, 45: 1, 46: 0}, passkeys)
	require.False(t, totpKept[46])
	require.True(t, totpKept[41])
	require.Equal(t, fmt.Sprintf(passkeyClearedOutputTemplate, 0), output)
}

func TestAdmin2FAResetWithoutSwitchLeavesEveryPasskeyInPlace(t *testing.T) {
	dsn := seedPasskeyRecoveryDatabase(t)
	output, cleanupCalls, err := runPasskeyRecovery(t, dsn, 41, false)
	require.NoError(t, err)
	require.Equal(t, 1, cleanupCalls)
	passkeys, totpKept := readPasskeyRecoveryState(t, dsn)
	require.Equal(t, map[int64]int{41: 3, 42: 2, 43: 1, 44: 1, 45: 1, 46: 0}, passkeys, "the default recovery must not touch passkey_credentials")
	require.Equal(t, map[int64]bool{41: false, 42: true, 43: true, 44: true, 45: true, 46: true}, totpKept)
	require.Equal(t, defaultResetOutputRevoked, output)
}

func TestAdmin2FAResetWithPasskeySwitchRefusesNonAdministratorsAndKeepsTheirPasskeys(t *testing.T) {
	for _, userID := range []int64{43, 44, 45, 999} {
		t.Run(fmt.Sprintf("user_%d", userID), func(t *testing.T) {
			dsn := seedPasskeyRecoveryDatabase(t)
			output, cleanupCalls, err := runPasskeyRecovery(t, dsn, userID, true)
			require.ErrorIs(t, err, errAdmin2FAResetIdentity)
			require.Empty(t, output)
			require.Zero(t, cleanupCalls)
			passkeys, totpKept := readPasskeyRecoveryState(t, dsn)
			require.Equal(t, map[int64]int{41: 3, 42: 2, 43: 1, 44: 1, 45: 1, 46: 0}, passkeys, "a refused target must not lose any credential")
			require.Equal(t, map[int64]bool{41: true, 42: true, 43: true, 44: true, 45: true, 46: true}, totpKept)
		})
	}
}

// 事务中途（TOTP 已重置、DELETE 失败）必须整体回滚：TOTP 仍然启用、Passkey 一条不少。
func TestAdmin2FAResetWithPasskeySwitchRollsBackTotpResetWhenDeleteFails(t *testing.T) {
	dsn := seedPasskeyRecoveryDatabase(t)
	db, err := sql.Open("sqlite", dsn)
	require.NoError(t, err)
	_, err = db.Exec(`CREATE TRIGGER passkey_delete_fails BEFORE DELETE ON passkey_credentials BEGIN SELECT RAISE(ABORT, 'PRIVATE trigger failure'); END`)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	output, cleanupCalls, err := runPasskeyRecovery(t, dsn, 41, true)
	require.ErrorIs(t, err, errAdmin2FAResetDatabase)
	require.NotContains(t, err.Error(), "PRIVATE")
	require.Empty(t, output)
	require.Zero(t, cleanupCalls)
	passkeys, totpKept := readPasskeyRecoveryState(t, dsn)
	require.Equal(t, map[int64]int{41: 3, 42: 2, 43: 1, 44: 1, 45: 1, 46: 0}, passkeys)
	require.Equal(t, map[int64]bool{41: true, 42: true, 43: true, 44: true, 45: true, 46: true}, totpKept, "the TOTP reset must be rolled back together with the failed delete")
}

// ---- 入口接线（真实 main）：开关已注册、值被原样传给 dispatch、-h 写明重新绑定 ----

func TestAdmin2FAClearPasskeysStartupWiring(t *testing.T) {
	if raw := os.Getenv("ADMIN_PASSKEY_WIRING_ARGS"); raw != "" {
		flag.CommandLine = flag.NewFlagSet("passkey-wiring", flag.ExitOnError)
		os.Args = append([]string{os.Args[0]}, strings.Fields(raw)...)
		main()
		os.Exit(0)
	}
	executable, err := os.Executable()
	require.NoError(t, err)
	const (
		readOnly = "read-only crypto test mode forbids administrator TOTP recovery"
		usage    = "administrator Passkey clearing requires only -reset-admin-2fa -user-id <positive ID> -confirm -clear-admin-passkeys"
	)
	for _, tc := range []struct {
		name, args string
		wantExit0  bool
		contains   []string
		absent     []string
	}{
		{name: "help_documents_switch_and_reenrollment", args: "-h", wantExit0: true,
			contains: []string{"-clear-admin-passkeys", admin2FAClearPasskeysUsage, "must enroll a factor again"}},
		// 开关值被真实传给 dispatch：校验通过后才走到只读模式的拒绝（此时没有任何数据库访问）。
		{name: "switch_reaches_the_recovery_command", args: "-reset-admin-2fa -user-id 1 -confirm -clear-admin-passkeys",
			contains: []string{readOnly}, absent: []string{"requires only", "not defined"}},
		// 显式 false 被拒绝：证明传给 dispatch 的就是这个开关本身，不是 -confirm 之类的别的值。
		{name: "explicit_false_is_an_argument_error", args: "-reset-admin-2fa -user-id 1 -confirm -clear-admin-passkeys=false",
			contains: []string{usage}, absent: []string{readOnly}},
		{name: "switch_alone_is_an_argument_error", args: "-clear-admin-passkeys",
			contains: []string{usage}, absent: []string{readOnly, "not defined"}},
		{name: "default_recovery_still_reaches_the_command", args: "-reset-admin-2fa -user-id 1 -confirm",
			contains: []string{readOnly}, absent: []string{"requires only"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(executable, "-test.run=^TestAdmin2FAClearPasskeysStartupWiring$")
			cmd.Env = append(os.Environ(), "ADMIN_PASSKEY_WIRING_ARGS="+tc.args,
				"CRYPTO_ADMIN_TEST_MODE=true", "SKIP_SETUP=false", "AUTO_SETUP=true", "DATA_DIR="+t.TempDir())
			output, err := cmd.CombinedOutput()
			if tc.wantExit0 {
				require.NoError(t, err, string(output))
			} else {
				require.Error(t, err, string(output))
			}
			for _, want := range tc.contains {
				require.Contains(t, string(output), want)
			}
			for _, unwanted := range tc.absent {
				require.NotContains(t, string(output), unwanted)
			}
		})
	}
}
