package service_test

import (
	"context"
	"net/http"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/repository" //nolint:depguard // 需要真实的 Passkey 会话存储来核对 token 形态、TTL 与一次性消费。
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/redis/go-redis/v9" //nolint:depguard // 需要真实的 Passkey 会话存储来核对 token 形态、TTL 与一次性消费。
	"github.com/stretchr/testify/require"
)

// 登录首步 Passkey 伪装在 PasskeyService 层的行为（审计 A-P1-02）。夹具全是假数据。

const disguiseServiceSecret = "passkey-disguise-service-test-secret-32b"

var disguiseTokenPattern = regexp.MustCompile(`^v1\.login_2fa\.(\d+)\.[A-Za-z0-9_-]{43}$`)

func newDisguiseServiceFixture(t *testing.T) (*miniredis.Miniredis, *service.PasskeyService, *crossInstancePasskeyRepo, *service.User, *service.LoginDisguise) {
	t.Helper()
	mini, svc, _, repo, user := newCrossInstancePasskeyFixture(t)
	repo.handles[user.ID] = []byte("disguise-service-test-handle-32b")
	repo.records = append(repo.records, service.PasskeyCredentialRecord{
		ID:         1,
		UserID:     user.ID,
		UserHandle: repo.handles[user.ID],
		Credential: webauthn.Credential{
			ID:        []byte("disguise-service-real-credential"),
			Transport: []protocol.AuthenticatorTransport{protocol.Internal},
		},
	})
	disguise := service.NewLoginDisguise(disguiseServiceSecret)
	require.NotNil(t, disguise)
	return mini, svc, repo, user, disguise
}

func disguiseFinishRequest(t *testing.T) *http.Request {
	t.Helper()
	return passkeyCredentialRequest(t, []byte(`{"id":"AA","rawId":"AA","type":"public-key","response":{}}`))
}

// 诱饵会话：token 形态 / TTL 与真实会话一致，带 Decoy 标记；镜像账号时用真实用户 ID 做 token 的用户 ID 段。
func TestBeginDisguisedLoginStoresMarkedSessionLikeARealOne(t *testing.T) {
	mini, svc, _, user, disguise := newDisguiseServiceFixture(t)
	ctx := context.Background()

	_, realToken, err := svc.BeginLoginForUser(ctx, user.ID, service.PasskeySessionKindLogin2FA)
	require.NoError(t, err)
	require.Regexp(t, disguiseTokenPattern, realToken)

	// 没有真实账号可镜像：HMAC 凭据 + 落在 [1,16384] 内的用户 ID 段。
	assertion, token, err := svc.BeginDisguisedLogin(ctx, disguise, "nobody@example.test", nil)
	require.NoError(t, err)
	require.NotNil(t, assertion)
	require.Regexp(t, disguiseTokenPattern, token)
	require.Equal(t, mini.TTL("passkey:session:"+realToken), mini.TTL("passkey:session:"+token), "TTL 必须与真实会话相同")
	require.Equal(t, 5*time.Minute, mini.TTL("passkey:session:"+token))
	stored, err := mini.Get("passkey:session:" + token)
	require.NoError(t, err)
	require.Contains(t, stored, `"decoy":true`)
	require.NotContains(t, func() string { s, _ := mini.Get("passkey:session:" + realToken); return s }(), "decoy")
	require.NotEmpty(t, assertion.Response.AllowedCredentials)
	require.Equal(t, protocol.VerificationRequired, assertion.Response.UserVerification)
	scopeOf := func(token string) string { return disguiseTokenPattern.FindStringSubmatch(token)[1] }
	require.Equal(t, scopeOf(token), strconv.FormatInt(svc.DisguiseScope(ctx, disguise, "nobody@example.test", nil), 10), "token 的用户 ID 段 = DisguiseScope")

	// 镜像真实账号：凭据与用户 ID 段都是真实的，与真实挑战一致。
	realAssertion, realMirrorToken, err := svc.BeginLoginForUser(ctx, user.ID, service.PasskeySessionKindLogin2FA)
	require.NoError(t, err)
	mirrorAssertion, mirrorToken, err := svc.BeginDisguisedLogin(ctx, disguise, user.Email, user)
	require.NoError(t, err)
	require.Equal(t, realAssertion.Response.AllowedCredentials, mirrorAssertion.Response.AllowedCredentials)
	require.Equal(t, realAssertion.Response.RelyingPartyID, mirrorAssertion.Response.RelyingPartyID)
	require.Equal(t, realAssertion.Response.Timeout, mirrorAssertion.Response.Timeout)
	require.Equal(t, scopeOf(realMirrorToken), scopeOf(mirrorToken), "用户 ID 段必须一致")
	require.Equal(t, strconv.FormatInt(user.ID, 10), scopeOf(mirrorToken))
	require.Equal(t, user.ID, svc.DisguiseScope(ctx, disguise, user.Email, user))
	require.NotEqual(t, realAssertion.Response.Challenge, mirrorAssertion.Response.Challenge, "challenge 每次随机")
}

// 诱饵 ceremony 对真实会话没有任何特权：真实登录会话拿到镜像账号的诱饵 ceremony，与拿到「同一用户的另一个
// 真实 ceremony」走同一条校验路径、同样需要认证器的有效签名，所以随便提交什么都只会得到校验错误。
func TestDecoyCeremonyGivesNoShortcutToARealLoginSession(t *testing.T) {
	_, svc, _, user, disguise := newDisguiseServiceFixture(t)
	ctx := context.Background()

	_, realOther, err := svc.BeginLoginForUser(ctx, user.ID, service.PasskeySessionKindLogin2FA)
	require.NoError(t, err)
	_, decoy, err := svc.BeginDisguisedLogin(ctx, disguise, user.Email, user)
	require.NoError(t, err)

	_, errReal := svc.FinishLoginForUser(ctx, user.ID, service.PasskeySessionKindLogin2FA, realOther, disguiseFinishRequest(t))
	_, errDecoy := svc.FinishLoginForUser(ctx, user.ID, service.PasskeySessionKindLogin2FA, decoy, disguiseFinishRequest(t))
	require.Error(t, errReal)
	require.Error(t, errDecoy)
	require.Equal(t, errReal, errDecoy, "诱饵 ceremony 在真实会话里的结果与另一个真实 ceremony 完全一致")
}

// FinishDisguisedLogin：只消费诱饵 ceremony；真实 ceremony / 用户 ID 段不一致一律 ErrPasskeySession；永远返回错误；一次性。
func TestFinishDisguisedLoginAlwaysFailsAndIsSingleUse(t *testing.T) {
	_, svc, _, user, disguise := newDisguiseServiceFixture(t)
	ctx := context.Background()
	const email = "nobody@example.test"
	scope := svc.DisguiseScope(ctx, disguise, email, nil)

	_, token, err := svc.BeginDisguisedLogin(ctx, disguise, email, nil)
	require.NoError(t, err)
	err = svc.FinishDisguisedLogin(ctx, token, disguiseFinishRequest(t), scope)
	require.Error(t, err)
	require.NotErrorIs(t, err, service.ErrPasskeySession)
	// 同一个 ceremony 第二次提交：已消费。
	require.ErrorIs(t, svc.FinishDisguisedLogin(ctx, token, disguiseFinishRequest(t), scope), service.ErrPasskeySessionExpired)

	// 用户 ID 段与登录会话不一致：与真实路径「ceremony 的用户 ID ≠ 登录会话的用户 ID」同样返回会话无效。
	_, other, err := svc.BeginDisguisedLogin(ctx, disguise, email, nil)
	require.NoError(t, err)
	require.ErrorIs(t, svc.FinishDisguisedLogin(ctx, other, disguiseFinishRequest(t), scope+1), service.ErrPasskeySession)

	// 真实 ceremony 不能被伪装路径消费（也就不能被拿来探测）。
	_, realToken, err := svc.BeginLoginForUser(ctx, user.ID, service.PasskeySessionKindLogin2FA)
	require.NoError(t, err)
	require.ErrorIs(t, svc.FinishDisguisedLogin(ctx, realToken, disguiseFinishRequest(t), user.ID), service.ErrPasskeySession)

	// 畸形 token 与真实路径同样的错误。
	require.ErrorIs(t, svc.FinishDisguisedLogin(ctx, "garbage", disguiseFinishRequest(t), scope), service.ErrPasskeySession)
}

func TestBeginDisguisedLoginPreconditions(t *testing.T) {
	_, svc, _, user, _ := newDisguiseServiceFixture(t)
	ctx := context.Background()

	// 没有伪装密钥：不生成任何东西。
	_, _, err := svc.BeginDisguisedLogin(ctx, nil, "nobody@example.test", nil)
	require.ErrorIs(t, err, service.ErrPasskeySession)
	require.Zero(t, svc.DisguiseScope(ctx, nil, "nobody@example.test", nil))

	// Passkey 服务未启用：与真实挑战同样的错误。
	mini := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	disabled, err := service.NewPasskeyService(&config.Config{}, newCrossInstancePasskeyRepo(), repository.NewPasskeySessionStore(client), &crossInstanceUserRepo{user: user})
	require.NoError(t, err)
	_, _, err = disabled.BeginDisguisedLogin(ctx, service.NewLoginDisguise(disguiseServiceSecret), "nobody@example.test", nil)
	require.ErrorIs(t, err, service.ErrPasskeysDisabled)
	require.ErrorIs(t, disabled.FinishDisguisedLogin(ctx, "whatever", disguiseFinishRequest(t), 0), service.ErrPasskeysDisabled)
}
