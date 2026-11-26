package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 「校验当前密码」与 Passkey 创建/删除这两类入口也走 step-up 对话框（同一个 STEP_UP_REQUIRED 机器码），
// 它们用的是 5 分钟不透明凭证 Cookie（每个因子一枚）而不是 Redis 里的会话授权。
// 老板 2026-11-26：同时绑定 TOTP 与 Passkey 时验证其一即可——这里必须与 step-up 中间件同一条规则，
// 否则对话框验证完一个因子就关闭、重试后这些入口又返回 STEP_UP_REQUIRED，用户永远卡在循环里。
// 登录不走这里：登录仍要求全部已绑定因子（见 login_factor_policy_test.go）。

type anyFactorGate struct {
	totp  *service.TotpService
	user  *service.User
	cache *twoFactorGrantCacheStub
}

func newAnyFactorGate(t *testing.T, totpBound, passkeyBound bool, passkeyErr error) *anyFactorGate {
	t.Helper()
	user, _, encryptor := newTwoFactorGrantTestUser(t, 7, "correct-password", totpBound)
	userRepo := &twoFactorGrantUserRepo{user: user}
	cache := newTwoFactorGrantCacheStub()
	settings := newTwoFactorGrantTestSettings(map[string]string{service.SettingKeyTotpEnabled: "true"})
	totpSvc := service.NewTotpService(userRepo, encryptor, cache, settings, nil, nil)
	if passkeyBound || passkeyErr != nil {
		repo := &factorLoginPasskeyRepo{err: passkeyErr}
		if passkeyBound {
			repo.records = []service.PasskeyCredentialRecord{{ID: 1, UserID: user.ID}}
		}
		totpSvc.SetPasskeyRepository(repo)
	}
	return &anyFactorGate{totp: totpSvc, user: user, cache: cache}
}

func (g *anyFactorGate) request() (*gin.Context, *httptest.ResponseRecorder) {
	return newPasskeyAuthenticatedContext(g.user.ID, http.MethodPost, "/api/v1/users/me/password", `{}`, nil)
}

// grantToken 为 userID 的 factor 因子签发一枚 5 分钟凭证（绑定 c 这次请求的 IP/UA），返回 Cookie 值。
func (g *anyFactorGate) grantToken(t *testing.T, c *gin.Context, userID int64, factor service.Factor) string {
	t.Helper()
	binding := service.TwoFactorGrantBinding{IP: ip.GetClientIP(c), UserAgent: c.GetHeader("User-Agent")}
	grant, err := g.totp.IssueTwoFactorGrant(context.Background(), userID, service.TwoFactorGrantScopePassword, factor, binding)
	require.NoError(t, err)
	require.NotEmpty(t, grant.Token)
	return grant.Token
}

func presentGrant(c *gin.Context, slot service.Factor, token string) {
	c.Request.AddCookie(&http.Cookie{Name: twoFactorGrantCookieName(slot), Value: token})
}

func TestPasswordVerificationGateAcceptsAnyOneBoundFactorGrant(t *testing.T) {
	gin.SetMode(gin.TestMode)
	type world struct{ totpBound, passkeyBound bool }
	both := world{true, true}
	totpOnly := world{totpBound: true}
	passkeyOnly := world{passkeyBound: true}
	none := world{}

	// cookie 描述浏览器带来的一枚凭证 Cookie：slot 是 Cookie 名对应的因子槽位，
	// factor 是凭证本身记录的因子，otherUser 表示凭证签发给了别的用户，forged 表示随手编的值。
	type cookie struct {
		slot, factor service.Factor
		otherUser    bool
		forged       bool
	}
	cases := []struct {
		name        string
		world       world
		cookies     []cookie
		wantPass    bool
		wantFactors string
	}{
		// 两者都绑定：任选其一。
		{"both / totp grant only", both, []cookie{{slot: service.FactorTOTP, factor: service.FactorTOTP}}, true, "totp"},
		{"both / passkey grant only", both, []cookie{{slot: service.FactorPasskey, factor: service.FactorPasskey}}, true, "passkey"},
		{"both / both grants", both, []cookie{
			{slot: service.FactorTOTP, factor: service.FactorTOTP}, {slot: service.FactorPasskey, factor: service.FactorPasskey},
		}, true, "totp,passkey"},
		{"both / no grant", both, nil, false, ""},
		{"both / forged cookies in both slots", both, []cookie{
			{slot: service.FactorTOTP, forged: true}, {slot: service.FactorPasskey, forged: true},
		}, false, ""},
		// 凭证记录着签发它的因子：TOTP 凭证放进 Passkey 槽位（或反过来）一律不算。
		{"both / totp grant presented in the passkey slot", both, []cookie{{slot: service.FactorPasskey, factor: service.FactorTOTP}}, false, ""},
		{"both / passkey grant presented in the totp slot", both, []cookie{{slot: service.FactorTOTP, factor: service.FactorPasskey}}, false, ""},
		{"both / grants issued to another user", both, []cookie{
			{slot: service.FactorTOTP, factor: service.FactorTOTP, otherUser: true}, {slot: service.FactorPasskey, factor: service.FactorPasskey, otherUser: true},
		}, false, ""},
		// 单因子账号：与改动前一致。
		{"totp only / totp grant", totpOnly, []cookie{{slot: service.FactorTOTP, factor: service.FactorTOTP}}, true, "totp"},
		{"totp only / passkey grant (unbound factor)", totpOnly, []cookie{{slot: service.FactorPasskey, factor: service.FactorPasskey}}, false, ""},
		{"totp only / no grant", totpOnly, nil, false, ""},
		{"passkey only / passkey grant", passkeyOnly, []cookie{{slot: service.FactorPasskey, factor: service.FactorPasskey}}, true, "passkey"},
		{"passkey only / totp grant (unbound factor)", passkeyOnly, []cookie{{slot: service.FactorTOTP, factor: service.FactorTOTP}}, false, ""},
		{"passkey only / no grant", passkeyOnly, nil, false, ""},
		// 没有任何因子：不额外要求（原有行为）。
		{"no factor / no grant", none, nil, true, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gate := newAnyFactorGate(t, tc.world.totpBound, tc.world.passkeyBound, nil)
			c, rec := gate.request()
			for _, item := range tc.cookies {
				token := "forged-token-value"
				if !item.forged {
					owner := gate.user.ID
					if item.otherUser {
						owner = 99
					}
					token = gate.grantToken(t, c, owner, item.factor)
				}
				presentGrant(c, item.slot, token)
			}

			passed := requirePasswordVerificationGrant(c, gate.totp, gate.user)
			require.Equal(t, tc.wantPass, passed, rec.Body.String())
			if tc.wantPass {
				require.Zero(t, rec.Body.Len(), "放行时不得写响应体")
				if tc.wantFactors != "" {
					raw, ok := c.Get("audit_extra")
					require.True(t, ok, "满足的因子必须进入审计附加字段")
					extra, isMap := raw.(map[string]any)
					require.True(t, isMap)
					require.Equal(t, tc.wantFactors, extra["two_factor_satisfied_factors"])
				}
				return
			}
			require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
			var body struct {
				Reason   string            `json:"reason"`
				Metadata map[string]string `json:"metadata"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
			require.Equal(t, "STEP_UP_REQUIRED", body.Reason)
			// 挑战体告诉客户端：已绑定因子任选其一。
			require.Equal(t, "any", body.Metadata["step_up_mode"])
			require.NotEmpty(t, body.Metadata["factors"])
		})
	}
}

// 策略读取失败（Passkey 仓库故障）：fail-closed，连有效的 TOTP 凭证也不放行。
func TestPasswordVerificationGateFailsClosedWhenThePolicyCannotBeRead(t *testing.T) {
	gin.SetMode(gin.TestMode)
	gate := newAnyFactorGate(t, true, false, errors.New("passkey db down"))
	c, rec := gate.request()
	presentGrant(c, service.FactorTOTP, gate.grantToken(t, c, gate.user.ID, service.FactorTOTP))

	require.False(t, requirePasswordVerificationGrant(c, gate.totp, gate.user))
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Contains(t, rec.Body.String(), "TWO_FACTOR_POLICY_UNAVAILABLE")
}

// 凭证存储不可用：读不到凭证按「没有凭证」处理，不放行。
func TestPasswordVerificationGateFailsClosedWhenTheGrantStoreIsDown(t *testing.T) {
	gin.SetMode(gin.TestMode)
	gate := newAnyFactorGate(t, true, true, nil)
	c, rec := gate.request()
	presentGrant(c, service.FactorTOTP, gate.grantToken(t, c, gate.user.ID, service.FactorTOTP))
	presentGrant(c, service.FactorPasskey, gate.grantToken(t, c, gate.user.ID, service.FactorPasskey))
	gate.cache.failLoad = true

	require.False(t, requirePasswordVerificationGrant(c, gate.totp, gate.user))
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Contains(t, rec.Body.String(), "STEP_UP_REQUIRED")
}

// 真实入口：同时绑定 TOTP 与 Passkey 的账号删除 Passkey，验证其一即可；一个都没有照旧 403 且不删除。
func TestPasskeyDeleteWithBothFactorsBoundAcceptsEitherFactorGrant(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name       string
		factor     *service.Factor
		wantStatus int
		wantDelete bool
	}{
		{"totp grant", factorPtr(service.FactorTOTP), http.StatusOK, true},
		{"passkey grant", factorPtr(service.FactorPasskey), http.StatusOK, true},
		{"no grant", nil, http.StatusForbidden, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newPasskeyGateFixture(t, true)
			// 让策略把 Passkey 也读成「已绑定」（删除本身走 fixture 里的仓库替身）。
			fixture.totp.SetPasskeyRepository(&factorLoginPasskeyRepo{
				records: []service.PasskeyCredentialRecord{{ID: 3, UserID: fixture.user.ID}},
			})
			ginContext, recorder := newPasskeyAuthenticatedContext(
				fixture.user.ID, http.MethodDelete, "/api/v1/user/passkeys/3",
				`{"password":"correct-password"}`, gin.Params{{Key: "id", Value: "3"}})
			if tc.factor != nil {
				binding := service.TwoFactorGrantBinding{IP: ip.GetClientIP(ginContext), UserAgent: ginContext.GetHeader("User-Agent")}
				grant, err := fixture.totp.IssueTwoFactorGrant(
					ginContext.Request.Context(), fixture.user.ID, service.TwoFactorGrantScopePassword, *tc.factor, binding)
				require.NoError(t, err)
				presentGrant(ginContext, *tc.factor, grant.Token)
			}

			fixture.handler.Delete(ginContext)

			require.Equal(t, tc.wantStatus, recorder.Code, recorder.Body.String())
			require.Equal(t, tc.wantDelete, fixture.repo.deleteCalled)
			if !tc.wantDelete {
				require.Contains(t, recorder.Body.String(), "STEP_UP_REQUIRED")
			}
		})
	}
}

func factorPtr(factor service.Factor) *service.Factor { return &factor }
