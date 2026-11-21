package repository

import (
	"context"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
)

func newCredentialCASRepository(t *testing.T) (*accountRepository, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	repo := newAccountRepositoryWithSQL(client, db, nil)
	repo.credentialVault = newSecretsTestVault(t, secretsTestKeyHex)
	return repo, mock
}

// The Grok credential compare-and-swap guard compares the whole JSONB document.
// The snapshot is plaintext in memory, so the repository must encrypt it before
// binding the parameter; otherwise the CAS can never match the stored row.
func TestGrokCredentialCASParameterIsEncrypted(t *testing.T) {
	repo, mock := newCredentialCASRepository(t)
	var captured string
	mock.ExpectExec(`(?s)WITH updated AS .*UPDATE accounts AS a`).
		WithArgs(
			service.StatusError,
			"credential invalid",
			int64(41),
			service.PlatformGrok,
			service.AccountTypeOAuth,
			service.StatusActive,
			captureEncryptedArg{captured: &captured},
			service.SchedulerOutboxEventAccountChanged,
		).
		WillReturnResult(sqlmock.NewResult(0, 0))

	_, err := repo.SetGrokOAuthErrorIfCredentialsUnchanged(context.Background(), 41, map[string]any{
		"access_token":   "plain-access-token",
		"refresh_token":  "plain-refresh-token",
		"_token_version": 7,
	}, "credential invalid")
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())

	require.Contains(t, captured, `"enc:v1:`)
	require.NotContains(t, captured, "plain-access-token")
	require.NotContains(t, captured, "plain-refresh-token")
	require.Contains(t, captured, "_token_version")
}

func TestUpdateCredentialsParameterIsEncrypted(t *testing.T) {
	repo, mock := newCredentialCASRepository(t)
	var captured string
	mock.ExpectBegin()
	mock.ExpectExec(`(?s)UPDATE accounts`).
		WithArgs(captureEncryptedArg{captured: &captured}, int64(42)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`(?s)INSERT INTO scheduler_outbox`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, repo.UpdateCredentials(context.Background(), 42, map[string]any{
		"api_key":  "plain-api-key",
		"base_url": "https://upstream.example.com",
	}))
	require.NoError(t, mock.ExpectationsWereMet())

	require.True(t, strings.Contains(captured, `"enc:v1:`))
	require.NotContains(t, captured, "plain-api-key")
	require.Contains(t, captured, "https://upstream.example.com")
}
