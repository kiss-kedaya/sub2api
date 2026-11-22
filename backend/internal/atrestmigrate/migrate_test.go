package atrestmigrate

import (
	"context"
	"database/sql"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/pkg/atrest"
	"github.com/stretchr/testify/require"
)

const migrateTestKeyHex = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"

func configureAtRestForTest(t *testing.T) {
	t.Helper()
	cipher, err := atrest.New(migrateTestKeyHex)
	require.NoError(t, err)
	atrest.Configure(cipher)
	t.Cleanup(atrest.Reset)
}

func newMigrateTestDB(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db, mock
}

func TestParseTables(t *testing.T) {
	tables, err := ParseTables("accounts, proxies,settings,accounts")
	require.NoError(t, err)
	require.Equal(t, []string{TableAccounts, TableProxies, TableSettings}, tables)

	_, err = ParseTables("accounts,users")
	require.Error(t, err)
	_, err = ParseTables(" , ")
	require.Error(t, err)
}

func TestRunRefusesMigrateWithoutKey(t *testing.T) {
	atrest.Reset()
	t.Cleanup(atrest.Reset)
	db, _ := newMigrateTestDB(t)
	_, err := Run(context.Background(), db, Options{Tables: []string{TableAccounts}, BatchSize: 10})
	require.Error(t, err)
	require.Contains(t, err.Error(), atrest.KeyEnv)
	require.Contains(t, err.Error(), atrest.EnabledEnv)
}

// 总开关默认关闭：仅配置有效密钥也必须拒绝写库，否则「把密钥加回环境」
// 又会自动加密（2026-11-22 事故根因）。
func TestRunRefusesMigrateWhenSealingDisabledEvenWithKey(t *testing.T) {
	cipher, err := atrest.New(migrateTestKeyHex)
	require.NoError(t, err)
	atrest.Configure(cipher)
	atrest.SetSealingEnabled(false)
	t.Cleanup(atrest.Reset)
	db, _ := newMigrateTestDB(t)
	_, err = Run(context.Background(), db, Options{Tables: []string{TableAccounts}, BatchSize: 10})
	require.Error(t, err)
	require.Contains(t, err.Error(), atrest.EnabledEnv)
}

func TestDryRunStatsWithoutWrites(t *testing.T) {
	configureAtRestForTest(t)
	db, mock := newMigrateTestDB(t)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, credentials, COALESCE(credentials_cipher, '')")).
		WithArgs(int64(0), 200).
		WillReturnRows(sqlmock.NewRows([]string{"id", "credentials", "credentials_cipher"}).
			AddRow(int64(1), []byte(`{"api_key":"sk-1"}`), "").
			AddRow(int64(2), []byte(`{"access_token":"at"}`), ""))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, credentials, COALESCE(credentials_cipher, '')")).
		WithArgs(int64(2), 200).
		WillReturnRows(sqlmock.NewRows([]string{"id", "credentials", "credentials_cipher"}))
	result, err := Run(context.Background(), db, Options{
		Tables:    []string{TableAccounts},
		BatchSize: 200,
		DryRun:    true,
		BatchGap:  time.Millisecond,
	})
	require.NoError(t, err)
	require.Len(t, result.Tables, 1)
	require.Equal(t, int64(2), result.Tables[0].Scanned)
	require.Equal(t, int64(2), result.Tables[0].RemainingPlaintext)
	require.Zero(t, result.Tables[0].Migrated)
	require.NoError(t, mock.ExpectationsWereMet(), "dry-run must not write")
}

func TestMigrateAccountsSealsAndIsIdempotent(t *testing.T) {
	configureAtRestForTest(t)
	db, mock := newMigrateTestDB(t)
	plain := `{"api_key":"sk-live","base_url":"https://api.example.com"}`
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, credentials, COALESCE(credentials_cipher, '')")).
		WithArgs(int64(0), 200).
		WillReturnRows(sqlmock.NewRows([]string{"id", "credentials", "credentials_cipher"}).
			AddRow(int64(7), []byte(plain), ""))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE accounts")).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), int64(7), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, credentials, COALESCE(credentials_cipher, '')")).
		WithArgs(int64(7), 200).
		WillReturnRows(sqlmock.NewRows([]string{"id", "credentials", "credentials_cipher"}))

	result, err := Run(context.Background(), db, Options{Tables: []string{TableAccounts}, BatchSize: 200, BatchGap: time.Millisecond})
	require.NoError(t, err)
	require.Zero(t, result.Tables[0].RemainingPlaintext)
	require.Equal(t, int64(1), result.Tables[0].Migrated)
	require.Equal(t, int64(1), result.Tables[0].Sealed)
	require.NoError(t, mock.ExpectationsWereMet())

	// 幂等：已封存 + 已有密文的行不再写库。
	sealedJSON, err := marshalSealed(map[string]any{"api_key": "sk-live", "base_url": "https://api.example.com"})
	require.NoError(t, err)
	ciphertext, err := atrest.EncryptString(plain)
	require.NoError(t, err)

	db2, mock2 := newMigrateTestDB(t)
	mock2.ExpectQuery(regexp.QuoteMeta("SELECT id, credentials, COALESCE(credentials_cipher, '')")).
		WithArgs(int64(0), 200).
		WillReturnRows(sqlmock.NewRows([]string{"id", "credentials", "credentials_cipher"}).
			AddRow(int64(7), []byte(sealedJSON), ciphertext))
	mock2.ExpectQuery(regexp.QuoteMeta("SELECT id, credentials, COALESCE(credentials_cipher, '')")).
		WithArgs(int64(7), 200).
		WillReturnRows(sqlmock.NewRows([]string{"id", "credentials", "credentials_cipher"}))
	second, err := Run(context.Background(), db2, Options{Tables: []string{TableAccounts}, BatchSize: 200, BatchGap: time.Millisecond})
	require.NoError(t, err)
	require.Zero(t, second.Tables[0].Migrated)
	require.Equal(t, int64(1), second.Tables[0].Already)
	require.Zero(t, second.Tables[0].RemainingPlaintext)
	require.NoError(t, mock2.ExpectationsWereMet())
}

func TestMigrateAccountsKeepsSealedProjection(t *testing.T) {
	configureAtRestForTest(t)
	sealed, err := marshalSealed(map[string]any{"refresh_token": "rt", "_token_version": float64(3)})
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal([]byte(sealed), &decoded))
	require.Equal(t, true, decoded["_sealed"])
	require.NotContains(t, decoded, "refresh_token")
	require.Equal(t, []any{"refresh_token"}, decoded["_sealed_keys"])
}

func TestMigrateProxiesSealsPlaintextAndSkipsManagedCipher(t *testing.T) {
	configureAtRestForTest(t)
	db, mock := newMigrateTestDB(t)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, COALESCE(password, ''), COALESCE(password_cipher, '')")).
		WithArgs(int64(0), 200).
		WillReturnRows(sqlmock.NewRows([]string{"id", "password", "password_cipher"}).
			AddRow(int64(1), "plain-proxy-pass", "").
			AddRow(int64(2), "enc:managed:v1:abc", "").
			AddRow(int64(3), "", ""))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE proxies")).
		WithArgs(sqlmock.AnyArg(), int64(1), "plain-proxy-pass").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, COALESCE(password, ''), COALESCE(password_cipher, '')")).
		WithArgs(int64(3), 200).
		WillReturnRows(sqlmock.NewRows([]string{"id", "password", "password_cipher"}))

	result, err := Run(context.Background(), db, Options{Tables: []string{TableProxies}, BatchSize: 200, BatchGap: time.Millisecond})
	require.NoError(t, err)
	require.Equal(t, int64(1), result.Tables[0].Migrated)
	require.Zero(t, result.Tables[0].RemainingPlaintext)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMigrateSettingsSealsWhitelistOnly(t *testing.T) {
	configureAtRestForTest(t)
	db, mock := newMigrateTestDB(t)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT key, value")).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value"}).
			AddRow("smtp_password", "smtp-secret").
			AddRow("github_oauth_client_secret", "gh-secret").
			AddRow("some_toggle", "true"))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE settings")).
		WithArgs(sqlmock.AnyArg(), "smtp_password", "smtp-secret").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE settings")).
		WithArgs(sqlmock.AnyArg(), "github_oauth_client_secret", "gh-secret").
		WillReturnResult(sqlmock.NewResult(0, 1))

	result, err := Run(context.Background(), db, Options{Tables: []string{TableSettings}, BatchSize: 200, BatchGap: time.Millisecond})
	require.NoError(t, err)
	require.Equal(t, int64(2), result.Tables[0].Migrated)
	require.Zero(t, result.Tables[0].RemainingPlaintext)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestVerifyReportsBrokenCiphertextAndRemainingPlaintext(t *testing.T) {
	configureAtRestForTest(t)
	db, mock := newMigrateTestDB(t)
	validCipher, err := atrest.EncryptString(`{"api_key":"sk-x"}`)
	require.NoError(t, err)
	sealedJSON, err := marshalSealed(map[string]any{"api_key": "sk-x"})
	require.NoError(t, err)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, credentials, COALESCE(credentials_cipher, '')")).
		WithArgs(int64(0), 200).
		WillReturnRows(sqlmock.NewRows([]string{"id", "credentials", "credentials_cipher"}).
			AddRow(int64(1), []byte(sealedJSON), validCipher).
			AddRow(int64(2), []byte(`{"api_key":"still-plain"}`), "").
			AddRow(int64(3), []byte(`{"_sealed":true}`), "").
			AddRow(int64(4), []byte(sealedJSON), "enc:v2:not-a-cipher"))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, credentials, COALESCE(credentials_cipher, '')")).
		WithArgs(int64(4), 200).
		WillReturnRows(sqlmock.NewRows([]string{"id", "credentials", "credentials_cipher"}))

	result, err := Run(context.Background(), db, Options{Tables: []string{TableAccounts}, BatchSize: 200, Verify: true, BatchGap: time.Millisecond})
	require.NoError(t, err)
	table := result.Tables[0]
	require.Equal(t, int64(4), table.Scanned)
	require.Equal(t, int64(2), table.RemainingPlaintext, "one plaintext row + one sealed row with plaintext column still present")
	require.Equal(t, int64(2), table.Broken, "unreadable ciphertext and sealed-without-cipher are broken")
	require.NoError(t, mock.ExpectationsWereMet(), "verify must be read-only")
}

func TestSealedProjectionIsDeterministic(t *testing.T) {
	credentials := map[string]any{
		"api_key":              "sk-abc",
		"access_token":         "at-abc",
		"refresh_token":        "rt-abc",
		"base_url":             "https://api.example.com",
		"model_mapping":        map[string]any{"gpt": "gpt-4"},
		"custom_usage_secrets": map[string]any{"api_key": "nested"},
	}
	sealed, err := marshalSealed(credentials)
	require.NoError(t, err)
	require.True(t, strings.Contains(sealed, `"_sealed":true`))
	require.True(t, strings.Contains(sealed, `"base_url":"https://api.example.com"`))
	require.True(t, strings.Contains(sealed, `"_id_api_key"`))
	require.True(t, strings.Contains(sealed, `"_id_access_token"`))
	require.NotContains(t, sealed, "sk-abc")
	require.NotContains(t, sealed, "at-abc")
	require.NotContains(t, sealed, "nested")

	// 可重复：封存投影必须是确定性的，CAS 依赖这一点。
	again, err := marshalSealed(credentials)
	require.NoError(t, err)
	require.Equal(t, sealed, again)
}
