package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/require"
)

// twoFactorGrantCacheStub 同时实现 TotpCache 与 TwoFactorGrantStore，
// 供登录 / step-up / 密码校验三处入口的凭证契约测试使用。
type twoFactorGrantCacheStub struct {
	service.TotpCache

	mu            sync.Mutex
	records       map[string]*service.TwoFactorGrantRecord
	loginSessions map[string]*service.TotpLoginSession
	attempts      map[string]int
	failLoad      bool
	failSave      bool
}

func newTwoFactorGrantCacheStub() *twoFactorGrantCacheStub {
	return &twoFactorGrantCacheStub{
		records:       map[string]*service.TwoFactorGrantRecord{},
		loginSessions: map[string]*service.TotpLoginSession{},
		attempts:      map[string]int{},
	}
}

func (c *twoFactorGrantCacheStub) SaveTwoFactorGrant(_ context.Context, digest string, record *service.TwoFactorGrantRecord, _ time.Duration) error {
	if c.failSave {
		return context.DeadlineExceeded
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	copied := *record
	c.records[digest] = &copied
	return nil
}

func (c *twoFactorGrantCacheStub) LoadTwoFactorGrant(_ context.Context, digest string) (*service.TwoFactorGrantRecord, error) {
	if c.failLoad {
		return nil, context.DeadlineExceeded
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	record, ok := c.records[digest]
	if !ok {
		return nil, nil
	}
	copied := *record
	return &copied, nil
}

func (c *twoFactorGrantCacheStub) DeleteTwoFactorGrant(_ context.Context, digest string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.records, digest)
	return nil
}

func (c *twoFactorGrantCacheStub) RevokeTwoFactorGrantsForUser(_ context.Context, userID int64) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var revoked int64
	for digest, record := range c.records {
		if record.UserID == userID {
			delete(c.records, digest)
			revoked++
		}
	}
	return revoked, nil
}

func (c *twoFactorGrantCacheStub) IncrementTwoFactorGrantVerifyAttempt(_ context.Context, userID int64, ipHash string, _ time.Duration) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := fmt.Sprintf("%d:%s", userID, ipHash)
	c.attempts[key]++
	return int64(c.attempts[key]), nil
}

func (c *twoFactorGrantCacheStub) GetLoginSession(_ context.Context, tempToken string) (*service.TotpLoginSession, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.loginSessions[tempToken], nil
}

func (c *twoFactorGrantCacheStub) SetLoginSession(_ context.Context, tempToken string, session *service.TotpLoginSession, _ time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.loginSessions[tempToken] = session
	return nil
}

func (c *twoFactorGrantCacheStub) DeleteLoginSession(_ context.Context, tempToken string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.loginSessions, tempToken)
	return nil
}

func (c *twoFactorGrantCacheStub) GetVerifyAttempts(context.Context, int64) (int, error) {
	return 0, nil
}
func (c *twoFactorGrantCacheStub) IncrementVerifyAttempts(context.Context, int64) (int, error) {
	return 1, nil
}
func (c *twoFactorGrantCacheStub) ClearVerifyAttempts(context.Context, int64) error { return nil }
func (c *twoFactorGrantCacheStub) SetStepUpGrant(context.Context, int64, string, time.Duration) error {
	return nil
}
func (c *twoFactorGrantCacheStub) MarkLoginCodeUsed(context.Context, int64, string, time.Duration) (bool, error) {
	return true, nil
}

// twoFactorGrantUserRepo 提供登录 / 密码校验所需的最小用户读写能力。
type twoFactorGrantUserRepo struct {
	service.UserRepository
	user *service.User
}

func (r *twoFactorGrantUserRepo) GetByEmail(context.Context, string) (*service.User, error) {
	return r.user, nil
}

func (r *twoFactorGrantUserRepo) GetByID(context.Context, int64) (*service.User, error) {
	return r.user, nil
}

func (r *twoFactorGrantUserRepo) GetUserAvatar(context.Context, int64) (*service.UserAvatar, error) {
	return nil, nil
}

func (r *twoFactorGrantUserRepo) Update(_ context.Context, _ *service.User, _ service.UserUpdateFields) error {
	return nil
}

// twoFactorGrantRefreshCache 让 GenerateTokenPair 在测试里可用。
type twoFactorGrantRefreshCache struct {
	service.RefreshTokenCache
}

func (twoFactorGrantRefreshCache) StoreRefreshToken(context.Context, string, *service.RefreshTokenData, time.Duration) error {
	return nil
}

func (twoFactorGrantRefreshCache) AddToUserTokenSet(context.Context, int64, string, time.Duration) error {
	return nil
}

func (twoFactorGrantRefreshCache) AddToFamilyTokenSet(context.Context, string, string, time.Duration) error {
	return nil
}

func newTwoFactorGrantTestConfig() *config.Config {
	cfg := &config.Config{}
	cfg.JWT.Secret = "two-factor-grant-test-secret-at-least-32-bytes"
	cfg.JWT.AccessTokenExpireMinutes = 60
	cfg.JWT.RefreshTokenExpireDays = 7
	return cfg
}

// twoFactorGrantTestEncryptor 让测试里的 TOTP 种子以明文形式存取（仅测试用）。
type twoFactorGrantTestEncryptor struct{}

func (twoFactorGrantTestEncryptor) Encrypt(plaintext string) (string, error) { return plaintext, nil }
func (twoFactorGrantTestEncryptor) Decrypt(ciphertext string) (string, error) {
	return ciphertext, nil
}

func newTwoFactorGrantTestUser(t *testing.T, id int64, password string, totpEnabled bool) (*service.User, string, service.SecretEncryptor) {
	t.Helper()
	user := &service.User{
		ID:          id,
		Email:       fmt.Sprintf("user-%d@example.test", id),
		Status:      service.StatusActive,
		Role:        service.RoleUser,
		TotpEnabled: totpEnabled,
	}
	require.NoError(t, user.SetPassword(password))
	secret := ""
	if totpEnabled {
		key, err := totp.Generate(totp.GenerateOpts{Issuer: "Sub2API-Test", AccountName: user.Email})
		require.NoError(t, err)
		secret = key.Secret()
		user.TotpSecretEncrypted = &secret
	}
	return user, secret, twoFactorGrantTestEncryptor{}
}

func newTwoFactorGrantTestSettings(values map[string]string) *service.SettingService {
	return service.NewSettingService(&loginTOTPSettingRepo{values: values}, nil)
}

func newTwoFactorGrantAuthHandler(
	t *testing.T,
	cfg *config.Config,
	userRepo *twoFactorGrantUserRepo,
	cache *twoFactorGrantCacheStub,
	user *service.User,
	encryptor service.SecretEncryptor,
) (*AuthHandler, *service.TotpService, *service.AuthService) {
	t.Helper()
	settingSvc := newTwoFactorGrantTestSettings(map[string]string{service.SettingKeyTotpEnabled: "true"})
	authSvc := service.NewAuthService(nil, userRepo, nil, twoFactorGrantRefreshCache{}, cfg,
		settingSvc, nil, nil, nil, nil, nil, nil, nil)
	totpSvc := service.NewTotpService(userRepo, encryptor, cache, settingSvc, nil, nil)
	userSvc := service.NewUserService(userRepo, nil, nil, nil)
	h := NewAuthHandler(cfg, authSvc, userSvc, settingSvc, nil, nil, totpSvc, nil)
	return h, totpSvc, authSvc
}

func twoFactorGrantCookieFromResponse(t *testing.T, resp *http.Response) string {
	t.Helper()
	for _, cookie := range resp.Cookies() {
		if cookie.Name == service.TwoFactorGrantCookieName && cookie.Value != "" && cookie.MaxAge >= 0 {
			return cookie.Value
		}
	}
	return ""
}

func loginRequestBody(email, password string) string {
	payload, _ := json.Marshal(map[string]string{"email": email, "password": password})
	return string(payload)
}

// 登录只输密码 + 有效凭证 → 跳过 TOTP 直接签发会话；Cookie 不进入响应体，
// 响应头带 no-store，审计动作为 grant_used。
func TestLoginSkipsTOTPWithValidTwoFactorGrant(t *testing.T) {
	gin.SetMode(gin.TestMode)
	user, _, encryptor := newTwoFactorGrantTestUser(t, 7, "correct-password", true)
	repo := &twoFactorGrantUserRepo{user: user}
	cache := newTwoFactorGrantCacheStub()
	h, totpSvc, _ := newTwoFactorGrantAuthHandler(t, newTwoFactorGrantTestConfig(), repo, cache, user, encryptor)

	// 用与请求一致的绑定材料签发凭证（IP 来自 httptest 默认 RemoteAddr）。
	binding := service.TwoFactorGrantBinding{IP: "192.0.2.1", UserAgent: "grant-test-agent"}
	grant, err := totpSvc.IssueTwoFactorGrant(context.Background(), user.ID, service.TwoFactorGrantScopeLogin, binding)
	require.NoError(t, err)

	var auditAction string
	var auditExtra map[string]any
	r := gin.New()
	r.POST("/api/v1/auth/login", func(c *gin.Context) {
		h.Login(c)
		auditAction = c.GetString("audit_action")
		if value, ok := c.Get("audit_extra"); ok {
			auditExtra, _ = value.(map[string]any)
		}
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(loginRequestBody(user.Email, "correct-password")))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "grant-test-agent")
	req.AddCookie(&http.Cookie{Name: service.TwoFactorGrantCookieName, Value: grant.Token})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var response struct {
		Code int `json:"code"`
		Data struct {
			AccessToken string `json:"access_token"`
			Requires2FA bool   `json:"requires_2fa"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	require.Equal(t, 0, response.Code)
	require.NotEmpty(t, response.Data.AccessToken, "有效凭证必须直接签发登录令牌")
	require.False(t, response.Data.Requires2FA)
	require.NotContains(t, w.Body.String(), grant.Token, "响应体不得回显不透明凭证")
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))

	require.Equal(t, service.AuditActionLogin2FAGrantUsed, auditAction)
	require.Equal(t, "used", auditExtra["two_factor_grant"])
	remaining, ok := auditExtra["two_factor_grant_remaining_seconds"].(int64)
	require.True(t, ok, "审计必须记录剩余秒数")
	require.Greater(t, remaining, int64(0))
	require.LessOrEqual(t, remaining, int64(300))

	// 第一次使用会重签：响应 Set-Cookie 换新值，旧 token 不再出现。
	rotated := ""
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == service.TwoFactorGrantCookieName {
			require.True(t, cookie.HttpOnly)
			require.True(t, cookie.Secure)
			require.Equal(t, "/", cookie.Path)
			require.Equal(t, http.SameSiteLaxMode, cookie.SameSite)
			require.Greater(t, cookie.MaxAge, 0)
			require.LessOrEqual(t, cookie.MaxAge, 300)
			rotated = cookie.Value
		}
	}
	require.NotEmpty(t, rotated)
	require.NotEqual(t, grant.Token, rotated, "成功使用后必须重签一次并替换 Cookie")

	// 重签后的 Cookie 仍可用（第二次不再重签，但窗口内继续有效）。
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(loginRequestBody(user.Email, "correct-password")))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("User-Agent", "grant-test-agent")
	req2.AddCookie(&http.Cookie{Name: service.TwoFactorGrantCookieName, Value: rotated})
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	require.Equal(t, http.StatusOK, w2.Code)
	require.Contains(t, w2.Body.String(), "access_token")
}

// 凭证缺失/伪造/用户不匹配/指纹不匹配/过期/存储不可用：一律退回动态码挑战。
func TestLoginRequiresTOTPWhenTwoFactorGrantInvalid(t *testing.T) {
	gin.SetMode(gin.TestMode)
	user, _, encryptor := newTwoFactorGrantTestUser(t, 7, "correct-password", true)
	repo := &twoFactorGrantUserRepo{user: user}
	cache := newTwoFactorGrantCacheStub()
	h, totpSvc, _ := newTwoFactorGrantAuthHandler(t, newTwoFactorGrantTestConfig(), repo, cache, user, encryptor)

	binding := service.TwoFactorGrantBinding{IP: "192.0.2.1", UserAgent: "grant-test-agent"}
	valid, err := totpSvc.IssueTwoFactorGrant(context.Background(), user.ID, service.TwoFactorGrantScopeLogin, binding)
	require.NoError(t, err)

	// 另一个用户的凭证。
	otherUserGrant, err := totpSvc.IssueTwoFactorGrant(context.Background(), 99, service.TwoFactorGrantScopeLogin, binding)
	require.NoError(t, err)

	// 指纹不匹配的凭证。
	otherUA, err := totpSvc.IssueTwoFactorGrant(context.Background(), user.ID, service.TwoFactorGrantScopeLogin,
		service.TwoFactorGrantBinding{IP: "192.0.2.1", UserAgent: "another-agent"})
	require.NoError(t, err)

	// 过期凭证。
	expired, err := totpSvc.IssueTwoFactorGrant(context.Background(), user.ID, service.TwoFactorGrantScopeLogin, binding)
	require.NoError(t, err)
	digest := twoFactorGrantDigestForHandlerTest(expired.Token)
	cache.records[digest].IssuedAt = time.Now().Add(-6 * time.Minute).Unix()
	cache.records[digest].ExpiresAt = time.Now().Add(-time.Second).Unix()

	cases := []struct {
		name   string
		cookie string
	}{
		{name: "no-cookie"},
		{name: "forged", cookie: "forged-token"},
		{name: "other-user", cookie: otherUserGrant.Token},
		{name: "other-ua", cookie: otherUA.Token},
		{name: "expired", cookie: expired.Token},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := gin.New()
			r.POST("/api/v1/auth/login", h.Login)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(loginRequestBody(user.Email, "correct-password")))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("User-Agent", "grant-test-agent")
			if tc.cookie != "" {
				req.AddCookie(&http.Cookie{Name: service.TwoFactorGrantCookieName, Value: tc.cookie})
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			require.Equal(t, http.StatusOK, w.Code)
			var response struct {
				Data struct {
					Requires2FA bool   `json:"requires_2fa"`
					TempToken   string `json:"temp_token"`
				} `json:"data"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
			require.True(t, response.Data.Requires2FA, "%s: 必须要求动态码", tc.name)
			require.NotEmpty(t, response.Data.TempToken)
			require.NotContains(t, w.Body.String(), "access_token")
		})
	}

	// 有效凭证仍然可用（排除测试自身把全部路径都判死）。
	r := gin.New()
	r.POST("/api/v1/auth/login", h.Login)
	r.POST("/api/v1/auth/login/2fa", h.Login2FA)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(loginRequestBody(user.Email, "correct-password")))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "grant-test-agent")
	req.AddCookie(&http.Cookie{Name: service.TwoFactorGrantCookieName, Value: valid.Token})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Contains(t, w.Body.String(), "access_token")

	// 密码错误时凭证不能绕过密码校验；统一登录协议下这一步返回与
	// 「需要 2FA」完全相同的挑战结构（不泄露密码对错），第二步才报
	// INVALID_CREDENTIALS。
	wrongReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(loginRequestBody(user.Email, "wrong-password")))
	wrongReq.Header.Set("Content-Type", "application/json")
	wrongReq.Header.Set("User-Agent", "grant-test-agent")
	wrongReq.AddCookie(&http.Cookie{Name: service.TwoFactorGrantCookieName, Value: valid.Token})
	wrongW := httptest.NewRecorder()
	r.ServeHTTP(wrongW, wrongReq)
	require.Equal(t, http.StatusOK, wrongW.Code)
	require.True(t, strings.Contains(wrongW.Body.String(), `"requires_2fa":true`), wrongW.Body.String())
	require.NotContains(t, wrongW.Body.String(), "access_token")

	// 第二步用错误密码：必须与「动态码错误」返回同一个码（INVALID_CREDENTIALS），
	// 不得回 TOTP_INVALID_CODE 之类可区分密码正确性的错误。
	var wrongChallenge struct {
		Data struct {
			TempToken string `json:"temp_token"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(wrongW.Body.Bytes(), &wrongChallenge), wrongW.Body.String())
	require.NotEmpty(t, wrongChallenge.Data.TempToken)
	step2Payload, err := json.Marshal(map[string]string{
		"temp_token": wrongChallenge.Data.TempToken,
		"totp_code":  "123456",
		"email":      user.Email,
		"password":   "wrong-password",
	})
	require.NoError(t, err)
	step2Req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login/2fa", strings.NewReader(string(step2Payload)))
	step2Req.Header.Set("Content-Type", "application/json")
	step2W := httptest.NewRecorder()
	r.ServeHTTP(step2W, step2Req)
	require.Equal(t, http.StatusUnauthorized, step2W.Code, step2W.Body.String())
	require.Contains(t, step2W.Body.String(), "INVALID_CREDENTIALS")
	require.NotContains(t, step2W.Body.String(), "TOTP_INVALID_CODE")
}

func TestLoginRequiresTOTPFailsClosedWhenGrantStoreUnavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	user, _, encryptor := newTwoFactorGrantTestUser(t, 7, "correct-password", true)
	repo := &twoFactorGrantUserRepo{user: user}
	cache := newTwoFactorGrantCacheStub()
	cache.failLoad = true
	h, totpSvc, _ := newTwoFactorGrantAuthHandler(t, newTwoFactorGrantTestConfig(), repo, cache, user, encryptor)
	grant, err := totpSvc.IssueTwoFactorGrant(context.Background(), user.ID, service.TwoFactorGrantScopeLogin,
		service.TwoFactorGrantBinding{IP: "192.0.2.1", UserAgent: "grant-test-agent"})
	require.NoError(t, err)
	cache.failLoad = true

	r := gin.New()
	r.POST("/api/v1/auth/login", h.Login)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(loginRequestBody(user.Email, "correct-password")))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "grant-test-agent")
	req.AddCookie(&http.Cookie{Name: service.TwoFactorGrantCookieName, Value: grant.Token})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.True(t, strings.Contains(w.Body.String(), `"requires_2fa":true`), "Redis 故障时不能放行: %s", w.Body.String())
}

// 登录二次验证成功必须下发不透明凭证 Cookie（HttpOnly/SameSite=Lax/Path=/），
// 且签发/使用都在审计里留痕，不含 token。
func TestLogin2FAIssuesTwoFactorGrantCookie(t *testing.T) {
	gin.SetMode(gin.TestMode)
	user, secret, encryptor := newTwoFactorGrantTestUser(t, 7, "correct-password", true)
	repo := &twoFactorGrantUserRepo{user: user}
	cache := newTwoFactorGrantCacheStub()
	h, totpSvc, _ := newTwoFactorGrantAuthHandler(t, newTwoFactorGrantTestConfig(), repo, cache, user, encryptor)

	tempToken, err := totpSvc.CreateLoginSession(context.Background(), user.ID, user.Email)
	require.NoError(t, err)
	code, err := totp.GenerateCode(secret, time.Now())
	require.NoError(t, err)

	var auditExtra map[string]any
	r := gin.New()
	r.POST("/api/v1/auth/login/2fa", func(c *gin.Context) {
		h.Login2FA(c)
		if value, ok := c.Get("audit_extra"); ok {
			auditExtra, _ = value.(map[string]any)
		}
	})
	body, _ := json.Marshal(map[string]string{"temp_token": tempToken, "totp_code": code})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login/2fa", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "grant-test-agent")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "access_token")
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))

	issued := ""
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name != service.TwoFactorGrantCookieName {
			continue
		}
		issued = cookie.Value
		require.True(t, cookie.HttpOnly, "凭证 Cookie 必须是 HttpOnly")
		require.True(t, cookie.Secure, "凭证 Cookie 必须只走 HTTPS")
		require.Equal(t, http.SameSiteLaxMode, cookie.SameSite)
		require.Equal(t, "/", cookie.Path)
		require.Equal(t, 300, cookie.MaxAge)
	}
	require.NotEmpty(t, issued, "2FA 验证成功必须下发 5 分钟凭证")
	require.NotContains(t, w.Body.String(), issued)

	require.Equal(t, "issued", auditExtra["two_factor_grant"])
	require.Equal(t, service.TwoFactorGrantScopeLogin, auditExtra["two_factor_grant_scope"])
	for _, value := range auditExtra {
		require.NotContains(t, fmt.Sprint(value), issued, "审计字段不得包含凭证")
	}
}

// step-up 成功后下发凭证；随后「校验当前密码」入口凭同一 Cookie 免 TOTP。
// 密码修改成功后凭证立即失效（撤销 + 清 Cookie）。
func TestStepUpGrantUnlocksPasswordChangeAndIsRevokedAfterUse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	user, secret, encryptor := newTwoFactorGrantTestUser(t, 7, "old-password", true)
	repo := &twoFactorGrantUserRepo{user: user}
	cache := newTwoFactorGrantCacheStub()
	settingSvc := newTwoFactorGrantTestSettings(map[string]string{service.SettingKeyTotpEnabled: "true"})
	totpSvc := service.NewTotpService(repo, encryptor, cache, settingSvc, nil, nil)
	userSvc := service.NewUserService(repo, nil, nil, nil)
	userHandler := &UserHandler{userService: userSvc, totpService: totpSvc}
	totpHandler := NewTotpHandler(totpSvc)

	withSubject := func(c *gin.Context) {
		c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: user.ID})
		c.Next()
	}

	stepUpRouter := gin.New()
	stepUpRouter.Use(withSubject)
	stepUpRouter.POST("/api/v1/user/totp/step-up", totpHandler.StepUp)

	code, err := totp.GenerateCode(secret, time.Now())
	require.NoError(t, err)
	body, _ := json.Marshal(map[string]string{"code": code})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/user/totp/step-up", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "grant-test-agent")
	w := httptest.NewRecorder()
	stepUpRouter.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	grantToken := twoFactorGrantCookieFromResponse(t, w.Result())
	require.NotEmpty(t, grantToken, "step-up 成功必须下发 2FA 凭证")

	passwordRouter := gin.New()
	passwordRouter.Use(withSubject)
	passwordRouter.PUT("/api/v1/user/password", userHandler.ChangePassword)
	passwordBody := `{"old_password":"old-password","new_password":"new-password-123"}`

	// 没有凭证：403 STEP_UP_REQUIRED，密码不变。
	noGrantReq := httptest.NewRequest(http.MethodPut, "/api/v1/user/password", strings.NewReader(passwordBody))
	noGrantReq.Header.Set("Content-Type", "application/json")
	noGrantReq.Header.Set("User-Agent", "grant-test-agent")
	noGrantW := httptest.NewRecorder()
	passwordRouter.ServeHTTP(noGrantW, noGrantReq)
	require.Equal(t, http.StatusForbidden, noGrantW.Code, noGrantW.Body.String())
	require.Contains(t, noGrantW.Body.String(), "STEP_UP_REQUIRED")
	require.True(t, user.CheckPassword("old-password"), "被门控拒绝时不得修改密码")

	// 带上 step-up 下发的凭证：免 TOTP 直接校验旧密码并修改。
	withGrantReq := httptest.NewRequest(http.MethodPut, "/api/v1/user/password", strings.NewReader(passwordBody))
	withGrantReq.Header.Set("Content-Type", "application/json")
	withGrantReq.Header.Set("User-Agent", "grant-test-agent")
	withGrantReq.AddCookie(&http.Cookie{Name: service.TwoFactorGrantCookieName, Value: grantToken})
	withGrantW := httptest.NewRecorder()
	passwordRouter.ServeHTTP(withGrantW, withGrantReq)
	require.Equal(t, http.StatusOK, withGrantW.Code, withGrantW.Body.String())
	require.True(t, user.CheckPassword("new-password-123"), "有效凭证应允许完成密码修改")

	// 密码已改：该用户全部凭证被撤销，Cookie 被清；旧 Cookie 再也不能跳过门控。
	require.Empty(t, cache.records, "密码修改后必须撤销该用户全部 2FA 凭证")
	var cleared bool
	for _, cookie := range withGrantW.Result().Cookies() {
		if cookie.Name == service.TwoFactorGrantCookieName && cookie.MaxAge < 0 {
			cleared = true
		}
	}
	require.True(t, cleared, "响应必须清掉本浏览器的 2FA 凭证 Cookie")

	replayReq := httptest.NewRequest(http.MethodPut, "/api/v1/user/password", strings.NewReader(passwordBody))
	replayReq.Header.Set("Content-Type", "application/json")
	replayReq.Header.Set("User-Agent", "grant-test-agent")
	replayReq.AddCookie(&http.Cookie{Name: service.TwoFactorGrantCookieName, Value: grantToken})
	replayW := httptest.NewRecorder()
	passwordRouter.ServeHTTP(replayW, replayReq)
	require.Equal(t, http.StatusForbidden, replayW.Code)
}

// 未启用 TOTP 的账号保持原有行为：密码校验入口不额外要求凭证。
func TestPasswordChangeWithoutTOTPKeepsWorking(t *testing.T) {
	gin.SetMode(gin.TestMode)
	user, _, encryptor := newTwoFactorGrantTestUser(t, 8, "old-password", false)
	repo := &twoFactorGrantUserRepo{user: user}
	cache := newTwoFactorGrantCacheStub()
	settingSvc := newTwoFactorGrantTestSettings(nil)
	totpSvc := service.NewTotpService(repo, encryptor, cache, settingSvc, nil, nil)
	userSvc := service.NewUserService(repo, nil, nil, nil)
	userHandler := &UserHandler{userService: userSvc, totpService: totpSvc}

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: user.ID})
		c.Next()
	})
	r.PUT("/api/v1/user/password", userHandler.ChangePassword)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/user/password",
		strings.NewReader(`{"old_password":"old-password","new_password":"new-password-456"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.True(t, user.CheckPassword("new-password-456"))
}

// Cookie 属性：HttpOnly + Secure + SameSite=Lax + Path=/，Max-Age 跟随剩余时间；
// 任何情况下都不允许 token 进入响应体。
func TestTwoFactorGrantCookieAttributes(t *testing.T) {
	grant := &service.TwoFactorGrant{
		Token:     "opaque-token-value",
		Scope:     service.TwoFactorGrantScopeLogin,
		Remaining: 4 * time.Minute,
	}

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	setTwoFactorGrantCookie(c, grant)
	cookies := w.Result().Cookies()
	require.Len(t, cookies, 1)
	require.Equal(t, service.TwoFactorGrantCookieName, cookies[0].Name)
	require.True(t, cookies[0].HttpOnly)
	require.True(t, cookies[0].Secure, "可跳过 2FA 的凭证必须只走 HTTPS")
	require.Equal(t, http.SameSiteLaxMode, cookies[0].SameSite)
	require.Equal(t, "/", cookies[0].Path)
	require.Equal(t, 240, cookies[0].MaxAge)
	require.NotContains(t, w.Body.String(), "opaque-token-value")
}

func twoFactorGrantDigestForHandlerTest(token string) string {
	// 与 service 内部实现独立：这里直接按同样公式重算，避免为了测试导出内部函数。
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
