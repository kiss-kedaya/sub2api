package repository

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

// WebAuthn 注册/登录的临时会话（challenge、user handle、RP 信息）存放在 Redis：
//   - 多实例共享：begin 落在 A 实例、finish 落在 B 实例也能读到同一份 challenge；
//   - 一次性：读取用 GETDEL，challenge 不能被重放；
//   - 有 TTL：默认 5 分钟（service.passkeySessionTTL），key 随会话过期自动删除；
//   - key 按「流程 + 用户」分片：`passkey:session:v1.<registration|login>.<userID|anon>.<随机串>`，
//     既方便运维按用户排查/清理，也不会把不同流程的会话混在一起。
const (
	passkeySessionPrefix       = "passkey:session:"
	passkeySessionTokenVersion = "v1"
	passkeySessionAnonScope    = "anon"
	// 43 字符的 base64url(32 字节随机数) + 前缀/流程/用户段，留出足够余量。
	passkeySessionMaxTokenLen = 192
	passkeySessionNonceBytes  = 32
)

type passkeySessionStore struct {
	redis *redis.Client
}

func NewPasskeySessionStore(redisClient *redis.Client) service.PasskeySessionStore {
	return &passkeySessionStore{redis: redisClient}
}

func (s *passkeySessionStore) Store(
	ctx context.Context,
	session *service.PasskeySession,
	ttl time.Duration,
) (string, error) {
	if session == nil || ttl <= 0 {
		return "", fmt.Errorf("invalid passkey session")
	}
	kind := strings.TrimSpace(session.Kind)
	if kind == "" || strings.Contains(kind, ".") {
		return "", fmt.Errorf("invalid passkey session kind")
	}
	// 库里的 WebAuthn 会话若自带绝对过期时间，以更早的那个为准（默认不启用强制超时，
	// 这里只是防御性处理），避免 Redis TTL 超过 challenge 本身的有效期。
	if !session.WebAuthn.Expires.IsZero() {
		remaining := time.Until(session.WebAuthn.Expires)
		if remaining <= 0 {
			return "", fmt.Errorf("passkey session already expired")
		}
		if remaining < ttl {
			ttl = remaining
		}
	}

	random := make([]byte, passkeySessionNonceBytes)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate passkey session token: %w", err)
	}
	scope := passkeySessionAnonScope
	if session.UserID > 0 {
		scope = strconv.FormatInt(session.UserID, 10)
	}
	token := strings.Join([]string{
		passkeySessionTokenVersion,
		kind,
		scope,
		base64.RawURLEncoding.EncodeToString(random),
	}, ".")

	payload, err := json.Marshal(session)
	if err != nil {
		return "", fmt.Errorf("encode passkey session: %w", err)
	}
	if err = s.redis.Set(ctx, passkeySessionKey(token), payload, ttl).Err(); err != nil {
		return "", fmt.Errorf("store passkey session: %w", err)
	}
	return token, nil
}

func (s *passkeySessionStore) Consume(
	ctx context.Context,
	token string,
) (*service.PasskeySession, error) {
	token = strings.TrimSpace(token)
	claims, structured := parsePasskeySessionToken(token)
	if !structured && !looksLikeLegacyPasskeySessionToken(token) {
		return nil, service.ErrPasskeySession
	}
	payload, err := s.redis.GetDel(ctx, passkeySessionKey(token)).Bytes()
	if err == redis.Nil {
		// 过期、已消费、或者 begin/finish 没落在共享同一 Redis 的实例上：
		// 对用户来说都是「这次 challenge 没了，重新来一次」。
		return nil, service.ErrPasskeySessionExpired
	}
	if err != nil {
		return nil, fmt.Errorf("consume passkey session: %w", err)
	}
	var session service.PasskeySession
	if err = json.Unmarshal(payload, &session); err != nil {
		return nil, service.ErrPasskeySession
	}
	// token 与 payload 必须互相印证：流程类型、用户维度都要一致。
	// 旧格式（滚动发布窗口内由旧实例写入）没有流程/用户段，跳过这层校验，
	// 其流程与用户仍由 service 层 FinishRegistration/FinishLogin 复核。
	if structured && !claims.matches(&session) {
		return nil, service.ErrPasskeySession
	}
	return &session, nil
}

func passkeySessionKey(token string) string {
	return passkeySessionPrefix + token
}

// looksLikeLegacyPasskeySessionToken 识别滚动发布窗口内旧实例写入的纯随机 token。
// 只为蓝绿发布兼容存在：下一个版本移除。
func looksLikeLegacyPasskeySessionToken(token string) bool {
	if len(token) != 43 {
		return false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	return err == nil && len(decoded) == passkeySessionNonceBytes
}

type passkeySessionTokenClaims struct {
	kind  string
	scope string
}

func parsePasskeySessionToken(token string) (passkeySessionTokenClaims, bool) {
	if token == "" || len(token) > passkeySessionMaxTokenLen {
		return passkeySessionTokenClaims{}, false
	}
	parts := strings.Split(token, ".")
	if len(parts) != 4 || parts[0] != passkeySessionTokenVersion {
		return passkeySessionTokenClaims{}, false
	}
	kind := strings.TrimSpace(parts[1])
	if kind == "" || strings.Contains(kind, ".") {
		return passkeySessionTokenClaims{}, false
	}
	scope := parts[2]
	if scope != passkeySessionAnonScope {
		userID, err := strconv.ParseInt(scope, 10, 64)
		if err != nil || userID <= 0 {
			return passkeySessionTokenClaims{}, false
		}
	}
	nonce, err := base64.RawURLEncoding.DecodeString(parts[3])
	if err != nil || len(nonce) != passkeySessionNonceBytes {
		return passkeySessionTokenClaims{}, false
	}
	return passkeySessionTokenClaims{kind: kind, scope: scope}, true
}

func (c passkeySessionTokenClaims) matches(session *service.PasskeySession) bool {
	if session == nil || session.Kind != c.kind {
		return false
	}
	if c.scope == passkeySessionAnonScope {
		return session.UserID == 0
	}
	return strconv.FormatInt(session.UserID, 10) == c.scope
}
