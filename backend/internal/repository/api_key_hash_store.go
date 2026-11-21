package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
)

// api_key_hashes 是迁移 327 新增的独立哈希表，与 api_keys.key_hash 保存同一份
// sha256 摘要（api_key_id 一对一）。它存在的意义：
//   - 明确「凭据本体只以哈希形式存在」的数据边界，明文列将来可整列删掉；
//   - 轮换/审计时只碰这一张表就能看到凭据轮换台账（created_at/updated_at）；
//   - 鉴权快路径仍然只查 api_keys.key_hash（单表部分唯一索引），不因新表多一次 JOIN。
//
// 因此两处必须同事务双写，否则会出现「快路径命中、台账里没有」或相反的漂移。
const upsertAPIKeyHashSQL = `
INSERT INTO api_key_hashes (api_key_id, key_hash, key_prefix, created_at, updated_at)
VALUES ($1, $2, $3, $4, $4)
ON CONFLICT (api_key_id) DO UPDATE
SET key_hash = EXCLUDED.key_hash,
    key_prefix = EXCLUDED.key_prefix,
    updated_at = EXCLUDED.updated_at`

const deleteAPIKeyHashSQL = `DELETE FROM api_key_hashes WHERE api_key_id = $1`

// upsertAPIKeyHash 写入/覆盖一把 key 的哈希台账。
func (r *apiKeyRepository) upsertAPIKeyHash(ctx context.Context, exec sqlExecutor, apiKeyID int64, keyHash, keyPrefix string) error {
	if exec == nil || apiKeyID <= 0 || keyHash == "" {
		return nil
	}
	now := time.Now()
	if _, err := exec.ExecContext(ctx, upsertAPIKeyHashSQL, apiKeyID, keyHash, keyPrefix, now); err != nil {
		return fmt.Errorf("upsert api key hash: %w", err)
	}
	return nil
}

// deleteAPIKeyHash 删除一把 key 的哈希台账（软删 / 物理删除时释放新表唯一约束）。
func (r *apiKeyRepository) deleteAPIKeyHash(ctx context.Context, exec sqlExecutor, apiKeyID int64) error {
	if exec == nil || apiKeyID <= 0 {
		return nil
	}
	if _, err := exec.ExecContext(ctx, deleteAPIKeyHashSQL, apiKeyID); err != nil {
		return fmt.Errorf("delete api key hash: %w", err)
	}
	return nil
}

// withTx 在调用方没有现成事务时开一个事务执行 fn；已在事务里时直接复用，
// 避免 ent 的嵌套事务报错，也让上层事务能整体回滚。两种「已在事务里」都要处理：
//   - ctx 里挂了 dbent.Tx（生产侧显式事务）；
//   - r.client 本身就是事务客户端（集成测试 testEntTx 就是这么构造的，
//     ent 会返回 ErrTxStarted）。
func (r *apiKeyRepository) withTx(ctx context.Context, fn func(exec *dbent.Client) error) error {
	if existing := dbent.TxFromContext(ctx); existing != nil {
		return fn(existing.Client())
	}
	tx, err := r.client.Tx(ctx)
	if errors.Is(err, dbent.ErrTxStarted) {
		return fn(r.client)
	}
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(tx.Client()); err != nil {
		return err
	}
	return tx.Commit()
}
