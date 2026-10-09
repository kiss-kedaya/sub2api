package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

// 在途请求展示，和余额预留不是同一组 key。
//
//	usage:inflight:{uid}           HASH  field=requestID value=JSON
//	usage:inflight:users           SET   有在途请求的 userID，管理员列表只读这个集合
//	usage:inflight:done:{uid}:{id} STRING 结束墓碑，挡住迟到的刷新把行写回来
//
// ponytail: 管理员一次最多展开 500 个用户。超过再改成按用户分页，不要 SCAN。
const (
	usageInflightUsersKey = "usage:inflight:users"
	usageInflightTTL      = 20 * time.Minute
	usageInflightDoneTTL  = 2 * time.Minute
	usageInflightUserCap  = 500
)

type usageInflightCache struct {
	rdb *redis.Client
}

func NewUsageInflightCache(rdb *redis.Client) service.UsageInflightStore {
	return &usageInflightCache{rdb: rdb}
}

func usageInflightHashKey(userID int64) string {
	return fmt.Sprintf("usage:inflight:{%d}", userID)
}

func usageInflightDoneKey(userID int64, requestID string) string {
	return fmt.Sprintf("usage:inflight:done:{%d}:%s", userID, requestID)
}

func (c *usageInflightCache) Save(ctx context.Context, snap service.UsageInflightSnapshot) error {
	if c == nil || c.rdb == nil || snap.UserID <= 0 || snap.RequestID == "" {
		return nil
	}
	done, err := c.rdb.Exists(ctx, usageInflightDoneKey(snap.UserID, snap.RequestID)).Result()
	if err != nil || done > 0 {
		return err
	}
	snap.ExpiresAtMs = time.Now().Add(usageInflightTTL).UnixMilli()
	payload, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	hashKey := usageInflightHashKey(snap.UserID)
	if err := c.rdb.HSet(ctx, hashKey, snap.RequestID, payload).Err(); err != nil {
		return err
	}
	_ = c.rdb.PExpire(ctx, hashKey, usageInflightTTL).Err()
	return c.rdb.SAdd(ctx, usageInflightUsersKey, strconv.FormatInt(snap.UserID, 10)).Err()
}

func (c *usageInflightCache) Delete(ctx context.Context, userID int64, requestID string) error {
	if c == nil || c.rdb == nil || userID <= 0 || requestID == "" {
		return nil
	}
	_ = c.rdb.Set(ctx, usageInflightDoneKey(userID, requestID), "1", usageInflightDoneTTL).Err()
	hashKey := usageInflightHashKey(userID)
	if err := c.rdb.HDel(ctx, hashKey, requestID).Err(); err != nil {
		return err
	}
	left, err := c.rdb.HLen(ctx, hashKey).Result()
	if err != nil || left > 0 {
		return err
	}
	return c.rdb.SRem(ctx, usageInflightUsersKey, strconv.FormatInt(userID, 10)).Err()
}

func (c *usageInflightCache) ListUser(ctx context.Context, userID int64) ([]service.UsageInflightSnapshot, error) {
	if c == nil || c.rdb == nil || userID <= 0 {
		return nil, nil
	}
	hashKey := usageInflightHashKey(userID)
	raw, err := c.rdb.HGetAll(ctx, hashKey).Result()
	if err != nil || len(raw) == 0 {
		if err == nil {
			_ = c.rdb.SRem(ctx, usageInflightUsersKey, strconv.FormatInt(userID, 10)).Err()
		}
		return nil, err
	}
	now := time.Now().UnixMilli()
	out := make([]service.UsageInflightSnapshot, 0, len(raw))
	var expired []string
	for field, payload := range raw {
		var snap service.UsageInflightSnapshot
		if json.Unmarshal([]byte(payload), &snap) != nil || snap.ExpiresAtMs <= now {
			expired = append(expired, field)
			continue
		}
		out = append(out, snap)
	}
	if len(expired) > 0 {
		_ = c.rdb.HDel(ctx, hashKey, expired...).Err()
	}
	if len(out) == 0 {
		_ = c.rdb.SRem(ctx, usageInflightUsersKey, strconv.FormatInt(userID, 10)).Err()
	}
	return out, nil
}

func (c *usageInflightCache) ListAll(ctx context.Context) ([]service.UsageInflightSnapshot, error) {
	if c == nil || c.rdb == nil {
		return nil, nil
	}
	ids, err := c.rdb.SMembers(ctx, usageInflightUsersKey).Result()
	if err != nil || len(ids) == 0 {
		return nil, err
	}
	if len(ids) > usageInflightUserCap {
		ids = ids[:usageInflightUserCap]
	}
	out := make([]service.UsageInflightSnapshot, 0, len(ids))
	for _, id := range ids {
		userID, convErr := strconv.ParseInt(id, 10, 64)
		if convErr != nil {
			_ = c.rdb.SRem(ctx, usageInflightUsersKey, id).Err()
			continue
		}
		rows, listErr := c.ListUser(ctx, userID)
		if listErr != nil {
			return out, listErr
		}
		out = append(out, rows...)
	}
	return out, nil
}
