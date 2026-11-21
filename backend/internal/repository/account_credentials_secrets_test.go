package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/enttest"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
)

func newAccountCredentialsRepositoryTest(t *testing.T, vault service.SecretVault) (*accountRepository, *sql.DB, *dbent.Client) {
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
	repo := newAccountRepositoryWithSQL(client, db, nil)
	repo.credentialVault = vault
	return repo, db, client
}

func TestAccountCredentialsEncryptedAtRestAndPlaintextInService(t *testing.T) {
	vault := newSecretsTestVault(t, secretsTestKeyHex)
	repo, _, client := newAccountCredentialsRepositoryTest(t, vault)
	ctx := context.Background()

	account := &service.Account{
		Name:     "credentials-encryption",
		Platform: service.PlatformOpenAI,
		Type:     service.AccountTypeAPIKey,
		Status:   service.StatusActive,
		Credentials: map[string]any{
			"api_key":            "sk-live-upstream",
			"base_url":           "https://upstream.example.com",
			"refresh_token":      "refresh-live",
			"model_mapping":      map[string]any{"gpt-4o": "gpt-4o"},
			"chatgpt_account_id": "account-plain-id",
		},
	}
	require.NoError(t, createAccountRecord(ctx, client, account, vault))

	stored, err := client.Account.Get(ctx, account.ID)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(credentialString(t, stored.Credentials, "api_key"), "enc:v1:"))
	require.NotContains(t, credentialString(t, stored.Credentials, "api_key"), "sk-live-upstream")
	require.True(t, strings.HasPrefix(credentialString(t, stored.Credentials, "refresh_token"), "enc:v1:"))
	// Structural keys queried by SQL stay plaintext.
	require.Equal(t, "https://upstream.example.com", stored.Credentials["base_url"])
	require.Equal(t, "account-plain-id", stored.Credentials["chatgpt_account_id"])
	require.Equal(t, map[string]any{"gpt-4o": "gpt-4o"}, stored.Credentials["model_mapping"])
	// The presence check used by the OAuth refresh candidate query still holds.
	require.NotEmpty(t, strings.TrimSpace(credentialString(t, stored.Credentials, "refresh_token")))

	read, err := repo.GetByID(ctx, account.ID)
	require.NoError(t, err)
	require.Equal(t, "sk-live-upstream", read.Credentials["api_key"])
	require.Equal(t, "refresh-live", read.Credentials["refresh_token"])

	// The wire form used by compare-and-swap guards must equal the stored row.
	wire, err := repo.wireCredentialsJSON(read.Credentials)
	require.NoError(t, err)
	raw, err := jsonMarshalForTest(stored.Credentials)
	require.NoError(t, err)
	require.JSONEq(t, string(raw), string(wire))
}

func TestAccountCredentialsReadFailsClosedOnWrongKey(t *testing.T) {
	writer := newSecretsTestVault(t, secretsTestKeyHex)
	_, _, client := newAccountCredentialsRepositoryTest(t, writer)
	ctx := context.Background()
	account := &service.Account{
		Name:        "wrong-key",
		Platform:    service.PlatformOpenAI,
		Type:        service.AccountTypeAPIKey,
		Status:      service.StatusActive,
		Credentials: map[string]any{"api_key": "sk-secret-value"},
	}
	require.NoError(t, createAccountRecord(ctx, client, account, writer))

	reader, _, _ := newAccountCredentialsRepositoryTest(t, newSecretsTestVault(t, strings.Repeat("22", 32)))
	_, err := reader.GetByID(ctx, account.ID)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "sk-secret-value")

	noKey, _, _ := newAccountCredentialsRepositoryTest(t, service.NewSecretVault(nil))
	_, err = noKey.GetByID(ctx, account.ID)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "sk-secret-value")
}

func TestAccountCredentialsLazilyReencryptsLegacyPlaintext(t *testing.T) {
	vault := newSecretsTestVault(t, secretsTestKeyHex)
	repo, _, client := newAccountCredentialsRepositoryTest(t, vault)
	ctx := context.Background()

	legacy := &service.Account{
		Name:        "legacy-plain",
		Platform:    service.PlatformAnthropic,
		Type:        service.AccountTypeAPIKey,
		Status:      service.StatusActive,
		Credentials: map[string]any{"api_key": "legacy-plain-key"},
	}
	// Simulate a pre-encryption row by writing through a nil-vault repository.
	require.NoError(t, createAccountRecord(ctx, client, legacy, nil))
	stored, err := client.Account.Get(ctx, legacy.ID)
	require.NoError(t, err)
	require.Equal(t, "legacy-plain-key", stored.Credentials["api_key"])

	read, err := repo.GetByID(ctx, legacy.ID)
	require.NoError(t, err)
	require.Equal(t, "legacy-plain-key", read.Credentials["api_key"])

	require.Eventually(t, func() bool {
		current, getErr := client.Account.Get(context.Background(), legacy.ID)
		if getErr != nil {
			return false
		}
		value, ok := current.Credentials["api_key"].(string)
		return ok && strings.HasPrefix(value, "enc:v1:")
	}, 3*time.Second, 20*time.Millisecond, "legacy account credentials should be rewritten by the lazy migration")
}

func credentialString(t *testing.T, credentials map[string]any, key string) string {
	t.Helper()
	value, ok := credentials[key].(string)
	require.True(t, ok, "credential %q should be a string", key)
	return value
}

func jsonMarshalForTest(value any) ([]byte, error) {
	return json.Marshal(value)
}
