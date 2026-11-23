package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type passkeySwitchSettingRepo struct {
	value  string
	values map[string]string
	err    error
}

type passkeyCaptchaVerifierStub struct {
	calls int
	proof service.TencentCaptchaProof
}

func (s *passkeyCaptchaVerifierStub) VerifyTicket(_ context.Context, _ service.TencentCaptchaCredentials, proof service.TencentCaptchaProof, _ string) (*service.TencentCaptchaVerifyResponse, error) {
	s.calls++
	s.proof = proof
	return &service.TencentCaptchaVerifyResponse{CaptchaCode: 1}, nil
}

type passkeyBeginSessionStoreStub struct {
	service.PasskeySessionStore
	storeCalls int
}

func (s *passkeyBeginSessionStoreStub) Store(context.Context, *service.PasskeySession, time.Duration) (string, error) {
	s.storeCalls++
	return "passkey-session", nil
}

func (r *passkeySwitchSettingRepo) Get(context.Context, string) (*service.Setting, error) {
	return nil, service.ErrSettingNotFound
}
func (r *passkeySwitchSettingRepo) GetValue(context.Context, string) (string, error) {
	return r.value, r.err
}
func (r *passkeySwitchSettingRepo) Set(context.Context, string, string) error { return nil }
func (r *passkeySwitchSettingRepo) GetMultiple(context.Context, []string) (map[string]string, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.values, nil
}
func (r *passkeySwitchSettingRepo) SetMultiple(context.Context, map[string]string) error {
	return nil
}
func (r *passkeySwitchSettingRepo) GetAll(context.Context) (map[string]string, error) {
	return map[string]string{}, nil
}
func (r *passkeySwitchSettingRepo) Delete(context.Context, string) error { return nil }

func TestBindPasskeyFinishRequestRejectsOversizedBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/passkey/login/finish",
		strings.NewReader(`{"credential":"`+strings.Repeat("x", passkeyFinishBodyMaxBytes)+`"}`),
	)
	context.Request.Header.Set("Content-Type", "application/json")

	_, ok := bindPasskeyFinishRequest(context)
	require.False(t, ok)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestPasskeyBeginLoginRejectsDisabledAdminSwitch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &passkeySwitchSettingRepo{value: "false"}
	settings := service.NewSettingService(repo, &config.Config{
		WebAuthn: config.WebAuthnConfig{Enabled: true},
	})
	handler := NewPasskeyHandler(nil, nil, settings)
	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	ginContext.Request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/passkey/login/begin", nil)

	handler.BeginLogin(ginContext)

	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.Contains(t, recorder.Body.String(), "PASSKEY_DISABLED")
}

func TestPasskeyBeginLoginReportsSettingStoreFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	settings := service.NewSettingService(
		&passkeySwitchSettingRepo{err: errors.New("database unavailable")},
		&config.Config{WebAuthn: config.WebAuthnConfig{Enabled: true}},
	)
	handler := NewPasskeyHandler(nil, nil, settings)
	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	ginContext.Request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/passkey/login/begin", nil)

	handler.BeginLogin(ginContext)

	require.Equal(t, http.StatusInternalServerError, recorder.Code)
	require.NotContains(t, recorder.Body.String(), "PASSKEY_DISABLED")
}

func newTencentProtectedPasskeyHandler(t *testing.T) (*PasskeyHandler, *passkeyCaptchaVerifierStub, *passkeyBeginSessionStoreStub) {
	t.Helper()
	cfg := &config.Config{WebAuthn: config.WebAuthnConfig{
		Enabled:       true,
		RPDisplayName: "Sub2API",
		RPID:          "sub2api.example.com",
		RPOrigins:     []string{"https://sub2api.example.com"},
	}}
	repo := &passkeySwitchSettingRepo{
		value: "true",
		values: map[string]string{
			service.SettingKeyTencentCaptchaEnabled:        "true",
			service.SettingKeyTencentCaptchaAppID:          "123456789",
			service.SettingKeyTencentCaptchaAppSecretKey:   "app-secret",
			service.SettingKeyTencentCaptchaCloudSecretID:  "cloud-secret-id",
			service.SettingKeyTencentCaptchaCloudSecretKey: "cloud-secret-key",
		},
	}
	settings := service.NewSettingService(repo, cfg)
	verifier := &passkeyCaptchaVerifierStub{}
	authService := service.NewAuthService(nil, nil, nil, nil, cfg, settings, nil, nil, nil, nil, nil, nil, nil)
	authService.SetTencentCaptchaService(service.NewTencentCaptchaService(settings, verifier))
	sessions := &passkeyBeginSessionStoreStub{}
	passkeys, err := service.NewPasskeyService(cfg, nil, sessions, nil)
	require.NoError(t, err)
	return NewPasskeyHandler(passkeys, authService, settings), verifier, sessions
}

func newPasskeyBeginLoginContext(body string) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	ginContext.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/passkey/login/begin",
		strings.NewReader(body),
	)
	ginContext.Request.Header.Set("Content-Type", "application/json")
	return ginContext, recorder
}

func TestPasskeyBeginLoginRejectsMissingTencentCaptchaProof(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, verifier, sessions := newTencentProtectedPasskeyHandler(t)
	ginContext, recorder := newPasskeyBeginLoginContext(`{}`)

	handler.BeginLogin(ginContext)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "TENCENT_CAPTCHA_VERIFICATION_FAILED")
	require.Zero(t, verifier.calls)
	require.Zero(t, sessions.storeCalls)
}

func TestPasskeyBeginLoginAcceptsTencentCaptchaProofBeforeCeremony(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, verifier, sessions := newTencentProtectedPasskeyHandler(t)
	ginContext, recorder := newPasskeyBeginLoginContext(
		`{"tencent_captcha_ticket":"ticket-value","tencent_captcha_randstr":"@rand-value"}`,
	)

	handler.BeginLogin(ginContext)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, 1, verifier.calls)
	require.Equal(t, service.TencentCaptchaProof{Ticket: "ticket-value", Randstr: "@rand-value"}, verifier.proof)
	require.Equal(t, 1, sessions.storeCalls)
}

func TestPasskeyCredentialListRemainsAvailableWhenSignInDisabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewPasskeyHandler(nil, nil, nil)
	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	ginContext.Request = httptest.NewRequest(http.MethodGet, "/api/v1/user/passkeys", nil)

	handler.List(ginContext)

	require.Equal(t, http.StatusUnauthorized, recorder.Code)
	require.NotContains(t, recorder.Body.String(), "PASSKEY_DISABLED")
}

// ---------- Passkey 创建/删除的 2FA step-up 门禁 ----------

type passkeyGateRepoStub struct {
	service.PasskeyRepository
	handle       []byte
	handleCalled bool
	deleteCalled bool
}

func (s *passkeyGateRepoStub) EnsureUserHandle(_ context.Context, _ int64, candidate []byte) ([]byte, error) {
	s.handleCalled = true
	s.handle = append([]byte(nil), candidate...)
	return s.handle, nil
}

func (s *passkeyGateRepoStub) ListByUserID(context.Context, int64) ([]service.PasskeyCredentialRecord, error) {
	return nil, nil
}

func (s *passkeyGateRepoStub) Delete(context.Context, int64, int64) error {
	s.deleteCalled = true
	return nil
}

type passkeyGateFixture struct {
	handler *PasskeyHandler
	user    *service.User
	repo    *passkeyGateRepoStub
	totp    *service.TotpService
}

// newPasskeyGateFixture 复用 2FA grant 测试的用户/缓存替身，构造带 step-up 门禁
// 的 PasskeyHandler。totpEnabled 控制账号是否开启 TOTP。
func newPasskeyGateFixture(t *testing.T, totpEnabled bool) *passkeyGateFixture {
	t.Helper()
	user, _, encryptor := newTwoFactorGrantTestUser(t, 7, "correct-password", totpEnabled)
	cfg := &config.Config{WebAuthn: config.WebAuthnConfig{
		Enabled:       true,
		RPDisplayName: "Sub2API",
		RPID:          "sub2api.example.com",
		RPOrigins:     []string{"https://sub2api.example.com"},
	}}
	settings := service.NewSettingService(&passkeySwitchSettingRepo{value: "true"}, cfg)
	userRepo := &twoFactorGrantUserRepo{user: user}
	cache := newTwoFactorGrantCacheStub()
	totpSvc := service.NewTotpService(userRepo, encryptor, cache, settings, nil, nil)
	userSvc := service.NewUserService(userRepo, nil, nil, nil)
	repo := &passkeyGateRepoStub{}
	passkeys, err := service.NewPasskeyService(cfg, repo, &passkeyBeginSessionStoreStub{}, userRepo)
	require.NoError(t, err)

	handler := NewPasskeyHandler(passkeys, nil, settings)
	handler.SetStepUpDependencies(totpSvc, userSvc)
	return &passkeyGateFixture{handler: handler, user: user, repo: repo, totp: totpSvc}
}

func newPasskeyAuthenticatedContext(
	userID int64,
	method, target, body string,
	params gin.Params,
) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	ginContext.Request = httptest.NewRequest(method, target, strings.NewReader(body))
	ginContext.Request.Header.Set("Content-Type", "application/json")
	ginContext.Request.Header.Set("User-Agent", "passkey-stepup-test")
	ginContext.Request.RemoteAddr = "192.0.2.7:4321"
	ginContext.Params = params
	ginContext.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: userID})
	return ginContext, recorder
}

// issueStepUpGrant 模拟前端全局 TOTP 对话框已通过 /user/totp/step-up 拿到 grant Cookie。
func (f *passkeyGateFixture) issueStepUpGrant(t *testing.T, ginContext *gin.Context) {
	t.Helper()
	binding := service.TwoFactorGrantBinding{
		IP:        ip.GetClientIP(ginContext),
		UserAgent: ginContext.GetHeader("User-Agent"),
	}
	grant, err := f.totp.IssueTwoFactorGrant(
		ginContext.Request.Context(), f.user.ID, service.TwoFactorGrantScopePassword, service.FactorTOTP, binding)
	require.NoError(t, err)
	require.NotEmpty(t, grant.Token)
	ginContext.Request.AddCookie(&http.Cookie{Name: service.TwoFactorGrantCookieName, Value: grant.Token})
}

func TestPasskeyBeginRegistrationRequiresStepUpWhenTOTPEnabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fixture := newPasskeyGateFixture(t, true)
	ginContext, recorder := newPasskeyAuthenticatedContext(
		fixture.user.ID, http.MethodPost, "/api/v1/user/passkeys/register/begin",
		`{"password":"correct-password"}`, nil)

	fixture.handler.BeginRegistration(ginContext)

	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.Contains(t, recorder.Body.String(), "STEP_UP_REQUIRED")
	require.False(t, fixture.repo.handleCalled, "step-up 未通过时不得签发 challenge")
}

func TestPasskeyBeginRegistrationAcceptsFreshStepUpGrant(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fixture := newPasskeyGateFixture(t, true)
	ginContext, recorder := newPasskeyAuthenticatedContext(
		fixture.user.ID, http.MethodPost, "/api/v1/user/passkeys/register/begin",
		`{"password":"correct-password"}`, nil)
	fixture.issueStepUpGrant(t, ginContext)

	fixture.handler.BeginRegistration(ginContext)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), "session_token")
	require.True(t, fixture.repo.handleCalled)
}

func TestPasskeyBeginRegistrationSkipsStepUpWhenTOTPDisabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fixture := newPasskeyGateFixture(t, false)
	ginContext, recorder := newPasskeyAuthenticatedContext(
		fixture.user.ID, http.MethodPost, "/api/v1/user/passkeys/register/begin",
		`{"password":"correct-password"}`, nil)

	fixture.handler.BeginRegistration(ginContext)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), "session_token")
	require.True(t, fixture.repo.handleCalled)
}

func TestPasskeyDeleteRequiresStepUpWhenTOTPEnabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fixture := newPasskeyGateFixture(t, true)
	ginContext, recorder := newPasskeyAuthenticatedContext(
		fixture.user.ID, http.MethodDelete, "/api/v1/user/passkeys/3",
		`{"password":"correct-password"}`, gin.Params{{Key: "id", Value: "3"}})

	fixture.handler.Delete(ginContext)

	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.Contains(t, recorder.Body.String(), "STEP_UP_REQUIRED")
	require.False(t, fixture.repo.deleteCalled)
}

func TestPasskeyDeleteAcceptsFreshStepUpGrant(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fixture := newPasskeyGateFixture(t, true)
	ginContext, recorder := newPasskeyAuthenticatedContext(
		fixture.user.ID, http.MethodDelete, "/api/v1/user/passkeys/3",
		`{"password":"correct-password"}`, gin.Params{{Key: "id", Value: "3"}})
	fixture.issueStepUpGrant(t, ginContext)

	fixture.handler.Delete(ginContext)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.True(t, fixture.repo.deleteCalled)
}

func TestPasskeyDeleteSkipsStepUpWhenTOTPDisabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fixture := newPasskeyGateFixture(t, false)
	ginContext, recorder := newPasskeyAuthenticatedContext(
		fixture.user.ID, http.MethodDelete, "/api/v1/user/passkeys/3",
		`{"password":"correct-password"}`, gin.Params{{Key: "id", Value: "3"}})

	fixture.handler.Delete(ginContext)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.True(t, fixture.repo.deleteCalled)
}
