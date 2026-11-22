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

// 登录响应统一：账号不存在 / 密码错误 / 密码正确但需要 2FA，三种情况必须返回
// 逐字节一致的挑战（只有随机 temp_token 的值不同），不能成为账号枚举和密码
// 有效性探测的 oracle。
func TestPasswordLoginChallengeIsByteIdenticalAcrossCredentials(t *testing.T) {
	gin.SetMode(gin.TestMode)
	user, _, _ := newTwoFactorGrantTestUser(t, 7, "correct-password", true)
	existingHandler := newLoginEnumHandler(t, &loginEnumUserRepo{user: user}, newTwoFactorGrantCacheStub())
	unknownHandler := newLoginEnumHandler(t, &loginEnumUserRepo{}, newTwoFactorGrantCacheStub())

	existingRouter := gin.New()
	existingRouter.POST("/api/v1/auth/login", existingHandler.Login)
	unknownRouter := gin.New()
	unknownRouter.POST("/api/v1/auth/login", unknownHandler.Login)

	// 三条路径用同一个邮箱输入，保证邮箱掩码也一致。
	unknownW, unknownResp := postLoginEnum(t, unknownRouter, user.Email, "correct-password")
	wrongPasswordW, wrongPasswordResp := postLoginEnum(t, existingRouter, user.Email, "wrong-password")
	needs2FAW, needs2FAResp := postLoginEnum(t, existingRouter, user.Email, "correct-password")

	for name, w := range map[string]*httptest.ResponseRecorder{
		"unknown-account": unknownW,
		"wrong-password":  wrongPasswordW,
		"needs-2fa":       needs2FAW,
	} {
		require.Equal(t, http.StatusOK, w.Code, "%s: %s", name, w.Body.String())
		require.Equal(t, "no-store", w.Header().Get("Cache-Control"), name)
		require.Equal(t, "application/json; charset=utf-8", w.Header().Get("Content-Type"), name)
	}
	for name, resp := range map[string]loginEnumResponse{
		"unknown-account": unknownResp,
		"wrong-password":  wrongPasswordResp,
		"needs-2fa":       needs2FAResp,
	} {
		require.Equal(t, 0, resp.Code, name)
		require.True(t, resp.Data.Requires2FA, "%s: 必须下发统一的二次验证挑战", name)
		require.NotEmpty(t, resp.Data.TempToken, name)
		require.Empty(t, resp.Data.AccessToken, "%s: 挑战阶段不得签发会话", name)
		require.Equal(t, service.MaskEmail(user.Email), resp.Data.UserEmailMasked, name)
	}

	// temp_token 是唯一的随机部分：等长、同构，替换后响应体必须逐字节一致。
	require.Equal(t, len(unknownResp.Data.TempToken), len(wrongPasswordResp.Data.TempToken))
	require.Equal(t, len(unknownResp.Data.TempToken), len(needs2FAResp.Data.TempToken))

	normalize := func(w *httptest.ResponseRecorder, token string) string {
		require.NotEmpty(t, token)
		return strings.ReplaceAll(w.Body.String(), token, "«temp_token»")
	}
	unknownBody := normalize(unknownW, unknownResp.Data.TempToken)
	require.Equal(t, unknownBody, normalize(wrongPasswordW, wrongPasswordResp.Data.TempToken), "密码错误不得与账号不存在有字节差异")
	require.Equal(t, unknownBody, normalize(needs2FAW, needs2FAResp.Data.TempToken), "需要 2FA 不得与账号不存在有字节差异")

	// 第二步：第一步凭据无效的挑战统一返回 INVALID_CREDENTIALS（不进入动态码校验），
	// 不泄露账号是否存在。
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

	for name, tc := range map[string]struct {
		handler  *AuthHandler
		temp     string
		password string
	}{
		"unknown-account": {handler: unknownHandler, temp: unknownResp.Data.TempToken, password: "correct-password"},
		"wrong-password":  {handler: existingHandler, temp: wrongPasswordResp.Data.TempToken, password: "wrong-password"},
	} {
		router2FA := gin.New()
		router2FA.POST("/api/v1/auth/login/2fa", tc.handler.Login2FA)
		w := post2FA(router2FA, tc.temp, user.Email, tc.password, "123456")
		require.Equal(t, http.StatusUnauthorized, w.Code, "%s: %s", name, w.Body.String())
		var body struct {
			Code   int    `json:"code"`
			Reason string `json:"reason"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body), w.Body.String())
		require.Equal(t, "INVALID_CREDENTIALS", body.Reason, "%s: 第二步凭据错误必须统一返回 INVALID_CREDENTIALS", name)
	}

	// 真实 2FA 账号：正确的密码 + 正确的动态码仍然能完成登录（新协议第二步
	// 同时提交邮箱+密码+动态码），证明统一挑战没有把正常流程改死。
	existingRouter2FA := gin.New()
	existingRouter2FA.POST("/api/v1/auth/login/2fa", existingHandler.Login2FA)
	secret := *user.TotpSecretEncrypted
	code, err := totp.GenerateCode(secret, time.Now())
	require.NoError(t, err)
	okW := post2FA(existingRouter2FA, needs2FAResp.Data.TempToken, user.Email, "correct-password", code)
	require.Equal(t, http.StatusOK, okW.Code, okW.Body.String())
	require.Contains(t, okW.Body.String(), "access_token")
}

// 第二步的失败响应不得泄露密码是否正确：真实 2FA 账号「密码正确、动态码错误」
// 与「密码错误」必须返回同一个错误码（INVALID_CREDENTIALS），否则第二步又变成
// 密码有效性 oracle。
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

	// 密码正确 -> 真 2FA 挑战；第二步密码正确、动态码错误。
	_, okResp := postLoginEnum(t, router, user.Email, "correct-password")
	require.True(t, okResp.Data.Requires2FA)
	wrongCodeW, wrongCodeReason := post2FA(okResp.Data.TempToken, user.Email, "correct-password", wrongCode)
	require.Equal(t, http.StatusUnauthorized, wrongCodeW.Code, wrongCodeW.Body.String())
	require.Equal(t, "INVALID_CREDENTIALS", wrongCodeReason)
	require.NotContains(t, wrongCodeW.Body.String(), "TOTP_INVALID_CODE")

	// 密码错误 -> 统一挑战；第二步密码仍然错误。
	_, badResp := postLoginEnum(t, router, user.Email, "wrong-password")
	require.True(t, badResp.Data.Requires2FA)
	wrongPassW, wrongPassReason := post2FA(badResp.Data.TempToken, user.Email, "wrong-password", wrongCode)
	require.Equal(t, http.StatusUnauthorized, wrongPassW.Code, wrongPassW.Body.String())
	require.Equal(t, "INVALID_CREDENTIALS", wrongPassReason)

	// 两种失败对客户端完全不可区分：响应体逐字节一致。
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
