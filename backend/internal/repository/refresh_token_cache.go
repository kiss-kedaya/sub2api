package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const (
	refreshTokenKeyPrefix   = "refresh_token:"
	userRefreshTokensPrefix = "user_refresh_tokens:"
	tokenFamilyPrefix       = "token_family:"
	oauthLoginCodeKeyPrefix = "oauth:login-code:"
)

// consumeOAuthLoginCodeScript 原子地读取并删除一次性登录码，保证同一码
// 只可能被兑换一次（并发请求也只有一个能拿到值）。
var consumeOAuthLoginCodeScript = redis.NewScript(`
local value = redis.call('GET', KEYS[1])
if not value then return false end
redis.call('DEL', KEYS[1])
return value
`)

// refreshTokenKey generates the Redis key for a refresh token.
func refreshTokenKey(tokenHash string) string {
	return refreshTokenKeyPrefix + tokenHash
}

// userRefreshTokensKey generates the Redis key for user's token set.
func userRefreshTokensKey(userID int64) string {
	return fmt.Sprintf("%s%d", userRefreshTokensPrefix, userID)
}

// tokenFamilyKey generates the Redis key for token family set.
func tokenFamilyKey(familyID string) string {
	return tokenFamilyPrefix + familyID
}

type refreshTokenCache struct {
	rdb *redis.Client
}

// NewRefreshTokenCache creates a new RefreshTokenCache implementation.
func NewRefreshTokenCache(rdb *redis.Client) service.RefreshTokenCache {
	return &refreshTokenCache{rdb: rdb}
}

func (c *refreshTokenCache) StoreRefreshToken(ctx context.Context, tokenHash string, data *service.RefreshTokenData, ttl time.Duration) error {
	key := refreshTokenKey(tokenHash)
	val, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal refresh token data: %w", err)
	}
	return c.rdb.Set(ctx, key, val, ttl).Err()
}

func (c *refreshTokenCache) GetRefreshToken(ctx context.Context, tokenHash string) (*service.RefreshTokenData, error) {
	key := refreshTokenKey(tokenHash)
	val, err := c.rdb.Get(ctx, key).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, service.ErrRefreshTokenNotFound
		}
		return nil, err
	}
	var data service.RefreshTokenData
	if err := json.Unmarshal([]byte(val), &data); err != nil {
		return nil, fmt.Errorf("unmarshal refresh token data: %w", err)
	}
	return &data, nil
}

// consumeRefreshTokenScript 仅当令牌记录还在、且还没被消费时，才把它换成墓碑。
// 撤销（DeleteTokenFamily / DeleteUserRefreshTokens）会删掉记录，所以撤销之后的消费返回 0，
// 不会把已撤销的令牌写回去。不在 Lua 里解析整条 JSON（token_version 接近 2^63，cjson 会丢精度），
// 只做子串判断：RefreshTokenData 的 Consumed 字段 omitempty，已消费的记录里一定有 "consumed":true。
// 返回：0 记录不存在；1 已改写成墓碑；2 已经是墓碑（被别的请求抢先消费）。
var consumeRefreshTokenScript = redis.NewScript(`
local cur = redis.call('GET', KEYS[1])
if not cur then return 0 end
if string.find(cur, '"consumed":true', 1, true) then return 2 end
redis.call('SET', KEYS[1], ARGV[1], 'PX', ARGV[2])
return 1
`)

// reserveLeewayReissueScript 原子地占用一个宽限重发名额：把墓碑 JSON 里的 leeway_reissues 加一
// （字段不存在就追加在末尾），TTL 保持不变。墓碑不存在（已撤销）返回 -1，不写任何东西；
// 记录不是墓碑返回 -3；名额已满返回 -2；成功返回新的计数。
var reserveLeewayReissueScript = redis.NewScript(`
local cur = redis.call('GET', KEYS[1])
if not cur then return -1 end
if not string.find(cur, '"consumed":true', 1, true) then return -3 end
local n = tonumber(string.match(cur, '"leeway_reissues":(%d+)') or '0')
if n >= tonumber(ARGV[1]) then return -2 end
n = n + 1
local nxt
if string.find(cur, '"leeway_reissues":', 1, true) then
  nxt = string.gsub(cur, '"leeway_reissues":%d+', '"leeway_reissues":' .. n, 1)
else
  nxt = string.sub(cur, 1, -2) .. ',"leeway_reissues":' .. n .. '}'
end
local ttl = redis.call('PTTL', KEYS[1])
if ttl > 0 then
  redis.call('SET', KEYS[1], nxt, 'PX', ttl)
else
  redis.call('SET', KEYS[1], nxt)
end
return n
`)

// ConsumeRefreshToken 实现 service.RefreshTokenAtomicStore：令牌记录还在且未被消费才改写成墓碑。
func (c *refreshTokenCache) ConsumeRefreshToken(ctx context.Context, tokenHash string, tombstone *service.RefreshTokenData, ttl time.Duration) (service.RefreshConsumeOutcome, error) {
	val, err := json.Marshal(tombstone)
	if err != nil {
		return service.RefreshConsumeMissing, fmt.Errorf("marshal refresh token tombstone: %w", err)
	}
	ms := ttl.Milliseconds()
	if ms < 1 {
		ms = 1
	}
	res, err := consumeRefreshTokenScript.Run(ctx, c.rdb, []string{refreshTokenKey(tokenHash)}, string(val), ms).Int()
	if err != nil {
		return service.RefreshConsumeMissing, err
	}
	switch res {
	case 1:
		return service.RefreshConsumeWon, nil
	case 2:
		return service.RefreshConsumeAlready, nil
	default:
		return service.RefreshConsumeMissing, nil
	}
}

// ReserveLeewayReissue 实现 service.RefreshTokenAtomicStore：原子地占用一个宽限重发名额。
func (c *refreshTokenCache) ReserveLeewayReissue(ctx context.Context, tokenHash string, maxReissues int) (service.RefreshLeewayOutcome, int, error) {
	res, err := reserveLeewayReissueScript.Run(ctx, c.rdb, []string{refreshTokenKey(tokenHash)}, maxReissues).Int()
	if err != nil {
		return service.RefreshLeewayMissing, 0, err
	}
	switch {
	case res > 0:
		return service.RefreshLeewayReserved, res, nil
	case res == -2:
		return service.RefreshLeewayCapped, maxReissues, nil
	case res == -3:
		return service.RefreshLeewayNotConsumed, 0, nil
	default:
		return service.RefreshLeewayMissing, 0, nil
	}
}

var _ service.RefreshTokenAtomicStore = (*refreshTokenCache)(nil)

func (c *refreshTokenCache) DeleteRefreshToken(ctx context.Context, tokenHash string) error {
	key := refreshTokenKey(tokenHash)
	return c.rdb.Del(ctx, key).Err()
}

func (c *refreshTokenCache) DeleteUserRefreshTokens(ctx context.Context, userID int64) error {
	if err := c.revokeCryptoGrants(ctx, service.CryptoGrantUserRevocationKey(userID)); err != nil {
		return err
	}
	// Get all token hashes for this user
	tokenHashes, err := c.GetUserTokenHashes(ctx, userID)
	if err != nil && err != redis.Nil {
		return fmt.Errorf("get user token hashes: %w", err)
	}

	if len(tokenHashes) == 0 {
		return nil
	}

	// Build keys to delete
	keys := make([]string, 0, len(tokenHashes)+1)
	for _, hash := range tokenHashes {
		keys = append(keys, refreshTokenKey(hash))
	}
	keys = append(keys, userRefreshTokensKey(userID))

	// Delete all keys in a pipeline
	pipe := c.rdb.Pipeline()
	for _, key := range keys {
		pipe.Del(ctx, key)
	}
	_, err = pipe.Exec(ctx)
	return err
}

func (c *refreshTokenCache) DeleteTokenFamily(ctx context.Context, familyID string) error {
	if err := c.revokeCryptoGrants(ctx, service.CryptoGrantSessionRevocationKey(familyID)); err != nil {
		return err
	}
	// Get all token hashes in this family
	tokenHashes, err := c.GetFamilyTokenHashes(ctx, familyID)
	if err != nil && err != redis.Nil {
		return fmt.Errorf("get family token hashes: %w", err)
	}

	if len(tokenHashes) == 0 {
		return nil
	}

	// Build keys to delete
	keys := make([]string, 0, len(tokenHashes)+1)
	for _, hash := range tokenHashes {
		keys = append(keys, refreshTokenKey(hash))
	}
	keys = append(keys, tokenFamilyKey(familyID))

	// Delete all keys in a pipeline
	pipe := c.rdb.Pipeline()
	for _, key := range keys {
		pipe.Del(ctx, key)
	}
	_, err = pipe.Exec(ctx)
	return err
}

// These short-lived tombstones revoke payment grants only. Ordinary refresh
// rotation calls DeleteRefreshToken and intentionally does not write one.
func (c *refreshTokenCache) revokeCryptoGrants(ctx context.Context, key string) error {
	return redis.NewScript(`
local previous = redis.call('GET', KEYS[1])
local current = ARGV[1]
if previous and tonumber(previous) > tonumber(current) then current = previous end
redis.call('SET', KEYS[1], current, 'PX', ARGV[2])
return 1
`).Run(ctx, c.rdb, []string{key}, time.Now().UnixMilli(), (5 * time.Minute).Milliseconds()).Err()
}

func (c *refreshTokenCache) AddToUserTokenSet(ctx context.Context, userID int64, tokenHash string, ttl time.Duration) error {
	key := userRefreshTokensKey(userID)
	pipe := c.rdb.Pipeline()
	pipe.SAdd(ctx, key, tokenHash)
	pipe.Expire(ctx, key, ttl)
	_, err := pipe.Exec(ctx)
	return err
}

func (c *refreshTokenCache) AddToFamilyTokenSet(ctx context.Context, familyID string, tokenHash string, ttl time.Duration) error {
	key := tokenFamilyKey(familyID)
	pipe := c.rdb.Pipeline()
	pipe.SAdd(ctx, key, tokenHash)
	pipe.Expire(ctx, key, ttl)
	_, err := pipe.Exec(ctx)
	return err
}

func (c *refreshTokenCache) GetUserTokenHashes(ctx context.Context, userID int64) ([]string, error) {
	key := userRefreshTokensKey(userID)
	return c.rdb.SMembers(ctx, key).Result()
}

func (c *refreshTokenCache) GetFamilyTokenHashes(ctx context.Context, familyID string) ([]string, error) {
	key := tokenFamilyKey(familyID)
	return c.rdb.SMembers(ctx, key).Result()
}

func (c *refreshTokenCache) IsTokenInFamily(ctx context.Context, familyID string, tokenHash string) (bool, error) {
	key := tokenFamilyKey(familyID)
	return c.rdb.SIsMember(ctx, key, tokenHash).Result()
}

const accessTokenRevokedBeforeKeyPrefix = "auth:revoked_before:"

func accessTokenRevokedBeforeKey(userID int64) string {
	return accessTokenRevokedBeforeKeyPrefix + strconv.FormatInt(userID, 10)
}

// RevokeAccessTokensBefore records the cutoff for stateless access tokens.
func (c *refreshTokenCache) RevokeAccessTokensBefore(ctx context.Context, userID int64, before time.Time, ttl time.Duration) error {
	if c == nil || c.rdb == nil || userID <= 0 {
		return nil
	}
	return c.rdb.Set(ctx, accessTokenRevokedBeforeKey(userID), before.UTC().Format(time.RFC3339Nano), ttl).Err()
}

// AccessTokensRevokedBefore returns the stored cutoff; zero means none.
func (c *refreshTokenCache) AccessTokensRevokedBefore(ctx context.Context, userID int64) (time.Time, error) {
	if c == nil || c.rdb == nil || userID <= 0 {
		return time.Time{}, nil
	}
	value, err := c.rdb.Get(ctx, accessTokenRevokedBeforeKey(userID)).Result()
	if errors.Is(err, redis.Nil) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, err
	}
	return parsed, nil
}

// StoreOAuthLoginCode 写入一次性 OAuth 登录码（只存哈希），ttl 到点自动过期。
func (c *refreshTokenCache) StoreOAuthLoginCode(ctx context.Context, codeHash string, data *service.OAuthLoginCodeData, ttl time.Duration) error {
	if c == nil || c.rdb == nil || codeHash == "" || data == nil || ttl <= 0 {
		return service.ErrOAuthLoginCodeNotFound
	}
	payload, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal oauth login code: %w", err)
	}
	return c.rdb.Set(ctx, oauthLoginCodeKeyPrefix+codeHash, payload, ttl).Err()
}

// ConsumeOAuthLoginCode 原子地读取并删除一次性 OAuth 登录码。
func (c *refreshTokenCache) ConsumeOAuthLoginCode(ctx context.Context, codeHash string) (*service.OAuthLoginCodeData, error) {
	if c == nil || c.rdb == nil || codeHash == "" {
		return nil, service.ErrOAuthLoginCodeNotFound
	}
	raw, err := consumeOAuthLoginCodeScript.Run(ctx, c.rdb, []string{oauthLoginCodeKeyPrefix + codeHash}).Text()
	if errors.Is(err, redis.Nil) {
		return nil, service.ErrOAuthLoginCodeNotFound
	}
	if err != nil {
		return nil, err
	}
	var data service.OAuthLoginCodeData
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		return nil, fmt.Errorf("unmarshal oauth login code: %w", err)
	}
	return &data, nil
}

var _ service.OAuthLoginCodeStore = (*refreshTokenCache)(nil)
