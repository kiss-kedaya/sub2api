package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/require"
)

// loginEnumUserRepo 只认识已知账号，其它邮箱一律 not found：
// 用来把「账号不存在」「密码错误」「需要 2FA」三条路径放在同一张桌子上对比。
type loginEnumUserRepo struct {
	service.UserRepository
	user  *service.User
	users []*service.User
}

func (r *loginEnumUserRepo) knownUsers() []*service.User {
	if len(r.users) > 0 {
		return r.users
	}
	if r.user != nil {
		return []*service.User{r.user}
	}
	return nil
}

func (r *loginEnumUserRepo) GetByEmail(_ context.Context, email string) (*service.User, error) {
	for _, user := range r.knownUsers() {
		if strings.EqualFold(strings.TrimSpace(email), user.Email) {
			return user, nil
		}
	}
	return nil, service.ErrUserNotFound
}

func (r *loginEnumUserRepo) GetByID(_ context.Context, id int64) (*service.User, error) {
	for _, user := range r.knownUsers() {
		if user.ID == id {
			return user, nil
		}
	}
	return nil, service.ErrUserNotFound
}

func (r *loginEnumUserRepo) GetUserAvatar(context.Context, int64) (*service.UserAvatar, error) {
	return nil, nil
}

func (r *loginEnumUserRepo) Update(context.Context, *service.User, service.UserUpdateFields) error {
	return nil
}

func newLoginEnumHandler(t *testing.T, repo service.UserRepository, cache *twoFactorGrantCacheStub) *AuthHandler {
	t.Helper()
	cfg := newTwoFactorGrantTestConfig()
	settingSvc := newTwoFactorGrantTestSettings(map[string]string{service.SettingKeyTotpEnabled: "true"})
	authSvc := service.NewAuthService(nil, repo, nil, twoFactorGrantRefreshCache{}, cfg,
		settingSvc, nil, nil, nil, nil, nil, nil, nil)
	totpSvc := service.NewTotpService(repo, twoFactorGrantTestEncryptor{}, cache, settingSvc, nil, nil)
	userSvc := service.NewUserService(repo, nil, nil, nil)
	return NewAuthHandler(cfg, authSvc, userSvc, settingSvc, nil, nil, totpSvc, nil)
}

type loginEnumResponse struct {
	Code int `json:"code"`
	Data struct {
		Requires2FA     bool   `json:"requires_2fa"`
		TempToken       string `json:"temp_token"`
		UserEmailMasked string `json:"user_email_masked"`
		AccessToken     string `json:"access_token"`
	} `json:"data"`
}

func postLoginEnum(t *testing.T, router *gin.Engine, email, password string) (*httptest.ResponseRecorder, loginEnumResponse) {
	t.Helper()
	body, err := json.Marshal(map[string]string{"email": email, "password": password})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	var resp loginEnumResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp), w.Body.String())
	return w, resp
}

// 默认登录防枚举语义（login_shape_disguise_enabled 关闭）：
// 账号不存在 / 无 2FA 账号密码错误 / 已绑 2FA 账号密码错误 / 禁用账号密码错误，
// 一律返回逐字节一致的 401 INVALID_CREDENTIALS，不弹 2FA 验证框；
// 密码正确时，无 2FA 直接签发令牌，已绑 2FA 进入真实二次验证挑战。
func TestPasswordLoginChallengeIsByteIdenticalAcrossCredentials(t *testing.T) {
	gin.SetMode(gin.TestMode)
	totpUser, _, _ := newTwoFactorGrantTestUser(t, 7, "correct-password", true)
	no2FAUser, _, _ := newTwoFactorGrantTestUser(t, 8, "correct-password", false)
	no2FAUser.Email = "no2fa@example.test"
	disabledUser, _, _ := newTwoFactorGrantTestUser(t, 9, "correct-password", true)
	disabledUser.Email = "disabled@example.test"
	disabledUser.Status = service.StatusDisabled

	existingHandler := newLoginEnumHandler(t, &loginEnumUserRepo{users: []*service.User{totpUser, no2FAUser, disabledUser}}, newTwoFactorGrantCacheStub())
	unknownHandler := newLoginEnumHandler(t, &loginEnumUserRepo{}, newTwoFactorGrantCacheStub())

	existingRouter := gin.New()
	existingRouter.POST("/api/v1/auth/login", existingHandler.Login)
	unknownRouter := gin.New()
	unknownRouter.POST("/api/v1/auth/login", unknownHandler.Login)

	unknownW, unknownResp := postLoginEnum(t, unknownRouter, totpUser.Email, "correct-password")
	wrongTotpW, wrongTotpResp := postLoginEnum(t, existingRouter, totpUser.Email, "wrong-password")
	wrongNo2FAW, wrongNo2FAResp := postLoginEnum(t, existingRouter, no2FAUser.Email, "wrong-password")
	wrongDisabledW, wrongDisabledResp := postLoginEnum(t, existingRouter, disabledUser.Email, "wrong-password")

	for name, tc := range map[string]struct {
		w    *httptest.ResponseRecorder
		resp loginEnumResponse
	}{
		"unknown-account":         {w: unknownW, resp: unknownResp},
		"totp-wrong-password":     {w: wrongTotpW, resp: wrongTotpResp},
		"no-2fa-wrong-password":   {w: wrongNo2FAW, resp: wrongNo2FAResp},
		"disabled-wrong-password": {w: wrongDisabledW, resp: wrongDisabledResp},
	} {
		require.Equal(t, http.StatusUnauthorized, tc.w.Code, "%s: %s", name, tc.w.Body.String())
		require.Equal(t, "no-store", tc.w.Header().Get("Cache-Control"), name)
		require.Equal(t, "application/json; charset=utf-8", tc.w.Header().Get("Content-Type"), name)
		require.Empty(t, tc.w.Result().Cookies(), "%s: 凭据错误不得下发或清理 Cookie", name)
		require.False(t, tc.resp.Data.Requires2FA, "%s: 凭据错误不得弹 2FA 验证框", name)
		require.Empty(t, tc.resp.Data.TempToken, name)
		require.Empty(t, tc.resp.Data.AccessToken, name)
		require.Contains(t, tc.w.Body.String(), `"reason":"INVALID_CREDENTIALS"`, name)
	}

	// 账号不存在与各类密码错误的响应体必须逐字节一致，不能被用来探测账号是否存在。
	require.Equal(t, unknownW.Body.String(), wrongTotpW.Body.String(), "已绑 2FA 密码错误不得与账号不存在有字节差异")
	require.Equal(t, unknownW.Body.String(), wrongNo2FAW.Body.String(), "无 2FA 密码错误不得与账号不存在有字节差异")
	require.Equal(t, unknownW.Body.String(), wrongDisabledW.Body.String(), "禁用账号密码错误不得与账号不存在有字节差异")

	// 无 2FA 账号输入正确密码直接签发令牌，不弹 2FA 验证框。
	no2FAOkW, no2FAOkResp := postLoginEnum(t, existingRouter, no2FAUser.Email, "correct-password")
	require.Equal(t, http.StatusOK, no2FAOkW.Code, no2FAOkW.Body.String())
	require.False(t, no2FAOkResp.Data.Requires2FA)
	require.NotEmpty(t, no2FAOkResp.Data.AccessToken)

	// 真实 2FA 账号输入正确密码进入 2FA 挑战，输入正确动态码完成登录。
	needs2FAW, needs2FAResp := postLoginEnum(t, existingRouter, totpUser.Email, "correct-password")
	require.Equal(t, http.StatusOK, needs2FAW.Code, needs2FAW.Body.String())
	require.Equal(t, "no-store", needs2FAW.Header().Get("Cache-Control"))
	require.Equal(t, 0, needs2FAResp.Code)
	require.True(t, needs2FAResp.Data.Requires2FA)
	require.NotEmpty(t, needs2FAResp.Data.TempToken)
	require.Empty(t, needs2FAResp.Data.AccessToken)
	require.Equal(t, service.MaskEmail(totpUser.Email), needs2FAResp.Data.UserEmailMasked)

	post2FA := func(router *gin.Engine, tempToken, email, password, code string) *httptest.ResponseRecorder {
		payload, err := json.Marshal(map[string]string{
			"temp_token": tempToken,
			"totp_code":  code,
			"email":      email,
			"password":   password,
		})
		require.NoError(t, err)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login/2fa", strings.NewReader(string(payload)))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}

	existingRouter2FA := gin.New()
	existingRouter2FA.POST("/api/v1/auth/login/2fa", existingHandler.Login2FA)
	secret := *totpUser.TotpSecretEncrypted
	code, err := totp.GenerateCode(secret, time.Now())
	require.NoError(t, err)
	okW := post2FA(existingRouter2FA, needs2FAResp.Data.TempToken, totpUser.Email, "correct-password", code)
	require.Equal(t, http.StatusOK, okW.Code, okW.Body.String())
	require.Contains(t, okW.Body.String(), "access_token")
}

// 第二步的失败响应统一返回 INVALID_CREDENTIALS：真实 2FA 账号在第二步提交错误动态码或
// 错误复核密码必须返回逐字节一致的 INVALID_CREDENTIALS，不返回 TOTP_INVALID_CODE。
func TestLogin2FASecondStepDoesNotLeakPasswordValidity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	user, secret, _ := newTwoFactorGrantTestUser(t, 7, "correct-password", true)
	handler := newLoginEnumHandler(t, &loginEnumUserRepo{user: user}, newTwoFactorGrantCacheStub())

	router := gin.New()
	router.POST("/api/v1/auth/login", handler.Login)
	router.POST("/api/v1/auth/login/2fa", handler.Login2FA)

	// 与当前时间窗相隔 2 小时的合法格式动态码：保证判为「码错误」而不抖动。
	wrongCode, err := totp.GenerateCode(secret, time.Now().Add(-2*time.Hour))
	require.NoError(t, err)

	post2FA := func(tempToken, email, password, code string) (*httptest.ResponseRecorder, string) {
		payload, err := json.Marshal(map[string]string{
			"temp_token": tempToken,
			"totp_code":  code,
			"email":      email,
			"password":   password,
		})
		require.NoError(t, err)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login/2fa", strings.NewReader(string(payload)))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		var body struct {
			Code   int    `json:"code"`
			Reason string `json:"reason"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body), w.Body.String())
		return w, body.Reason
	}

	// 默认模式（disguise 关闭）：第一步密码错误直接 401 INVALID_CREDENTIALS，不发 2FA 挑战。
	badW, badResp := postLoginEnum(t, router, user.Email, "wrong-password")
	require.Equal(t, http.StatusUnauthorized, badW.Code, badW.Body.String())
	require.False(t, badResp.Data.Requires2FA)
	require.Contains(t, badW.Body.String(), `"reason":"INVALID_CREDENTIALS"`)

	// 密码正确 -> 真 2FA 挑战；第二步「密码正确、动态码错误」与「密码被改错」返回逐字节一致的 INVALID_CREDENTIALS。
	_, okResp := postLoginEnum(t, router, user.Email, "correct-password")
	require.True(t, okResp.Data.Requires2FA)
	wrongCodeW, wrongCodeReason := post2FA(okResp.Data.TempToken, user.Email, "correct-password", wrongCode)
	require.Equal(t, http.StatusUnauthorized, wrongCodeW.Code, wrongCodeW.Body.String())
	require.Equal(t, "INVALID_CREDENTIALS", wrongCodeReason)
	require.NotContains(t, wrongCodeW.Body.String(), "TOTP_INVALID_CODE")

	wrongPassW, wrongPassReason := post2FA(okResp.Data.TempToken, user.Email, "wrong-password", wrongCode)
	require.Equal(t, http.StatusUnauthorized, wrongPassW.Code, wrongPassW.Body.String())
	require.Equal(t, "INVALID_CREDENTIALS", wrongPassReason)

	require.Equal(t, wrongPassW.Body.String(), wrongCodeW.Body.String())
}

// 第二步的密码复核固定用 temp_token 会话里的邮箱：请求里换邮箱直接按统一错误
// 拒绝，不能持自己的会话替任意邮箱做密码校验（绕过邮箱维度限速）。
func TestLogin2FASecondStepRejectsEmailMismatch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	alice, aliceSecret, _ := newTwoFactorGrantTestUser(t, 7, "alice-password", true)
	bob, _, _ := newTwoFactorGrantTestUser(t, 8, "bob-password", true)
	handler := newLoginEnumHandler(t, &loginEnumUserRepo{users: []*service.User{alice, bob}}, newTwoFactorGrantCacheStub())

	router := gin.New()
	router.POST("/api/v1/auth/login", handler.Login)
	router.POST("/api/v1/auth/login/2fa", handler.Login2FA)

	// 用 alice 自己的凭据拿到真 2FA 挑战。
	_, aliceChallenge := postLoginEnum(t, router, alice.Email, "alice-password")
	require.True(t, aliceChallenge.Data.Requires2FA)

	// 与当前时间窗相隔 2 小时的动态码，保证不是「码错误」掩盖了邮箱不一致。
	staleCode, err := totp.GenerateCode(aliceSecret, time.Now().Add(-2*time.Hour))
	require.NoError(t, err)

	payload, err := json.Marshal(map[string]string{
		"temp_token": aliceChallenge.Data.TempToken,
		"totp_code":  staleCode,
		// 请求里换成 bob 的邮箱+bob 的正确密码：旧实现会先拿 bob 的邮箱去校验
		// 密码，从而把「bob 的密码是否正确」暴露成可区分的错误码。
		"email":    bob.Email,
		"password": "bob-password",
	})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login/2fa", strings.NewReader(string(payload)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusUnauthorized, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "INVALID_CREDENTIALS")
	require.NotContains(t, w.Body.String(), "access_token")
	require.NotContains(t, w.Body.String(), "TOTP_INVALID_CODE")
}
