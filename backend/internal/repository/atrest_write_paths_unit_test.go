package repository

import (
	"context"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/pkg/atrest"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
)

// 默认（无 env）：账号凭据写入走明文 SQL，并把 credentials_cipher 清成 NULL。
// 这是「上游凭据不再加密、代码层默认关闭」的核心写路径断言。
func TestUpdateCredentialsWritesPlaintextAndClearsCipherByDefault(t *testing.T) {
	atrest.Reset()
	t.Cleanup(atrest.Reset)

	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })

	mock.ExpectBegin()
	mock.ExpectExec(`(?s)UPDATE accounts.*credentials_cipher = NULL.*WHERE id = \$2 AND deleted_at IS NULL`).
		WithArgs(`{"api_key":"sk-new"}`, int64(27)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO scheduler_outbox")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	repo := newAccountRepositoryWithSQL(client, db, nil)
	err = repo.UpdateCredentials(context.Background(), 27, map[string]any{"api_key": "sk-new"})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// BulkUpdate 的凭据补丁同样默认只写明文，并清掉旧密文列。
func TestBulkUpdateCredentialsClearsCipherByDefault(t *testing.T) {
	atrest.Reset()
	t.Cleanup(atrest.Reset)

	exec := &recordingSQLExecutor{result: rowsAffectedResult(1)}
	repo := newAccountRepositoryWithSQL(nil, exec, nil)
	_, err := repo.BulkUpdate(context.Background(), []int64{27}, service.AccountBulkUpdate{
		Credentials: map[string]any{"api_key": "sk-new"},
	})
	require.NoError(t, err)
	require.NotEmpty(t, exec.execQueries)
	query := normalizeSQLWhitespace(exec.execQueries[0])
	require.Contains(t, query, "credentials_cipher = NULL",
		"sealing disabled must clear the stale cipher column")
	require.NotContains(t, query, "credentials_cipher = COALESCE",
		"sealing disabled must not refresh ciphertext")
	payload, ok := exec.execArgs[0][0].([]byte)
	require.True(t, ok)
	require.JSONEq(t, `{"api_key":"sk-new"}`, string(payload))
}

// 显式开启 sealing 后仍走原加密路径：明文列双写、封存投影去掉敏感值、
// 密文列是可解密的 enc:v2:。
func TestCredentialsStorageParamsKeepEncryptionWhenSealingEnabled(t *testing.T) {
	configureRepositoryAtRest(t)
	plain, sealed, cipher, err := credentialsStorageParams(map[string]any{"api_key": "sk-new", "base_url": "https://api.example.com"})
	require.NoError(t, err)
	require.Contains(t, plain, `"api_key":"sk-new"`, "unsealed row keeps plaintext for rollback")
	require.NotContains(t, sealed, "sk-new", "sealed projection drops sensitive values")
	ciphertext, ok := cipher.(string)
	require.True(t, ok)
	require.True(t, atrest.IsEncrypted(ciphertext), "sealing enabled must produce enc:v2: ciphertext")
	decrypted, err := atrest.DecryptString(ciphertext)
	require.NoError(t, err)
	require.JSONEq(t, plain, decrypted)
}
