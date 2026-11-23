package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/require"
)

// factorLoginPasskeyRepo 是登录/step-up 编排测试用的最小 Passkey 仓库：
// 绑定状态只看 ListByUserID，assertion 挑战需要 GetUserHandle。
type factorLoginPasskeyRepo struct {
	service.PasskeyRepository
	records []service.PasskeyCredentialRecord
	handle  []byte
	err     error
}

func (r *factorLoginPasskeyRepo) ListByUserID(context.Context, int64) ([]service.PasskeyCredentialRecord, error) {
	return r.records, r.err
}

func (r *factorLoginPasskeyRepo) GetUserHandle(context.Context, int64) ([]byte, error) {
	if len(r.handle) == 0 {
		return nil, service.ErrPasskeyNotFound
	}
	return r.handle, nil
}

// factorLoginSessionStore 是无 Redis 的 ceremony 会话替身。
type factorLoginSessionStore struct {
	service.PasskeySessionStore
	sessions map[string]*service.PasskeySession
	nextID   int
}

func newFactorLoginSessionStore() *factorLoginSessionStore {
	return &factorLoginSessionStore{sessions: map[string]*service.PasskeySession{}}
}

func (s *factorLoginSessionStore) Store(_ context.Context, session *service.PasskeySession, _ time.Duration) (string, error) {
	s.nextID++
	token := fmt.Sprintf("passkey-ceremony-%d", s.nextID)
	copied := *session
	s.sessions[token] = &copied
	return token, nil
}

func (s *factorLoginSessionStore) Consume(_ context.Context, token string) (*service.PasskeySession, error) {
	session, ok := s.sessions[token]
	if !ok {
		return nil, service.ErrPasskeySession
	}
	delete(s.sessions, token)
	return session, nil
}

func newFactorLoginTestPasskeyRepo(userID int64) *factorLoginPasskeyRepo {
	return &factorLoginPasskeyRepo{
		handle: []byte("factor-login-test-user-handle-32b"),
		records: []service.PasskeyCredentialRecord{{
			ID:         1,
			UserID:     userID,
			UserHandle: []byte("factor-login-test-user-handle-32b"),
			Credential: webauthn.Credential{ID: []byte{0x01, 0x02, 0x03, 0x04}},
		}},
	}
}

func newFactorLoginTestPasskeyService(
	t *testing.T,
	userRepo *twoFactorGrantUserRepo,
	passkeyRepo *factorLoginPasskeyRepo,
) (*service.PasskeyService, *factorLoginSessionStore) {
	t.Helper()
	cfg := &config.Config{WebAuthn: config.WebAuthnConfig{
		Enabled:       true,
		RPDisplayName: "Sub2API",
		RPID:          "example.test",
		RPOrigins:     []string{"https://example.test"},
	}}
	sessions := newFactorLoginSessionStore()
	svc, err := service.NewPasskeyService(cfg, passkeyRepo, sessions, userRepo)
	require.NoError(t, err)
	require.True(t, svc.Enabled())
	return svc, sessions
}

// newFactorLoginTestHandler 组装带 Passkey 因子的 AuthHandler。
func newFactorLoginTestHandler(
	t *testing.T,
	totpEnabled bool,
	bindPasskey bool,
) (*AuthHandler, *service.TotpService, *service.User, string, *factorLoginPasskeyRepo) {
	t.Helper()
	user, secret, encryptor := newTwoFactorGrantTestUser(t, 11, "correct-password", totpEnabled)
	repo := &twoFactorGrantUserRepo{user: user}
	cache := newTwoFactorGrantCacheStub()
	h, totpSvc, _ := newTwoFactorGrantAuthHandler(t, newTwoFactorGrantTestConfig(), repo, cache, user, encryptor)

	var passkeyRepo *factorLoginPasskeyRepo
	if bindPasskey {
		passkeyRepo = newFactorLoginTestPasskeyRepo(user.ID)
		totpSvc.SetPasskeyRepository(passkeyRepo)
		passkeySvc, _ := newFactorLoginTestPasskeyService(t, repo, passkeyRepo)
		h.SetPasskeyService(passkeySvc)
	}
	return h, totpSvc, user, secret, passkeyRepo
}

type loginFactorResponse struct {
	AccessToken         string          `json:"access_token"`
	Requires2FA         bool            `json:"requires_2fa"`
	TempToken           string          `json:"temp_token"`
	UserEmailMasked     string          `json:"user_email_masked"`
	RequiredFactors     []string        `json:"required_factors"`
	PassedFactors       []string        `json:"passed_factors"`
	NextFactor          string          `json:"next_factor"`
	PasskeySessionToken string          `json:"passkey_session_token"`
	PasskeyOptions      json.RawMessage `json:"passkey_options"`
}

func doFactorLoginRequest(t *testing.T, r *gin.Engine, method, path, body string, userAgent string, cookies ...*http.Cookie) (*httptest.ResponseRecorder, loginFactorResponse) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var envelope struct {
		Code int                 `json:"code"`
		Data loginFactorResponse `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &envelope)
	return w, envelope.Data
}

func newFactorLoginRouter(h *AuthHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/v1/auth/login", h.Login)
	r.POST("/api/v1/auth/login/2fa", h.Login2FA)
	r.POST("/api/v1/auth/login/2fa/passkey/finish", h.Login2FAPasskeyFinish)
	return r
}

// 只绑 Passkey：密码通过后直接下发 Passkey assertion 挑战，不签发令牌。
func TestLoginPasskeyOnlyChallengesPasskey(t *testing.T) {
	h, _, user, _, _ := newFactorLoginTestHandler(t, false, true)
	r := newFactorLoginRouter(h)

	w, data := doFactorLoginRequest(t, r, http.MethodPost, "/api/v1/auth/login",
		loginRequestBody(user.Email, "correct-password"), "factor-login-agent")

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Empty(t, data.AccessToken, "Passkey 因子未完成前不得签发令牌")
	require.True(t, data.Requires2FA)
	require.NotEmpty(t, data.TempToken)
	require.Equal(t, []string{"passkey"}, data.RequiredFactors)
	require.Equal(t, "passkey", data.NextFactor)
	require.NotEmpty(t, data.PasskeySessionToken)
	require.NotEmpty(t, data.PasskeyOptions)
	require.NotContains(t, w.Body.String(), "access_token")
	require.Empty(t, data.PassedFactors)
}

// 两者都绑：先 TOTP（next_factor=totp），TOTP 通过后转 Passkey，仍不签发令牌。
func TestLoginBothFactorsRequiresTOTPThenPasskey(t *testing.T) {
	h, _, user, secret, _ := newFactorLoginTestHandler(t, true, true)
	r := newFactorLoginRouter(h)

	w, data := doFactorLoginRequest(t, r, http.MethodPost, "/api/v1/auth/login",
		loginRequestBody(user.Email, "correct-password"), "factor-login-agent")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Empty(t, data.AccessToken)
	require.Equal(t, "totp", data.NextFactor)
	require.Equal(t, []string{"totp", "passkey"}, data.RequiredFactors)
	require.Empty(t, data.PasskeyOptions, "第一步只挑战 TOTP，还未到 Passkey")

	code, err := totp.GenerateCode(secret, time.Now())
	require.NoError(t, err)
	body, _ := json.Marshal(map[string]string{
		"temp_token": data.TempToken,
		"totp_code":  code,
		"email":      user.Email,
		"password":   "correct-password",
	})
	w2, data2 := doFactorLoginRequest(t, r, http.MethodPost, "/api/v1/auth/login/2fa",
		string(body), "factor-login-agent")
	require.Equal(t, http.StatusOK, w2.Code, w2.Body.String())
	require.Empty(t, data2.AccessToken, "TOTP 通过但还缺 Passkey，不得签发令牌")
	require.True(t, data2.Requires2FA)
	require.Equal(t, "passkey", data2.NextFactor)
	require.Equal(t, []string{"totp", "passkey"}, data2.RequiredFactors)
	require.Equal(t, []string{"totp"}, data2.PassedFactors)
	require.NotEmpty(t, data2.PasskeySessionToken)
	require.NotEmpty(t, data2.PasskeyOptions)

	// TOTP 因子凭证已下发（scope=login, factor=totp），用于 5 分钟内免重复 TOTP。
	totpCookie := ""
	for _, cookie := range w2.Result().Cookies() {
		if cookie.Name == service.TwoFactorGrantCookieName && cookie.Value != "" {
			totpCookie = cookie.Value
		}
	}
	require.NotEmpty(t, totpCookie)

	// 伪造的 Passkey assertion 必须被拒绝，且不签发令牌（fail-closed）。
	invalidBody := `{"temp_token":"` + data2.TempToken + `","session_token":"` + data2.PasskeySessionToken + `","credential":{"id":"AA","rawId":"AA","type":"public-key","response":{}}}`
	w3, data3 := doFactorLoginRequest(t, r, http.MethodPost, "/api/v1/auth/login/2fa/passkey/finish",
		invalidBody, "factor-login-agent")
	require.NotEqual(t, http.StatusOK, w3.Code)
	require.Empty(t, data3.AccessToken)
	require.NotContains(t, w3.Body.String(), "access_token")

	// 5 分钟内重新用同一 TOTP 因子凭证登录：第一步直接跳过 TOTP 只挑战 Passkey。
	w4, data4 := doFactorLoginRequest(t, r, http.MethodPost, "/api/v1/auth/login",
		loginRequestBody(user.Email, "correct-password"), "factor-login-agent",
		&http.Cookie{Name: service.TwoFactorGrantCookieName, Value: totpCookie})
	require.Equal(t, http.StatusOK, w4.Code, w4.Body.String())
	require.Empty(t, data4.AccessToken)
	require.Equal(t, "passkey", data4.NextFactor)
	require.Equal(t, []string{"totp"}, data4.PassedFactors)
}

// 只绑 Passkey 的账号：有有效 Passkey 因子凭证时 5 分钟内免重复验证。
func TestLoginPasskeyFactorGrantSkipsChallenge(t *testing.T) {
	h, totpSvc, user, _, _ := newFactorLoginTestHandler(t, false, true)
	r := newFactorLoginRouter(h)

	binding := service.TwoFactorGrantBinding{IP: "192.0.2.1", UserAgent: "factor-login-agent"}
	grant, err := totpSvc.IssueTwoFactorGrant(context.Background(), user.ID,
		service.TwoFactorGrantScopeLogin, service.FactorPasskey, binding)
	require.NoError(t, err)

	w, data := doFactorLoginRequest(t, r, http.MethodPost, "/api/v1/auth/login",
		loginRequestBody(user.Email, "correct-password"), "factor-login-agent",
		&http.Cookie{Name: service.TwoFactorPasskeyGrantCookieName, Value: grant.Token})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NotEmpty(t, data.AccessToken, "Passkey 因子凭证有效时必须直接签发令牌")
	require.False(t, data.Requires2FA)
}

// TOTP 凭证不能冒充 Passkey 凭证跳过 Passkey 因子。
func TestLoginTOTPGrantCannotSatisfyPasskeyFactor(t *testing.T) {
	h, totpSvc, user, _, _ := newFactorLoginTestHandler(t, false, true)
	r := newFactorLoginRouter(h)

	binding := service.TwoFactorGrantBinding{IP: "192.0.2.1", UserAgent: "factor-login-agent"}
	grant, err := totpSvc.IssueTwoFactorGrant(context.Background(), user.ID,
		service.TwoFactorGrantScopeLogin, service.FactorTOTP, binding)
	require.NoError(t, err)

	w, data := doFactorLoginRequest(t, r, http.MethodPost, "/api/v1/auth/login",
		loginRequestBody(user.Email, "correct-password"), "factor-login-agent",
		&http.Cookie{Name: service.TwoFactorGrantCookieName, Value: grant.Token})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Empty(t, data.AccessToken)
	require.Equal(t, "passkey", data.NextFactor)
}

// Passkey finish 只接受「要求 Passkey 因子」的登录会话，其余一律拒绝。
func TestLogin2FAPasskeyFinishRejectsSessionWithoutPasskeyFactor(t *testing.T) {
	h, totpSvc, user, _, _ := newFactorLoginTestHandler(t, true, false)
	r := newFactorLoginRouter(h)

	tempToken, err := totpSvc.CreateLoginSessionForFactors(context.Background(), user.ID, user.Email,
		[]service.Factor{service.FactorTOTP}, nil)
	require.NoError(t, err)
	body := `{"temp_token":"` + tempToken + `","session_token":"whatever","credential":{"id":"AA","type":"public-key","response":{}}}`
	w, data := doFactorLoginRequest(t, r, http.MethodPost, "/api/v1/auth/login/2fa/passkey/finish",
		body, "factor-login-agent")
	require.NotEqual(t, http.StatusOK, w.Code, w.Body.String())
	require.Empty(t, data.AccessToken)
}

// GET /user/2fa/policy：矩阵返回值供前端决定弹窗顺序。
func TestTwoFactorPolicyEndpointMatrix(t *testing.T) {
	cases := []struct {
		name     string
		totp     bool
		passkey  bool
		required []string
	}{
		{name: "none", required: []string{}},
		{name: "totp only", totp: true, required: []string{"totp"}},
		{name: "passkey only", passkey: true, required: []string{"passkey"}},
		{name: "both", totp: true, passkey: true, required: []string{"totp", "passkey"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			user, _, encryptor := newTwoFactorGrantTestUser(t, 21, "correct-password", tc.totp)
			repo := &twoFactorGrantUserRepo{user: user}
			cache := newTwoFactorGrantCacheStub()
			settingSvc := newTwoFactorGrantTestSettings(map[string]string{
				service.SettingKeyTotpEnabled:   "true",
				service.SettingKeyStepUpEnabled: "true",
			})
			totpSvc := service.NewTotpService(repo, encryptor, cache, settingSvc, nil, nil)
			if tc.passkey {
				totpSvc.SetPasskeyRepository(newFactorLoginTestPasskeyRepo(user.ID))
			}
			handler := NewTotpHandler(totpSvc)

			gin.SetMode(gin.TestMode)
			r := gin.New()
			r.GET("/api/v1/user/2fa/policy", func(c *gin.Context) {
				c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: user.ID})
				handler.GetPolicy(c)
			})
			req := httptest.NewRequest(http.MethodGet, "/api/v1/user/2fa/policy", nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())

			var envelope struct {
				Data struct {
					RequiredFactors []string `json:"required_factors"`
					TotpEnabled     bool     `json:"totp_enabled"`
					PasskeyBound    bool     `json:"passkey_bound"`
					StepUpEnabled   bool     `json:"step_up_enabled"`
				} `json:"data"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
			require.Equal(t, tc.required, envelope.Data.RequiredFactors)
			require.Equal(t, tc.totp, envelope.Data.TotpEnabled)
			require.Equal(t, tc.passkey, envelope.Data.PasskeyBound)
			require.True(t, envelope.Data.StepUpEnabled)
		})
	}
}
