package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// 五分钟 2FA 凭证（two-factor grant）的 Redis 存储。
//
// key 布局（设计文档要求的命名）：
//   - auth:2fa_grant:<sha256(token) hex>     → 凭证绑定记录（TTL = 剩余有效期）
//   - auth:2fa_grant_user:<user_id>          → SET，成员为上面完整的 grant key，
//     供「密码修改 / 关闭 2FA / 账号禁用 / 强制下线」按用户一次性撤销。
//   - auth:2fa_grant_rate:<user_id>:<ip_hash> → 凭证校验的 60 秒窗口计数。
//
// Cookie 里的 token 明文不落任何地方（不落 Redis、不落日志、不落审计），
// 服务端只保存它的 SHA-256 摘要。
const (
	twoFactorGrantKeyPrefix     = "auth:2fa_grant:"
	twoFactorGrantUserKeyPrefix = "auth:2fa_grant_user:"
	twoFactorGrantRateKeyPrefix = "auth:2fa_grant_rate:"

	// 用户索引的 TTL 比凭证本身长，保证凭证还能用的整个窗口内索引都在；
	// 索引只是撤销用的辅助集合，长一点没有额外的敏感数据。
	twoFactorGrantUserIndexTTL = 10 * time.Minute
)

var _ service.TwoFactorGrantStore = (*TotpCache)(nil)

func twoFactorGrantKey(digest string) string {
	return twoFactorGrantKeyPrefix + digest
}

func twoFactorGrantUserKey(userID int64) string {
	return fmt.Sprintf("%s%d", twoFactorGrantUserKeyPrefix, userID)
}

func twoFactorGrantRateKey(userID int64, ipHash string) string {
	return fmt.Sprintf("%s%d:%s", twoFactorGrantRateKeyPrefix, userID, ipHash)
}

// SaveTwoFactorGrant 写入一条凭证记录，并把它加入用户维度索引。
// 写记录与写索引在同一个 Redis 事务里，避免出现「记录存在但撤销时看不到」。
func (c *TotpCache) SaveTwoFactorGrant(
	ctx context.Context,
	digest string,
	record *service.TwoFactorGrantRecord,
	ttl time.Duration,
) error {
	if c == nil || c.rdb == nil || digest == "" || record == nil || record.UserID <= 0 || ttl <= 0 {
		return fmt.Errorf("invalid two-factor grant record")
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal two-factor grant: %w", err)
	}
	grantKey := twoFactorGrantKey(digest)
	userKey := twoFactorGrantUserKey(record.UserID)

	pipe := c.rdb.TxPipeline()
	pipe.Set(ctx, grantKey, payload, ttl)
	pipe.SAdd(ctx, userKey, grantKey)
	indexTTL := twoFactorGrantUserIndexTTL
	if ttl > indexTTL {
		indexTTL = ttl
	}
	pipe.Expire(ctx, userKey, indexTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("store two-factor grant: %w", err)
	}
	return nil
}

// LoadTwoFactorGrant 读取绑定记录。key 不存在返回 (nil, nil)；损坏的记录按
// 错误处理，服务层会 fail-closed。
func (c *TotpCache) LoadTwoFactorGrant(ctx context.Context, digest string) (*service.TwoFactorGrantRecord, error) {
	if c == nil || c.rdb == nil || digest == "" {
		return nil, fmt.Errorf("invalid two-factor grant digest")
	}
	raw, err := c.rdb.Get(ctx, twoFactorGrantKey(digest)).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, fmt.Errorf("load two-factor grant: %w", err)
	}
	var record service.TwoFactorGrantRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return nil, fmt.Errorf("unmarshal two-factor grant: %w", err)
	}
	return &record, nil
}

// DeleteTwoFactorGrant 删除单条凭证（重签后用旧摘要时调用）。
func (c *TotpCache) DeleteTwoFactorGrant(ctx context.Context, digest string) error {
	if c == nil || c.rdb == nil || digest == "" {
		return fmt.Errorf("invalid two-factor grant digest")
	}
	grantKey := twoFactorGrantKey(digest)
	pipe := c.rdb.TxPipeline()
	pipe.Del(ctx, grantKey)
	// 用户索引成员用完整 key 存储；这里无法从摘要反推 user_id，索引里的
	// 陈旧成员会在集合过期时一起消失，不影响撤销正确性。
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("delete two-factor grant: %w", err)
	}
	return nil
}

// RevokeTwoFactorGrantsForUser 删除该用户全部凭证。只按用户索引集合删除，
// 不做 SCAN：热路径上禁止整库扫描。
func (c *TotpCache) RevokeTwoFactorGrantsForUser(ctx context.Context, userID int64) (int64, error) {
	if c == nil || c.rdb == nil || userID <= 0 {
		return 0, fmt.Errorf("invalid two-factor grant user")
	}
	userKey := twoFactorGrantUserKey(userID)
	keys, err := c.rdb.SMembers(ctx, userKey).Result()
	if err != nil {
		return 0, fmt.Errorf("list two-factor grants: %w", err)
	}
	var revoked int64
	if len(keys) > 0 {
		pipe := c.rdb.TxPipeline()
		// 分块删除，避免单条命令参数过大。
		const chunk = 128
		for start := 0; start < len(keys); start += chunk {
			end := start + chunk
			if end > len(keys) {
				end = len(keys)
			}
			del := pipe.Del(ctx, keys[start:end]...)
			_ = del
			revoked += int64(end - start)
		}
		if _, err := pipe.Exec(ctx); err != nil {
			return 0, fmt.Errorf("delete two-factor grants: %w", err)
		}
	}
	if err := c.rdb.Del(ctx, userKey).Err(); err != nil {
		return 0, fmt.Errorf("delete two-factor grant index: %w", err)
	}
	return revoked, nil
}

// IncrementTwoFactorGrantVerifyAttempt 返回 60 秒窗口内的凭证校验次数。
// 首次计数时设置过期时间；没有过期时间的残留 key 按异常处理返回 -1，
// 服务层据此 fail-closed。
func (c *TotpCache) IncrementTwoFactorGrantVerifyAttempt(
	ctx context.Context,
	userID int64,
	ipHash string,
	window time.Duration,
) (int64, error) {
	if c == nil || c.rdb == nil || userID <= 0 || ipHash == "" || window <= 0 {
		return 0, fmt.Errorf("invalid two-factor grant attempt counter")
	}
	count, err := cryptoTotpAttemptScript.Run(ctx, c.rdb, []string{twoFactorGrantRateKey(userID, ipHash)}, window.Milliseconds()).Int64()
	if err != nil {
		return 0, fmt.Errorf("increment two-factor grant verify attempts: %w", err)
	}
	if count < 0 {
		// 脚本对「没有过期时间」的异常 key 返回 -1：不能把它当成 0 次放行。
		return 0, fmt.Errorf("invalid two-factor grant attempt counter state")
	}
	return count, nil
}
