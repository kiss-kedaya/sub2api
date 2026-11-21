package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Key 凭据哈希存储的结构迁移：api_keys 增加哈希/前缀列 + 部分唯一索引，
// 另建 api_key_hashes 独立表。key 列必须放开 NOT NULL（新行只写 NULL，
// 旧唯一约束对 NULL 不生效，因此明文回退查询仍走原索引）。
func TestAPIKeyHashStorageMigration(t *testing.T) {
	content, err := FS.ReadFile("327_api_key_hash_storage.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "ALTER TABLE api_keys ALTER COLUMN key DROP NOT NULL")
	require.Contains(t, sql, "ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS key_hash CHAR(64)")
	require.Contains(t, sql, "ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS key_prefix VARCHAR(16)")
	require.Contains(t, sql, "CREATE UNIQUE INDEX IF NOT EXISTS uq_api_keys_key_hash ON api_keys (key_hash) WHERE key_hash IS NOT NULL")
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS api_key_hashes")
	require.Contains(t, sql, "api_key_id BIGINT PRIMARY KEY REFERENCES api_keys(id) ON DELETE CASCADE")
	require.Contains(t, sql, "key_hash CHAR(64) NOT NULL UNIQUE")
	require.Contains(t, sql, "key_prefix VARCHAR(16) NOT NULL")
}

// 认证缓存的 durable invalidation 触发器必须能从 key_hash 取缓存键：
// 新行明文列为 NULL，继续用 OLD.key/NEW.key 会整体不入队（跨实例失效失效）。
func TestAPIKeyHashStorageMigrationKeepsAuthCacheTriggerHashAware(t *testing.T) {
	content, err := FS.ReadFile("327_api_key_hash_storage.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "CREATE OR REPLACE FUNCTION enqueue_api_key_auth_cache_invalidation_for(raw_hash TEXT, raw_key TEXT)")
	require.Contains(t, sql, "CREATE OR REPLACE FUNCTION enqueue_api_key_auth_cache_invalidation()")
	require.Contains(t, sql, "OLD.key_hash IS DISTINCT FROM NEW.key_hash")
	require.Contains(t, sql, "PERFORM enqueue_api_key_auth_cache_invalidation_for(OLD.key_hash, OLD.key)")
	require.Contains(t, sql, "PERFORM enqueue_api_key_auth_cache_invalidation_for(NEW.key_hash, NEW.key)")
	// 三处按用户/分组批量入队的触发器同批改成 COALESCE(key_hash, sha256(key))。
	for _, fn := range []string{
		"enqueue_user_auth_cache_invalidation",
		"enqueue_group_auth_cache_invalidation",
		"enqueue_allowed_group_auth_cache_invalidation",
	} {
		require.Contains(t, sql, "CREATE OR REPLACE FUNCTION "+fn+"()")
	}
	require.Contains(t, sql, "COALESCE(NULLIF(k.key_hash, ''), encode(sha256(convert_to(k.key, 'UTF8')), 'hex'))")
	require.Contains(t, sql, "AND (k.key_hash IS NOT NULL OR k.key <> '')")
}
