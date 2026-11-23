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

// API Key 三选一身份校验（创建 / 二次查看）的 Redis 存储。
//
// key 布局：
//   - apikey:reveal_grant:<sha256(token)>   → 二次查看授权记录（TTL 5 分钟）
//   - apikey:passkey_ticket:<sha256(token)> → Passkey 校验票据（TTL 5 分钟）
//
// 服务端只保存 token 的 SHA-256 摘要：浏览器拿到的是 token 明文，Redis 里没有
// token 原文，拖库也换不出有效 grant。两个 key 都是一次性消费（GETDEL）。
const (
	apiKeyRevealGrantKeyPrefix     = "apikey:reveal_grant:"
	apiKeyPasskeyTicketKeyPrefix   = "apikey:passkey_ticket:"
	apiKeyVerificationMaxTokenSize = 128
)

var _ service.APIKeyVerificationStore = (*TotpCache)(nil)

func apiKeyRevealGrantKey(digest string) string {
	return apiKeyRevealGrantKeyPrefix + digest
}

func apiKeyPasskeyTicketKey(digest string) string {
	return apiKeyPasskeyTicketKeyPrefix + digest
}

// SaveAPIKeyRevealGrant 写入一条二次查看授权记录（覆盖同摘要不可能出现，token 随机）。
func (c *TotpCache) SaveAPIKeyRevealGrant(
	ctx context.Context,
	digest string,
	record *service.APIKeyRevealGrantRecord,
	ttl time.Duration,
) error {
	if c == nil || c.rdb == nil || digest == "" || record == nil || ttl <= 0 {
		return fmt.Errorf("invalid api key reveal grant")
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal api key reveal grant: %w", err)
	}
	return c.rdb.Set(ctx, apiKeyRevealGrantKey(digest), payload, ttl).Err()
}

// ConsumeAPIKeyRevealGrant 原子读出并删除授权记录（一次性）。key 不存在返回 (nil, nil)。
func (c *TotpCache) ConsumeAPIKeyRevealGrant(ctx context.Context, digest string) (*service.APIKeyRevealGrantRecord, error) {
	if c == nil || c.rdb == nil || digest == "" {
		return nil, fmt.Errorf("invalid api key reveal grant digest")
	}
	raw, err := c.rdb.GetDel(ctx, apiKeyRevealGrantKey(digest)).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, fmt.Errorf("consume api key reveal grant: %w", err)
	}
	var record service.APIKeyRevealGrantRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return nil, fmt.Errorf("unmarshal api key reveal grant: %w", err)
	}
	return &record, nil
}

// SaveAPIKeyPasskeyTicket 写入 Passkey 校验成功后的 5 分钟一次性票据。
func (c *TotpCache) SaveAPIKeyPasskeyTicket(
	ctx context.Context,
	digest string,
	record *service.APIKeyPasskeyVerificationRecord,
	ttl time.Duration,
) error {
	if c == nil || c.rdb == nil || digest == "" || record == nil || ttl <= 0 {
		return fmt.Errorf("invalid api key passkey ticket")
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal api key passkey ticket: %w", err)
	}
	return c.rdb.Set(ctx, apiKeyPasskeyTicketKey(digest), payload, ttl).Err()
}

// ConsumeAPIKeyPasskeyTicket 原子读出并删除票据（一次性）。key 不存在返回 (nil, nil)。
func (c *TotpCache) ConsumeAPIKeyPasskeyTicket(ctx context.Context, digest string) (*service.APIKeyPasskeyVerificationRecord, error) {
	if c == nil || c.rdb == nil || digest == "" {
		return nil, fmt.Errorf("invalid api key passkey ticket digest")
	}
	raw, err := c.rdb.GetDel(ctx, apiKeyPasskeyTicketKey(digest)).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, fmt.Errorf("consume api key passkey ticket: %w", err)
	}
	var record service.APIKeyPasskeyVerificationRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return nil, fmt.Errorf("unmarshal api key passkey ticket: %w", err)
	}
	return &record, nil
}
