package handler

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"
)

// ensureRedeemCodeHashLedgerTable 在 SQLite（enttest）测试库里补出迁移 328 的
// 原生 DDL 台账表。
//
// redeem_code_hashes 与 api_key_hashes 同构，都是手写 DDL 而不是 ent 实体，
// 因此 enttest 自动迁移不会创建它；但卡密创建路径会与主表同事务双写这张表，
// 测试库缺表会让所有「创建邀请码 / 调整余额」用例直接失败。
//
// 与迁移里的列类型保持等价（SQLite 用 TEXT/INTEGER 表示 CHAR(64)/BIGINT）。
func ensureRedeemCodeHashLedgerTable(t *testing.T, db *sql.DB) {
	t.Helper()
	if db == nil {
		return
	}
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS redeem_code_hashes (
		redeem_code_id INTEGER PRIMARY KEY,
		code_hash      TEXT NOT NULL UNIQUE,
		code_prefix    TEXT NOT NULL,
		created_at     TIMESTAMP NOT NULL,
		updated_at     TIMESTAMP NOT NULL
	)`)
	require.NoError(t, err, "create redeem_code_hashes ledger table for sqlite test store")
}
