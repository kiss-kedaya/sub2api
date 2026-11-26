package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// user_security_preferences 是迁移 343 新增的小表：每个用户最多一行，目前只放
// 「创建 / 轮换 API Key 前要不要验证身份」这一项（'on' / 'off'）。没有行 = auto，
// 即旧行为（见 service.APIKeyCreateVerificationAuto）。
//
// 走原生 SQL 而不是 ent：只有四条语句、不需要 ent 的关联与 Hook，也不想为一张偏好表
// 再生成一份 ent schema。
//
// 写入一律是「带期望值的比较并交换」：服务层先读到旧值、据此判断这次修改是不是在放宽保护
// （放宽必须先验证身份），写入时再要求「库里现在还是我读到的那个值」。否则两个并发请求
// 交错时，一个按旧值判断过的请求可以把别人刚刚收紧的设置悄悄改回宽松。
const (
	getUserSecurityPreferenceSQL = `
SELECT api_key_create_verification
FROM user_security_preferences
WHERE user_id = $1`

	// 期望「没有行」：只有 user_id 还不存在时才插入成功。
	insertUserSecurityPreferenceSQL = `
INSERT INTO user_security_preferences (user_id, api_key_create_verification, updated_at)
VALUES ($1, $2, $3)
ON CONFLICT (user_id) DO NOTHING`

	// 期望「现在是 $2」：只有值仍然是 $2 才改成 $3。
	updateUserSecurityPreferenceSQL = `
UPDATE user_security_preferences
SET api_key_create_verification = $3, updated_at = $4
WHERE user_id = $1 AND api_key_create_verification = $2`

	// 恢复默认（auto）= 删行，同样要求值仍然是 $2。
	deleteUserSecurityPreferenceSQL = `
DELETE FROM user_security_preferences
WHERE user_id = $1 AND api_key_create_verification = $2`
)

type userSecurityPreferenceRepository struct {
	db *sql.DB
}

// NewUserSecurityPreferenceRepository 创建 user_security_preferences 仓储。
func NewUserSecurityPreferenceRepository(sqlDB *sql.DB) service.UserSecurityPreferenceRepository {
	return &userSecurityPreferenceRepository{db: sqlDB}
}

// GetAPIKeyCreateVerificationMode 返回用户显式设置的值（"on" / "off"）；没有行返回 ""。
func (r *userSecurityPreferenceRepository) GetAPIKeyCreateVerificationMode(ctx context.Context, userID int64) (string, error) {
	if r == nil || r.db == nil || userID <= 0 {
		return "", fmt.Errorf("get api key create verification mode: invalid argument")
	}
	var mode string
	err := r.db.QueryRowContext(ctx, getUserSecurityPreferenceSQL, userID).Scan(&mode)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get api key create verification mode: %w", err)
	}
	return mode, nil
}

// CompareAndSetAPIKeyCreateVerificationMode 仅当库里现在的值等于 expected 时把它改成 next，
// 返回是否真的写入。expected / next 取 "" / "on" / "off"，"" 表示没有行：
//
//	"" → "on"/"off"   插入（行已存在则失败）
//	"on"/"off" → 另一个值   更新（值已变则失败）
//	"on"/"off" → ""   删行（值已变则失败）
//	"" → ""           只在确实没有行时算成功
//
// 返回 (false, nil) 表示被并发修改抢先了，由调用方重新读取再判断，不是错误。
func (r *userSecurityPreferenceRepository) CompareAndSetAPIKeyCreateVerificationMode(
	ctx context.Context, userID int64, expected, next string,
) (bool, error) {
	if r == nil || r.db == nil || userID <= 0 || !validStoredAPIKeyCreateVerificationMode(expected) || !validStoredAPIKeyCreateVerificationMode(next) {
		return false, fmt.Errorf("compare and set api key create verification mode: invalid argument")
	}
	now := time.Now().UTC()
	var (
		res sql.Result
		err error
	)
	switch {
	case expected == "" && next == "":
		current, getErr := r.GetAPIKeyCreateVerificationMode(ctx, userID)
		return getErr == nil && current == "", getErr
	case expected == "":
		res, err = r.db.ExecContext(ctx, insertUserSecurityPreferenceSQL, userID, next, now)
	case next == "":
		res, err = r.db.ExecContext(ctx, deleteUserSecurityPreferenceSQL, userID, expected)
	default:
		res, err = r.db.ExecContext(ctx, updateUserSecurityPreferenceSQL, userID, expected, next, now)
	}
	if err != nil {
		return false, fmt.Errorf("compare and set api key create verification mode: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("compare and set api key create verification mode: %w", err)
	}
	return affected == 1, nil
}

// validStoredAPIKeyCreateVerificationMode 与迁移 343 的 CHECK 约束一致（再加上「没有行」的空串）。
func validStoredAPIKeyCreateVerificationMode(mode string) bool {
	switch mode {
	case "", service.APIKeyCreateVerificationOn, service.APIKeyCreateVerificationOff:
		return true
	default:
		return false
	}
}
