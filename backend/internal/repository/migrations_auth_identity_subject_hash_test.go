package repository

import (
	"regexp"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

// 迁移 328 只做结构变更：加列 + 加索引 + 放宽 NOT NULL。
// 回填/清空/回滚都在 docs/ops/auth-identity-subject-hash-20261121.md 里人工执行，
// 迁移本身不能夹带 UPDATE（大表长事务），也必须可以重复执行。
func TestAuthIdentitySubjectHashMigrationIsAdditiveAndIdempotent(t *testing.T) {
	const filename = "329_auth_identity_subject_hash.sql"
	raw, err := migrations.FS.ReadFile(filename)
	require.NoError(t, err)
	statement := string(raw)

	require.Contains(t, statement, "ALTER TABLE auth_identities ALTER COLUMN provider_subject DROP NOT NULL")
	require.Contains(t, statement, "ADD COLUMN IF NOT EXISTS provider_subject_hash CHAR(64)")
	require.Contains(t, statement, "ADD COLUMN IF NOT EXISTS provider_subject_prefix VARCHAR(16)")
	require.Contains(t, statement, "CREATE UNIQUE INDEX IF NOT EXISTS auth_identities_provider_subject_hash_key")
	require.Contains(t, statement, "ON auth_identities (provider_type, provider_key, provider_subject_hash)")
	require.Contains(t, statement, "CREATE INDEX IF NOT EXISTS auth_identities_provider_subject_hash_lookup_idx")
	require.Contains(t, statement, "ON auth_identities (provider_type, provider_subject_hash)")

	require.Contains(t, statement, "ALTER TABLE auth_identity_channels ALTER COLUMN channel_subject DROP NOT NULL")
	require.Contains(t, statement, "ADD COLUMN IF NOT EXISTS channel_subject_hash CHAR(64)")
	require.Contains(t, statement, "ADD COLUMN IF NOT EXISTS channel_subject_prefix VARCHAR(16)")
	require.Contains(t, statement, "CREATE UNIQUE INDEX IF NOT EXISTS auth_identity_channels_channel_subject_hash_key")
	require.Contains(t, statement, "CREATE INDEX IF NOT EXISTS auth_identity_channels_channel_subject_hash_lookup_idx")

	require.Contains(t, statement, "ALTER TABLE pending_auth_sessions ALTER COLUMN provider_subject DROP NOT NULL")
	require.Contains(t, statement, "ADD COLUMN IF NOT EXISTS provider_subject_hash CHAR(64)")
	require.Contains(t, statement, "ADD COLUMN IF NOT EXISTS provider_subject_prefix VARCHAR(16)")
	require.Contains(t, statement, "CREATE INDEX IF NOT EXISTS pending_auth_sessions_provider_subject_hash_idx")

	// 回填/清洗只允许在 docs/ops 的人工 SQL 里，迁移本身不能夹带数据变更。
	require.NotContains(t, statement, "UPDATE auth_identities")
	require.NotContains(t, statement, "UPDATE auth_identity_channels")
	require.NotContains(t, statement, "UPDATE pending_auth_sessions")
	require.NotContains(t, statement, "\nSELECT encode(")

	// 结构迁移不允许顺手回填 / 清空存量数据。
	require.NotContains(t, statement, "UPDATE auth_identities")
	require.NotContains(t, statement, "DELETE FROM auth_identities")
	require.NotContains(t, statement, "\nSET provider_subject = NULL")

	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectExec(regexp.QuoteMeta(statement)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta(statement)).WillReturnResult(sqlmock.NewResult(0, 0))
	_, err = db.Exec(statement)
	require.NoError(t, err)
	_, err = db.Exec(statement)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}
