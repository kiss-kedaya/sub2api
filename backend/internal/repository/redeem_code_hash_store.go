package repository

import (
	"context"
	"fmt"
	"time"

	"entgo.io/ent/dialect"
	"github.com/lib/pq"
)

// redeem_code_hashes 是迁移 328 新增的独立哈希台账表，与 redeem_codes.code_hash
// 保存同一份 sha256 摘要（redeem_code_id 一对一）。与 api_key_hashes 同构，
// 存在的意义也一样：
//   - 明确「卡密本体只以哈希形式存在」的数据边界，明文列将来可整列删掉；
//   - 审计/核对时只碰这一张表就能证明「没有任何一行还留着明文」；
//   - 兑换快路径仍然只查 redeem_codes.code_hash（单表部分唯一索引），不因新表多一次 JOIN。
//
// 因此两处必须同事务双写，否则会出现「快路径命中、台账里没有」或相反的漂移。
// 时间戳由 Go 传入（而不是 SQL 里的 NOW()）：单测跑 SQLite，没有 NOW() 函数，
// 方言中立能让双写路径在两种驱动上都被真实覆盖。
const upsertRedeemCodeHashSQL = `
INSERT INTO redeem_code_hashes (redeem_code_id, code_hash, code_prefix, created_at, updated_at)
VALUES ($1, $2, $3, $4, $4)
ON CONFLICT (redeem_code_id) DO UPDATE
SET code_hash = EXCLUDED.code_hash,
    code_prefix = EXCLUDED.code_prefix,
    updated_at = EXCLUDED.updated_at`

// upsertRedeemCodeHash 写入/覆盖一张卡的哈希台账。
//
// 只接受已补齐哈希的内存对象；调用方（Create / CreateBatch）必须先 EnsureCodeHash。
func (r *redeemCodeRepository) upsertRedeemCodeHash(ctx context.Context, codeID int64, codeHash, codePrefix string) error {
	if codeID <= 0 || codeHash == "" {
		return nil
	}
	client := clientFromContext(ctx, r.client)
	now := time.Now()
	if _, err := client.ExecContext(ctx, upsertRedeemCodeHashSQL, codeID, codeHash, codePrefix, now); err != nil {
		return fmt.Errorf("upsert redeem code hash: %w", err)
	}
	return nil
}

// upsertRedeemCodeHashesBatch 是批量版本：生成接口一次最多 1000 行，逐行 Exec
// 会把往返放大 1000 倍，因此用单条语句 + unnest 双写台账。
//
// 前提：codes 已按插入顺序拿到 ID，且每行都已 EnsureCodeHash。
func (r *redeemCodeRepository) upsertRedeemCodeHashesBatch(ctx context.Context, codes []dbentRedeemCodeIdentity) error {
	if len(codes) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(codes))
	hashes := make([]string, 0, len(codes))
	prefixes := make([]string, 0, len(codes))
	for _, c := range codes {
		if c.ID <= 0 || c.Hash == "" {
			continue
		}
		ids = append(ids, c.ID)
		hashes = append(hashes, c.Hash)
		prefixes = append(prefixes, c.Prefix)
	}
	if len(ids) == 0 {
		return nil
	}
	client := clientFromContext(ctx, r.client)
	// unnest + 类型转换是 PostgreSQL 语法；SQLite（单测 / enttest）退化为逐行 upsert，
	// 生产批量生成只有 PG 一条路径。
	if client.Driver() == nil || client.Driver().Dialect() != dialect.Postgres {
		for i := range ids {
			if err := r.upsertRedeemCodeHash(ctx, ids[i], hashes[i], prefixes[i]); err != nil {
				return err
			}
		}
		return nil
	}
	now := time.Now()
	// lib/pq 对切片参数必须包 pq.Array；列类型转换放在 SQL 里做（text[] → char(64)/varchar(16)），
	// 避免驱动端再猜一遍数组元素类型。
	if _, err := client.ExecContext(ctx, `
INSERT INTO redeem_code_hashes (redeem_code_id, code_hash, code_prefix, created_at, updated_at)
SELECT id, hash::char(64), prefix::varchar(16), $4, $4
FROM unnest($1::bigint[], $2::text[], $3::text[]) AS t(id, hash, prefix)
ON CONFLICT (redeem_code_id) DO UPDATE
SET code_hash = EXCLUDED.code_hash,
    code_prefix = EXCLUDED.code_prefix,
    updated_at = EXCLUDED.updated_at`, pq.Array(ids), pq.Array(hashes), pq.Array(prefixes), now); err != nil {
		return fmt.Errorf("upsert redeem code hashes batch: %w", err)
	}
	return nil
}

// dbentRedeemCodeIdentity 是双写台账需要的最小字段集合。
type dbentRedeemCodeIdentity struct {
	ID     int64
	Hash   string
	Prefix string
}
