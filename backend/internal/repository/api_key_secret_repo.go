package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// api_key_secrets 是迁移 335 新增的表，只存「浏览器端用登录密码派生的密钥加密
// 后的密文」。服务端不存明文、不接收密码、也没有任何可解密材料——它只负责
// 密文的存取与生命周期（轮换 / 删除时立即丢弃）。
//
// 与 api_key_hashes 一样走原生 SQL 而不是 ent：本表不需要 ent 的关联与 Hook，
// 用到的 SQL 也很少；更要紧的是「轮换 / 删除时同事务删除密文」必须和 api_keys
// 的写路径共用同一个事务执行器，而不是绕开事务另起一条连接。
const (
	upsertAPIKeySecretSQL = `
INSERT INTO api_key_secrets (api_key_id, user_id, version, kdf, sealed, auth_factor, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), $7, $7)
ON CONFLICT (api_key_id) DO UPDATE
SET user_id = EXCLUDED.user_id,
    version = EXCLUDED.version,
    kdf = EXCLUDED.kdf,
    sealed = EXCLUDED.sealed,
    auth_factor = EXCLUDED.auth_factor,
    updated_at = EXCLUDED.updated_at`

	getAPIKeySecretSQL = `
SELECT version, kdf, sealed, COALESCE(auth_factor, ''), created_at, updated_at
FROM api_key_secrets
WHERE api_key_id = $1 AND user_id = $2`

	deleteAPIKeySecretSQL = `DELETE FROM api_key_secrets WHERE api_key_id = $1`

	deleteAPIKeySecretForUserSQL = `DELETE FROM api_key_secrets WHERE api_key_id = $1 AND user_id = $2`
)

type apiKeySecretRepository struct {
	db *sql.DB
}

// NewAPIKeySecretRepository 创建 api_key_secrets 仓储。
func NewAPIKeySecretRepository(sqlDB *sql.DB) service.APIKeySecretRepository {
	return &apiKeySecretRepository{db: sqlDB}
}

// Upsert 幂等写入密文：用户关闭再开启「后续可查看」、或改登录密码后重新封装，
// 都是覆盖同一行，不会在表里留下多份旧密文。
func (r *apiKeySecretRepository) Upsert(ctx context.Context, secret *service.APIKeySecret) error {
	if r == nil || r.db == nil || secret == nil || secret.APIKeyID <= 0 || secret.UserID <= 0 {
		return fmt.Errorf("upsert api key secret: invalid argument")
	}
	now := time.Now()
	if _, err := r.db.ExecContext(ctx, upsertAPIKeySecretSQL,
		secret.APIKeyID, secret.UserID, secret.Version, string(secret.KDF), secret.Sealed,
		service.NormalizeAPIKeyAuthFactor(secret.AuthFactor), now); err != nil {
		return fmt.Errorf("upsert api key secret: %w", err)
	}
	secret.CreatedAt = now
	secret.UpdatedAt = now
	return nil
}

// GetByAPIKeyID 读取密文。查询带 user_id 过滤：即使 api_key_id 被猜中，
// 也读不到别人的密文。没有记录时返回 API_KEY_SECRET_NOT_FOUND。
func (r *apiKeySecretRepository) GetByAPIKeyID(ctx context.Context, apiKeyID, userID int64) (*service.APIKeySecret, error) {
	if r == nil || r.db == nil {
		return nil, service.ErrAPIKeySecretNotFound
	}
	var (
		version    int
		kdf        []byte
		sealed     string
		authFactor string
		createdAt  time.Time
		updatedAt  time.Time
	)
	err := r.db.QueryRowContext(ctx, getAPIKeySecretSQL, apiKeyID, userID).
		Scan(&version, &kdf, &sealed, &authFactor, &createdAt, &updatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, service.ErrAPIKeySecretNotFound
		}
		return nil, fmt.Errorf("get api key secret: %w", err)
	}
	return &service.APIKeySecret{
		APIKeyID:   apiKeyID,
		UserID:     userID,
		Version:    version,
		KDF:        append(json.RawMessage(nil), kdf...),
		Sealed:     sealed,
		AuthFactor: authFactor,
		CreatedAt:  createdAt,
		UpdatedAt:  updatedAt,
	}, nil
}

// DeleteByAPIKeyID 删除自己那把 Key 的密文；不存在时视为已删除（幂等）。
func (r *apiKeySecretRepository) DeleteByAPIKeyID(ctx context.Context, apiKeyID, userID int64) error {
	if r == nil || r.db == nil {
		return nil
	}
	if _, err := r.db.ExecContext(ctx, deleteAPIKeySecretForUserSQL, apiKeyID, userID); err != nil {
		return fmt.Errorf("delete api key secret: %w", err)
	}
	return nil
}

// ListPresenceByAPIKeyIDs 返回这批 Key 中已封存密文的 ID → 授权因子。
// 只查 api_key_id 与 auth_factor 元数据，不把 sealed / kdf 带进列表响应路径。
func (r *apiKeySecretRepository) ListPresenceByAPIKeyIDs(ctx context.Context, userID int64, apiKeyIDs []int64) (map[int64]string, error) {
	out := make(map[int64]string, len(apiKeyIDs))
	if r == nil || r.db == nil || userID <= 0 || len(apiKeyIDs) == 0 {
		return out, nil
	}
	// ids 来自列表页，数量有上限（一页最多几百），用 IN 比 = ANY(array) 更省事，
	// 也不依赖 lib/pq 的数组编码，SQLite 单测可以直接跑。
	placeholders := make([]string, 0, len(apiKeyIDs))
	args := make([]any, 0, len(apiKeyIDs)+1)
	args = append(args, userID)
	for i, id := range apiKeyIDs {
		placeholders = append(placeholders, fmt.Sprintf("$%d", i+2))
		args = append(args, id)
	}
	query := `SELECT api_key_id, COALESCE(auth_factor, '') FROM api_key_secrets WHERE user_id = $1 AND api_key_id IN (` +
		strings.Join(placeholders, ", ") + `)`
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list api key secret presence: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var (
			id     int64
			factor string
		)
		if err := rows.Scan(&id, &factor); err != nil {
			return nil, fmt.Errorf("scan api key secret presence: %w", err)
		}
		out[id] = factor
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate api key secret presence: %w", err)
	}
	return out, nil
}

// deleteAPIKeySecretRow 删除一把 Key 的密文，供 api_key_repo.go 在
// 「轮换 / 软删除」的同一事务里调用。exec 可以是 dbent.Client 或 *sql.DB
// （两者都实现了 ExecContext），由调用方决定事务边界。
//
// 级联语义：密文只在「当前这版明文可解」时有意义；一旦轮换（旧 key 失效、
// 新 key 由前端重新封装）或删除，旧密文必须立刻消失，不能留在库里等着被拖。
func deleteAPIKeySecretRow(ctx context.Context, exec sqlExecutor, apiKeyID int64) error {
	if exec == nil || apiKeyID <= 0 {
		return nil
	}
	if _, err := exec.ExecContext(ctx, deleteAPIKeySecretSQL, apiKeyID); err != nil {
		return fmt.Errorf("delete api key secret: %w", err)
	}
	return nil
}
