package repository

import (
	"database/sql/driver"
	"encoding/json"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type captureEncryptedArg struct {
	captured *string
}

func (m captureEncryptedArg) Match(value driver.Value) bool {
	text, ok := value.(string)
	if !ok {
		return false
	}
	*m.captured = text
	return true
}

func TestRunSecretsEncryptionBackfillRewritesLegacyRows(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	vault := newSecretsTestVault(t, secretsTestKeyHex)

	// Accounts: one legacy plaintext row, then the terminating empty batch.
	accountRows := sqlmock.NewRows([]string{"id", "credentials"}).
		AddRow(int64(1), []byte(`{"api_key":"legacy-key","base_url":"https://upstream.example.com"}`))
	mock.ExpectQuery(`SELECT id, credentials FROM accounts`).WithArgs(int64(0), secretsBackfillBatchSize).WillReturnRows(accountRows)
	var accountCipher string
	mock.ExpectExec(`UPDATE accounts SET credentials = \$2`).
		WithArgs(int64(1), captureEncryptedArg{captured: &accountCipher}, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	// Proxies.
	proxyRows := sqlmock.NewRows([]string{"id", "password"}).AddRow(int64(2), "legacy-proxy-password")
	mock.ExpectQuery(`SELECT id, password FROM proxies`).WithArgs(int64(0), secretsBackfillBatchSize).WillReturnRows(proxyRows)
	var proxyCipher string
	mock.ExpectExec(`UPDATE proxies SET password = \$2`).
		WithArgs(int64(2), captureEncryptedArg{captured: &proxyCipher}, "legacy-proxy-password").
		WillReturnResult(sqlmock.NewResult(0, 1))

	// Sensitive settings.
	settingRows := sqlmock.NewRows([]string{"id", "key", "value"}).
		AddRow(int64(3), service.SettingKeySMTPPassword, "legacy-smtp-password")
	mock.ExpectQuery(`SELECT id, key, value FROM settings`).
		WithArgs(int64(0), sqlmock.AnyArg(), secretsBackfillBatchSize).
		WillReturnRows(settingRows)
	var settingCipher string
	mock.ExpectExec(`UPDATE settings SET value = \$1`).
		WithArgs(captureEncryptedArg{captured: &settingCipher}, int64(3), "legacy-smtp-password").
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, RunSecretsEncryptionBackfill(t.Context(), db, vault))
	require.NoError(t, mock.ExpectationsWereMet())

	require.Contains(t, accountCipher, `"enc:v1:`, "account credentials must be rewritten as ciphertext")
	require.NotContains(t, accountCipher, "legacy-key")
	require.True(t, strings.HasPrefix(proxyCipher, "enc:v1:"))
	require.NotContains(t, proxyCipher, "legacy-proxy-password")
	require.True(t, strings.HasPrefix(settingCipher, "enc:v1:"))
	require.NotContains(t, settingCipher, "legacy-smtp-password")
}

func TestRunSecretsEncryptionBackfillSkipsWithoutKey(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	require.NoError(t, RunSecretsEncryptionBackfill(t.Context(), db, service.NewSecretVault(nil)))
	require.NoError(t, mock.ExpectationsWereMet(), "no queries should run without an installation key")
}

func TestRunSecretsEncryptionDecryptRestoresPlaintext(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	vault := newSecretsTestVault(t, secretsTestKeyHex)

	encryptedCredentials, err := service.EncryptCredentialsForStorage(vault, map[string]any{
		"api_key":  "sk-live",
		"base_url": "https://upstream.example.com",
	})
	require.NoError(t, err)
	accountRaw, err := json.Marshal(encryptedCredentials)
	require.NoError(t, err)

	encryptedProxy, err := service.EncryptProxyPassword(vault, "proxy-password")
	require.NoError(t, err)
	encryptedSetting, err := service.EncryptSettingValue(vault, service.SettingKeySMTPPassword, "smtp-password")
	require.NoError(t, err)

	mock.ExpectQuery(`SELECT id, credentials FROM accounts`).
		WithArgs(int64(0), secretsBackfillBatchSize).
		WillReturnRows(sqlmock.NewRows([]string{"id", "credentials"}).AddRow(int64(1), accountRaw))
	var accountPlain string
	mock.ExpectExec(`UPDATE accounts SET credentials = \$2`).
		WithArgs(int64(1), captureEncryptedArg{captured: &accountPlain}).
		WillReturnResult(sqlmock.NewResult(0, 1))

	mock.ExpectQuery(`SELECT id, password FROM proxies`).
		WithArgs(int64(0), secretsBackfillBatchSize).
		WillReturnRows(sqlmock.NewRows([]string{"id", "password"}).AddRow(int64(2), encryptedProxy))
	var proxyPlain string
	mock.ExpectExec(`UPDATE proxies SET password = \$2`).
		WithArgs(int64(2), captureEncryptedArg{captured: &proxyPlain}).
		WillReturnResult(sqlmock.NewResult(0, 1))

	mock.ExpectQuery(`SELECT id, key, value FROM settings`).
		WithArgs(int64(0), sqlmock.AnyArg(), secretsBackfillBatchSize).
		WillReturnRows(sqlmock.NewRows([]string{"id", "key", "value"}).AddRow(int64(3), service.SettingKeySMTPPassword, encryptedSetting))
	var settingPlain string
	mock.ExpectExec(`UPDATE settings SET value = \$1`).
		WithArgs(captureEncryptedArg{captured: &settingPlain}, int64(3)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, RunSecretsEncryptionDecrypt(t.Context(), db, vault))
	require.NoError(t, mock.ExpectationsWereMet())

	require.Contains(t, accountPlain, "sk-live")
	require.NotContains(t, accountPlain, "enc:v1:")
	require.Equal(t, "proxy-password", proxyPlain)
	require.Equal(t, "smtp-password", settingPlain)
}
