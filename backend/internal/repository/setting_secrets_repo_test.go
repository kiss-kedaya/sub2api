package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/enttest"
	"github.com/Wei-Shaw/sub2api/ent/setting"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	_ "modernc.org/sqlite"
)

func newSecretsTestVault(t *testing.T, keyHex string) service.SecretVault {
	t.Helper()
	return service.NewSecretVault(&config.Config{Totp: config.TotpConfig{EncryptionKey: keyHex}})
}

const secretsTestKeyHex = "8f3b1d2c4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f9"

func newSettingSecretsRepositoryTest(t *testing.T, vault service.SecretVault) (*settingRepository, *sql.DB, *dbent.Client) {
	t.Helper()
	name := strings.ReplaceAll(t.Name(), "/", "_")
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", name)
	db, err := sql.Open("sqlite", dsn)
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec("PRAGMA foreign_keys = ON")
	require.NoError(t, err)
	client := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(entsql.OpenDB(dialect.SQLite, db))))
	t.Cleanup(func() { _ = client.Close() })
	repo, ok := NewSettingRepository(client, vault).(*settingRepository)
	require.True(t, ok)
	return repo, db, client
}

func rawSettingValue(t *testing.T, client *dbent.Client, key string) string {
	t.Helper()
	m, err := client.Setting.Query().Where(setting.KeyEQ(key)).Only(context.Background())
	require.NoError(t, err)
	return m.Value
}

func TestSettingRepositoryEncryptsSensitiveRowsAtRest(t *testing.T) {
	vault := newSecretsTestVault(t, secretsTestKeyHex)
	repo, _, client := newSettingSecretsRepositoryTest(t, vault)
	ctx := context.Background()

	require.NoError(t, repo.Set(ctx, service.SettingKeySMTPPassword, "smtp-live-password"))
	raw := rawSettingValue(t, client, service.SettingKeySMTPPassword)
	require.True(t, strings.HasPrefix(raw, "enc:v1:"), "sensitive settings must be ciphertext at rest")
	require.NotContains(t, raw, "smtp-live-password")

	value, err := repo.GetValue(ctx, service.SettingKeySMTPPassword)
	require.NoError(t, err)
	require.Equal(t, "smtp-live-password", value)

	// Batch reads decrypt too.
	multiple, err := repo.GetMultiple(ctx, []string{service.SettingKeySMTPPassword})
	require.NoError(t, err)
	require.Equal(t, "smtp-live-password", multiple[service.SettingKeySMTPPassword])
	all, err := repo.GetAll(ctx)
	require.NoError(t, err)
	require.Equal(t, "smtp-live-password", all[service.SettingKeySMTPPassword])

	// Non-sensitive keys stay plaintext.
	require.NoError(t, repo.Set(ctx, service.SettingKeySMTPHost, "smtp.example.com"))
	require.Equal(t, "smtp.example.com", rawSettingValue(t, client, service.SettingKeySMTPHost))
}

func TestSettingRepositoryLazilyReencryptsLegacyPlaintext(t *testing.T) {
	vault := newSecretsTestVault(t, secretsTestKeyHex)
	repo, _, client := newSettingSecretsRepositoryTest(t, vault)
	ctx := context.Background()

	_, err := client.Setting.Create().SetKey(service.SettingKeySMTPPassword).SetValue("legacy-plain").Save(ctx)
	require.NoError(t, err)

	value, err := repo.GetValue(ctx, service.SettingKeySMTPPassword)
	require.NoError(t, err)
	require.Equal(t, "legacy-plain", value, "legacy plaintext must stay readable during the migration window")

	require.Eventually(t, func() bool {
		return strings.HasPrefix(rawSettingValue(t, client, service.SettingKeySMTPPassword), "enc:v1:")
	}, 3*time.Second, 20*time.Millisecond, "legacy plaintext should be rewritten by the lazy migration")
}

func TestSettingRepositoryFailsClosedOnWrongKey(t *testing.T) {
	writer := newSecretsTestVault(t, secretsTestKeyHex)
	repo, _, _ := newSettingSecretsRepositoryTest(t, writer)
	ctx := context.Background()
	require.NoError(t, repo.Set(ctx, service.SettingKeyTurnstileSecretKey, "turnstile-live-secret"))

	reader, _, _ := newSettingSecretsRepositoryTest(t, newSecretsTestVault(t, strings.Repeat("11", 32)))
	_, err := reader.GetValue(ctx, service.SettingKeyTurnstileSecretKey)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "turnstile-live-secret")

	// Missing key must also fail closed instead of returning an empty value.
	noKey, _, _ := newSettingSecretsRepositoryTest(t, service.NewSecretVault(nil))
	_, err = noKey.GetValue(ctx, service.SettingKeyTurnstileSecretKey)
	require.Error(t, err)
}
