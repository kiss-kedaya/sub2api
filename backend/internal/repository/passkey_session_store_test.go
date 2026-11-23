package repository

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

const passkeySessionTestTTL = 5 * time.Minute

// newPasskeySessionTestStores 用同一个 miniredis 构造两个 store，模拟生产上
// 「begin 落到 8491 实例、finish 落到 8432 实例」的多实例部署。
func newPasskeySessionTestStores(t *testing.T) (*miniredis.Miniredis, service.PasskeySessionStore, service.PasskeySessionStore) {
	t.Helper()
	mini := miniredis.RunT(t)
	newClient := func() *redis.Client {
		client := redis.NewClient(&redis.Options{Addr: mini.Addr()})
		t.Cleanup(func() { _ = client.Close() })
		return client
	}
	return mini, NewPasskeySessionStore(newClient()), NewPasskeySessionStore(newClient())
}

func newTestPasskeySession(kind string, userID int64) *service.PasskeySession {
	return &service.PasskeySession{
		Kind:   kind,
		UserID: userID,
		WebAuthn: webauthn.SessionData{
			Challenge:      "challenge-value",
			RelyingPartyID: "sub2api.example.com",
			UserID:         []byte("user-handle"),
		},
	}
}

func TestPasskeySessionStoreSharesChallengeAcrossInstances(t *testing.T) {
	mini, instanceA, instanceB := newPasskeySessionTestStores(t)
	ctx := context.Background()

	token, err := instanceA.Store(ctx, newTestPasskeySession("registration", 42), passkeySessionTestTTL)
	require.NoError(t, err)
	require.NotEmpty(t, token)

	// key 按「流程 + 用户」分片，运维可以直接按用户排查/清理。
	require.Contains(t, token, ".registration.42.")
	keys := mini.Keys()
	require.Len(t, keys, 1)
	require.Equal(t, passkeySessionPrefix+token, keys[0])
	require.True(t, strings.HasPrefix(keys[0], "passkey:session:v1.registration.42."))
	require.Positive(t, mini.TTL(keys[0]))
	require.LessOrEqual(t, mini.TTL(keys[0]), passkeySessionTestTTL)

	// begin 在 A 实例写入，finish 在 B 实例读取：多实例部署能拿到同一份 challenge。
	session, err := instanceB.Consume(ctx, token)
	require.NoError(t, err)
	require.NotNil(t, session)
	require.Equal(t, "registration", session.Kind)
	require.EqualValues(t, 42, session.UserID)
	require.Equal(t, "challenge-value", session.WebAuthn.Challenge)

	// 一次性：GETDEL 之后同样的 token 再消费必须失败（防重放）。
	_, err = instanceA.Consume(ctx, token)
	require.ErrorIs(t, err, service.ErrPasskeySessionExpired)
	require.Empty(t, mini.Keys())
}

func TestPasskeySessionStoreExpiresChallenge(t *testing.T) {
	mini, instanceA, instanceB := newPasskeySessionTestStores(t)
	ctx := context.Background()

	token, err := instanceA.Store(ctx, newTestPasskeySession("registration", 7), passkeySessionTestTTL)
	require.NoError(t, err)

	mini.FastForward(passkeySessionTestTTL + time.Second)

	_, err = instanceB.Consume(ctx, token)
	require.ErrorIs(t, err, service.ErrPasskeySessionExpired)
}

func TestPasskeySessionStoreRejectsMalformedOrMismatchedSessions(t *testing.T) {
	mini, instanceA, instanceB := newPasskeySessionTestStores(t)
	ctx := context.Background()

	// 旧格式（纯随机串）/ 空串 / 非法随机段：都按会话格式非法处理。
	for _, token := range []string{
		"",
		"legacy-random-token",
		"v1.registration.42.not-base64!!",
		"v2.registration.42." + strings.Repeat("A", 43),
	} {
		_, err := instanceA.Consume(ctx, token)
		require.ErrorIs(t, err, service.ErrPasskeySession)
	}

	// 格式合法但 Redis 中不存在（例如篡改了 user 段）：按 challenge 过期处理。
	_, err := instanceA.Consume(ctx, "v1.registration.43."+strings.Repeat("A", 43))
	require.ErrorIs(t, err, service.ErrPasskeySessionExpired)

	// payload 与 token 声明的流程/用户不一致（例如 key 被错误复用）也拒绝。
	token, err := instanceA.Store(ctx, newTestPasskeySession("registration", 42), passkeySessionTestTTL)
	require.NoError(t, err)
	mismatched := newTestPasskeySession("login", 0)
	payload, marshalErr := json.Marshal(mismatched)
	require.NoError(t, marshalErr)
	storeB, ok := instanceB.(*passkeySessionStore)
	require.True(t, ok)
	setCmd := storeB.redis.Set(ctx, passkeySessionKey(token), payload, time.Minute)
	require.NoError(t, setCmd.Err())

	_, err = instanceB.Consume(ctx, token)
	require.ErrorIs(t, err, service.ErrPasskeySession)
	require.Empty(t, mini.Keys())
}

func TestPasskeySessionStoreScopesLoginSessionsAsAnonymous(t *testing.T) {
	mini, instanceA, _ := newPasskeySessionTestStores(t)
	ctx := context.Background()

	token, err := instanceA.Store(ctx, newTestPasskeySession("login", 0), passkeySessionTestTTL)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(token, "v1.login.anon."))

	keys := mini.Keys()
	require.Len(t, keys, 1)
	require.True(t, strings.HasPrefix(keys[0], "passkey:session:v1.login.anon."))
}

// TestPasskeySessionStoreConsumesLegacyTokenDuringRollout 覆盖蓝绿发布窗口：
// 旧实例仍写入纯随机 token，新实例必须能消费一次，避免发布期间用户注册失败。
func TestPasskeySessionStoreConsumesLegacyTokenDuringRollout(t *testing.T) {
	_, instanceA, instanceB := newPasskeySessionTestStores(t)
	ctx := context.Background()

	legacyToken := base64.RawURLEncoding.EncodeToString(make([]byte, passkeySessionNonceBytes))
	require.Len(t, legacyToken, 43)
	payload, err := json.Marshal(newTestPasskeySession("registration", 42))
	require.NoError(t, err)
	storeA, ok := instanceA.(*passkeySessionStore)
	require.True(t, ok)
	legacyCmd := storeA.redis.Set(
		ctx, passkeySessionKey(legacyToken), payload, time.Minute)
	require.NoError(t, legacyCmd.Err())

	session, err := instanceB.Consume(ctx, legacyToken)
	require.NoError(t, err)
	require.EqualValues(t, 42, session.UserID)

	_, err = instanceA.Consume(ctx, legacyToken)
	require.ErrorIs(t, err, service.ErrPasskeySessionExpired)
}

func TestPasskeySessionStoreKeepsTighterWebAuthnExpiry(t *testing.T) {
	mini, instanceA, _ := newPasskeySessionTestStores(t)
	ctx := context.Background()

	session := newTestPasskeySession("registration", 9)
	session.WebAuthn.Expires = time.Now().Add(time.Minute)
	_, err := instanceA.Store(ctx, session, passkeySessionTestTTL)
	require.NoError(t, err)

	keys := mini.Keys()
	require.Len(t, keys, 1)
	require.LessOrEqual(t, mini.TTL(keys[0]), time.Minute)
}

func TestPasskeySessionStoreRejectsExpiredWebAuthnSession(t *testing.T) {
	_, instanceA, _ := newPasskeySessionTestStores(t)
	ctx := context.Background()

	session := newTestPasskeySession("registration", 9)
	session.WebAuthn.Expires = time.Now().Add(-time.Second)
	_, err := instanceA.Store(ctx, session, passkeySessionTestTTL)
	require.Error(t, err)
}
