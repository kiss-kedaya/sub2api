package handler

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/repository" //nolint:depguard // 需要真实的 Passkey 会话存储来核对 token 形态与 Redis TTL。
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/pquerna/otp/totp"
	"github.com/redis/go-redis/v9" //nolint:depguard // 需要真实的 Passkey 会话存储来核对 token 形态与 Redis TTL。
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// 登录首步「凭据无效」挑战稳定伪装（审计 A-P1-02）的行为测试。
// 夹具里的邮箱 / 密码 / 凭据 ID 全是假数据。

const (
	disguiseTestPassword = "correct-password"
	disguiseTestWrong    = "wrong-password"
	disguiseTestRPID     = "example.test"
	disguiseTestOrigin   = "https://example.test"
)

// ---- 夹具 ----

type disguiseUserRepo struct {
	service.UserRepository
	users []*service.User
	maxID int64
	// 库读计数：耗时对比里用来说明每条路径各做了几次点查。
	byEmail, byID atomic.Int64
}

func (r *disguiseUserRepo) GetByEmail(_ context.Context, email string) (*service.User, error) {
	r.byEmail.Add(1)
	for _, user := range r.users {
		if strings.EqualFold(strings.TrimSpace(email), user.Email) {
			return user, nil
		}
	}
	return nil, service.ErrUserNotFound
}

func (r *disguiseUserRepo) GetByID(_ context.Context, id int64) (*service.User, error) {
	r.byID.Add(1)
	for _, user := range r.users {
		if user.ID == id {
			return user, nil
		}
	}
	return nil, service.ErrUserNotFound
}

func (r *disguiseUserRepo) GetUserAvatar(context.Context, int64) (*service.UserAvatar, error) {
	return nil, nil
}

func (r *disguiseUserRepo) Update(context.Context, *service.User, service.UserUpdateFields) error {
	return nil
}

func (r *disguiseUserRepo) MaxUserID(context.Context) (int64, error) { return r.maxID, nil }

type disguisePasskeyRepo struct {
	service.PasskeyRepository
	records       map[int64][]service.PasskeyCredentialRecord
	lists, handle atomic.Int64
}

func (r *disguisePasskeyRepo) ListByUserID(_ context.Context, userID int64) ([]service.PasskeyCredentialRecord, error) {
	r.lists.Add(1)
	return r.records[userID], nil
}

func (r *disguisePasskeyRepo) GetUserHandle(_ context.Context, userID int64) ([]byte, error) {
	r.handle.Add(1)
	if len(r.records[userID]) == 0 {
		return nil, service.ErrPasskeyNotFound
	}
	return []byte(fmt.Sprintf("disguise-test-handle-%d-padding-32b", userID)), nil
}

// disguiseLimiter 与生产限速器同口径：同一个键最多放行 limit 次，之后 429；Reset 清零。
type disguiseLimiter struct {
	mu     sync.Mutex
	counts map[int64]int
	limit  int
}

func newDisguiseLimiter() *disguiseLimiter {
	return &disguiseLimiter{counts: map[int64]int{}, limit: 5}
}

func (l *disguiseLimiter) Check(_ context.Context, key int64) (bool, time.Duration, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.counts[key]++
	if l.counts[key] > l.limit {
		return false, 30 * time.Second, nil
	}
	return true, 0, nil
}

func (l *disguiseLimiter) Reset(_ context.Context, key int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.counts, key)
}

func (l *disguiseLimiter) clear() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.counts = map[int64]int{}
}

// 四个账号覆盖所有真实因子形状。
const (
	disguiseUserTOTPID    = int64(21) // 只绑 TOTP        → [totp]
	disguiseUserPasskeyID = int64(22) // 只绑 Passkey      → [passkey]
	disguiseUserBothID    = int64(23) // 两者都绑          → [totp passkey]
	disguiseUserNoneID    = int64(24) // 没有任何二次验证  → 直接签发令牌
	disguiseTestUserIDMax = int64(14000)
)

type disguiseHarness struct {
	t        *testing.T
	handler  *AuthHandler
	router   *gin.Engine
	redis    *miniredis.Miniredis
	limiter  *disguiseLimiter // 内存限速器；newDisguiseHarnessWithRedis 夹具里为 nil（改用生产的 Redis 限速器）
	settings *loginTOTPSettingRepo
	users    map[int64]*service.User
	secrets  map[int64]string
	cfg      *config.Config

	userRepo    *disguiseUserRepo
	passkeyRepo *disguisePasskeyRepo

	// remoteAddr 非空时作为后续请求的 RemoteAddr，用来模拟不同的客户端 IP（默认是 httptest 的 192.0.2.1）。
	remoteAddr string
}

func newDisguiseHarness(t *testing.T, switchValue string) *disguiseHarness {
	t.Helper()
	return newDisguiseHarnessWith(t, switchValue, false)
}

// newDisguiseHarnessWithRedis 与 newDisguiseHarness 相同，但限速与 TOTP 缓存都换成生产实现（middleware.TwoFactorRateLimiter、
// repository.TotpCache）跑在 miniredis 上：第二步的滑动窗口桶与首步的凭证校验桶是真实口径，不是同口径的内存替身。
func newDisguiseHarnessWithRedis(t *testing.T, switchValue string) *disguiseHarness {
	t.Helper()
	return newDisguiseHarnessWith(t, switchValue, true)
}

func newDisguiseHarnessWith(t *testing.T, switchValue string, realBackends bool) *disguiseHarness {
	t.Helper()
	gin.SetMode(gin.TestMode)

	cfg := newTwoFactorGrantTestConfig()
	cfg.WebAuthn = config.WebAuthnConfig{
		Enabled:       true,
		RPDisplayName: "Sub2API",
		RPID:          disguiseTestRPID,
		RPOrigins:     []string{disguiseTestOrigin},
	}

	h := &disguiseHarness{
		t:       t,
		users:   map[int64]*service.User{},
		secrets: map[int64]string{},
		cfg:     cfg,
		settings: &loginTOTPSettingRepo{values: map[string]string{
			service.SettingKeyTotpEnabled: "true",
		}},
	}
	if !realBackends {
		h.limiter = newDisguiseLimiter()
	}
	if switchValue != "" {
		h.settings.values[service.SettingKeyLoginShapeDisguiseEnabled] = switchValue
	}

	passkeyRepo := &disguisePasskeyRepo{records: map[int64][]service.PasskeyCredentialRecord{}}
	addUser := func(id int64, totpEnabled bool, credentials ...webauthn.Credential) {
		user, secret, _ := newTwoFactorGrantTestUser(t, id, disguiseTestPassword, totpEnabled)
		h.users[id] = user
		h.secrets[id] = secret
		for i, credential := range credentials {
			passkeyRepo.records[id] = append(passkeyRepo.records[id], service.PasskeyCredentialRecord{
				ID:         id*100 + int64(i),
				UserID:     id,
				UserHandle: []byte(fmt.Sprintf("disguise-test-handle-%d-padding-32b", id)),
				Credential: credential,
			})
		}
	}
	addUser(disguiseUserTOTPID, true)
	addUser(disguiseUserPasskeyID, false, webauthn.Credential{
		ID:        []byte("passkey-only-credential-id-32-bt"),
		Transport: []protocol.AuthenticatorTransport{protocol.Hybrid, protocol.Internal},
	})
	addUser(disguiseUserBothID, true,
		webauthn.Credential{ID: []byte("both-factors-credential-id-1-32b"), Transport: []protocol.AuthenticatorTransport{protocol.Internal}},
		webauthn.Credential{ID: []byte("both-factors-credential-id-2-20"), Transport: []protocol.AuthenticatorTransport{protocol.NFC, protocol.USB}},
	)
	addUser(disguiseUserNoneID, false)

	userRepo := &disguiseUserRepo{maxID: disguiseTestUserIDMax}
	for _, id := range []int64{disguiseUserTOTPID, disguiseUserPasskeyID, disguiseUserBothID, disguiseUserNoneID} {
		userRepo.users = append(userRepo.users, h.users[id])
	}

	redisServer := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { require.NoError(t, redisClient.Close()) })
	h.redis = redisServer

	settingSvc := service.NewSettingService(h.settings, cfg)
	authSvc := service.NewAuthService(nil, userRepo, nil, twoFactorGrantRefreshCache{}, cfg,
		settingSvc, nil, nil, nil, nil, nil, nil, nil)
	var totpCache service.TotpCache = newTwoFactorGrantCacheStub()
	if realBackends {
		totpCache = repository.NewTotpCache(redisClient)
	}
	totpSvc := service.NewTotpService(userRepo, twoFactorGrantTestEncryptor{}, totpCache, settingSvc, nil, nil)
	totpSvc.SetPasskeyRepository(passkeyRepo)
	userSvc := service.NewUserService(userRepo, nil, nil, nil)
	passkeySvc, err := service.NewPasskeyService(cfg, passkeyRepo, repository.NewPasskeySessionStore(redisClient), userRepo)
	require.NoError(t, err)

	h.userRepo, h.passkeyRepo = userRepo, passkeyRepo
	h.handler = NewAuthHandler(cfg, authSvc, userSvc, settingSvc, nil, nil, totpSvc, nil)
	h.handler.SetPasskeyService(passkeySvc)
	if realBackends {
		h.handler.SetTwoFactorRateLimiter(middleware2.NewTwoFactorRateLimiter(redisClient))
	} else {
		h.handler.SetTwoFactorRateLimiter(h.limiter)
	}

	h.router = gin.New()
	h.router.POST("/api/v1/auth/login", h.handler.Login)
	h.router.POST("/api/v1/auth/login/2fa", h.handler.Login2FA)
	h.router.POST("/api/v1/auth/login/2fa/passkey/finish", h.handler.Login2FAPasskeyFinish)
	return h
}

func (h *disguiseHarness) post(path, body string) *httptest.ResponseRecorder {
	h.t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "disguise-test-agent")
	if h.remoteAddr != "" {
		req.RemoteAddr = h.remoteAddr
	}
	w := httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	return w
}

func (h *disguiseHarness) login(email, password string) *httptest.ResponseRecorder {
	h.t.Helper()
	return h.post("/api/v1/auth/login", loginRequestBody(email, password))
}

// loginWithCookies 与 login 相同，但带上指定的 Cookie（用来探测 5 分钟因子凭证 Cookie 的处理）。
func (h *disguiseHarness) loginWithCookies(email, password string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	h.t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(loginRequestBody(email, password)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "disguise-test-agent")
	if h.remoteAddr != "" {
		req.RemoteAddr = h.remoteAddr
	}
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	return w
}

// setCookieSummary 把响应里的 Set-Cookie 归一成排序后的「名:MaxAge」列表，用来对比两份响应是否清了同样的 Cookie。
func setCookieSummary(w *httptest.ResponseRecorder) []string {
	var out []string
	for _, cookie := range w.Result().Cookies() {
		out = append(out, fmt.Sprintf("%s:%d", cookie.Name, cookie.MaxAge))
	}
	sort.Strings(out)
	return out
}

func (h *disguiseHarness) emailOf(id int64) string { return h.users[id].Email }

// dbReads 返回夹具仓储累计的点查次数（按 用户-按邮箱 / 用户-按ID / 凭据列表 / user handle 分类）。
func (h *disguiseHarness) dbReads() [4]int64 {
	return [4]int64{h.userRepo.byEmail.Load(), h.userRepo.byID.Load(), h.passkeyRepo.lists.Load(), h.passkeyRepo.handle.Load()}
}

func login2FABody(tempToken, email, password, code string) string {
	payload := map[string]string{"temp_token": tempToken, "totp_code": code}
	if email != "" {
		payload["email"] = email
	}
	if password != "" {
		payload["password"] = password
	}
	raw, _ := json.Marshal(payload)
	return string(raw)
}

func passkeyFinishBody(tempToken, sessionToken, credential string) string {
	raw, _ := json.Marshal(map[string]any{
		"temp_token":    tempToken,
		"session_token": sessionToken,
		"credential":    json.RawMessage(credential),
	})
	return string(raw)
}

// ---- 响应归一化：只抹掉随机值，其余逐字节保留 ----

var (
	reDisguiseTempToken    = regexp.MustCompile(`"temp_token":"[0-9a-f]{64}"`)
	reDisguiseSessionToken = regexp.MustCompile(`"passkey_session_token":"(v1\.login_2fa\.\d+)\.[A-Za-z0-9_-]{43}"`)
	reDisguiseChallenge    = regexp.MustCompile(`"challenge":"[A-Za-z0-9_-]{43}"`)
)

var reDisguiseMasked = regexp.MustCompile(`"user_email_masked":"[^"]*"`)

// normalizeIgnoringMask 在 normalizeChallengeBody 之外再抹掉邮箱掩码：掩码按请求里的邮箱原样计算（大小写敏感），
// 大小写变体的请求只用来证明「形状 / 凭据 / ID 段」不随大小写变化。
func normalizeIgnoringMask(body string) string {
	return reDisguiseMasked.ReplaceAllString(normalizeChallengeBody(body), `"user_email_masked":"«M»"`)
}

func normalizeChallengeBody(body string) string {
	body = reDisguiseTempToken.ReplaceAllString(body, `"temp_token":"«T»"`)
	body = reDisguiseSessionToken.ReplaceAllString(body, `"passkey_session_token":"$1.«N»"`)
	body = reDisguiseChallenge.ReplaceAllString(body, `"challenge":"«C»"`)
	return body
}

type challengeVars struct {
	tempToken    string
	sessionToken string
	challenge    string
	allowedIDs   [][]byte
}

func parseChallenge(t *testing.T, w *httptest.ResponseRecorder) challengeVars {
	t.Helper()
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	body := w.Body.String()
	vars := challengeVars{
		tempToken:    gjson.Get(body, "data.temp_token").String(),
		sessionToken: gjson.Get(body, "data.passkey_session_token").String(),
		challenge:    gjson.Get(body, "data.passkey_options.publicKey.challenge").String(),
	}
	for _, entry := range gjson.Get(body, "data.passkey_options.publicKey.allowCredentials").Array() {
		id, err := base64.RawURLEncoding.DecodeString(entry.Get("id").String())
		require.NoError(t, err)
		vars.allowedIDs = append(vars.allowedIDs, id)
	}
	return vars
}

func objectKeys(raw gjson.Result) []string {
	var keys []string
	raw.ForEach(func(key, _ gjson.Result) bool {
		keys = append(keys, key.String())
		return true
	})
	return keys
}

// disguiseShapeOf 返回挑战响应里的 required_factors / next_factor / 是否带 passkey 字段。
func disguiseShapeOf(body string) string {
	return fmt.Sprintf("required=%s next=%s passkey=%t",
		gjson.Get(body, "data.required_factors").Raw,
		gjson.Get(body, "data.next_factor").String(),
		gjson.Get(body, "data.passkey_options").Exists())
}

// 在合成邮箱里找出 HMAC 形状为 want 的邮箱（同一密钥下确定）。
func findDisguiseEmail(t *testing.T, secret string, want []service.Factor, skip int) string {
	t.Helper()
	d := service.NewLoginDisguise(secret)
	require.NotNil(t, d)
	for i := 0; i < 100000; i++ {
		email := fmt.Sprintf("nobody-%d@example.test", i)
		shape := d.Shape(email, true)
		if fmt.Sprint(shape) == fmt.Sprint(want) {
			if skip == 0 {
				return email
			}
			skip--
		}
	}
	t.Fatalf("没有找到形状为 %v 的合成邮箱", want)
	return ""
}

var (
	shapeTOTP     = []service.Factor{service.FactorTOTP}
	shapePasskey  = []service.Factor{service.FactorPasskey}
	shapeTOTPPass = []service.Factor{service.FactorTOTP, service.FactorPasskey}
)

// ---- 1. 开关关闭：凭据错误统一返回 401 INVALID_CREDENTIALS，不弹 2FA 框 ----

func TestLoginShapeDisguiseOffIsByteIdenticalToLegacy(t *testing.T) {
	for _, switchValue := range []string{"", "false", "garbage"} {
		t.Run("switch="+switchValue, func(t *testing.T) {
			h := newDisguiseHarness(t, switchValue)
			cases := []struct {
				name     string
				email    string
				password string
			}{
				{"unknown email", "nobody@example.test", disguiseTestPassword},
				{"totp account wrong password", h.emailOf(disguiseUserTOTPID), disguiseTestWrong},
				{"passkey account wrong password", h.emailOf(disguiseUserPasskeyID), disguiseTestWrong},
				{"passkey+totp account wrong password", h.emailOf(disguiseUserBothID), disguiseTestWrong},
				{"no-2fa account wrong password", h.emailOf(disguiseUserNoneID), disguiseTestWrong},
				{"mixed case email", strings.ToUpper(h.emailOf(disguiseUserPasskeyID)), disguiseTestWrong},
			}
			var baseline string
			for _, tc := range cases {
				w := h.login(tc.email, tc.password)
				require.Equal(t, http.StatusUnauthorized, w.Code, tc.name)
				require.Equal(t, "no-store", w.Header().Get("Cache-Control"), tc.name)
				require.Equal(t, "application/json; charset=utf-8", w.Header().Get("Content-Type"), tc.name)
				require.Empty(t, w.Result().Cookies(), tc.name)

				body := w.Body.String()
				require.Contains(t, body, `"reason":"INVALID_CREDENTIALS"`, tc.name)
				require.NotContains(t, body, "requires_2fa", tc.name)
				require.NotContains(t, body, "temp_token", tc.name)
				require.NotContains(t, body, "passkey", tc.name)
				if baseline == "" {
					baseline = body
				} else {
					require.Equal(t, baseline, body, "%s 必须与不存在邮箱的 401 响应逐字节一致", tc.name)
				}
			}
		})
	}
}

// 开关关闭时不再创建诱饵会话；同时保留对滚动升级期间遗留的旧无用户会话（UserID=0）的 fail-closed 拒绝与清理。
func TestLoginShapeDisguiseOffKeepsLegacySessionsAndSecondStep(t *testing.T) {
	h := newDisguiseHarness(t, "")
	email := h.emailOf(disguiseUserPasskeyID)
	w := h.login(email, disguiseTestWrong)
	require.Equal(t, http.StatusUnauthorized, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "INVALID_CREDENTIALS")
	require.NotContains(t, w.Body.String(), "temp_token")

	// 兼容滚动升级期间仍在 TTL 内的旧 UserID=0 诱饵会话：首次提交报 401 并删会话，第二次报 400。
	legacyToken, err := h.handler.totpService.CreateLoginSession(context.Background(), 0, email)
	require.NoError(t, err)
	session, err := h.handler.totpService.GetLoginSession(context.Background(), legacyToken)
	require.NoError(t, err)
	require.NotNil(t, session)
	require.False(t, session.Decoy)
	require.EqualValues(t, 0, session.UserID)

	first := h.post("/api/v1/auth/login/2fa", login2FABody(legacyToken, email, disguiseTestWrong, "123456"))
	require.Equal(t, http.StatusUnauthorized, first.Code, first.Body.String())
	require.Contains(t, first.Body.String(), "INVALID_CREDENTIALS")
	second := h.post("/api/v1/auth/login/2fa", login2FABody(legacyToken, email, disguiseTestWrong, "123456"))
	require.Equal(t, http.StatusBadRequest, second.Code, second.Body.String())
}

// 全站关闭 TOTP 登录验证时没有诱饵挑战，开关不改变这一点。
func TestLoginShapeDisguiseDoesNotApplyWhenTOTPLoginIsOff(t *testing.T) {
	h := newDisguiseHarness(t, "true")
	h.settings.values[service.SettingKeyTotpEnabled] = "false"
	w := h.login(h.emailOf(disguiseUserPasskeyID), disguiseTestWrong)
	require.Equal(t, http.StatusUnauthorized, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "INVALID_CREDENTIALS")
}

// ---- 2. 开关打开：镜像真实账号，密码对错不改变响应 ----

func TestLoginShapeDisguiseMirrorsRealAccountShape(t *testing.T) {
	h := newDisguiseHarness(t, "true")
	for _, tc := range []struct {
		name    string
		id      int64
		wantKey string
	}{
		{"totp only", disguiseUserTOTPID, `required=["totp"] next=totp passkey=false`},
		{"passkey only", disguiseUserPasskeyID, `required=["passkey"] next=passkey passkey=true`},
		{"totp + passkey", disguiseUserBothID, `required=["totp","passkey"] next=totp passkey=false`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			email := h.emailOf(tc.id)
			realW := h.login(email, disguiseTestPassword)
			require.Equal(t, http.StatusOK, realW.Code, realW.Body.String())
			require.Equal(t, tc.wantKey, disguiseShapeOf(realW.Body.String()), "真实挑战的形状")
			realBody := normalizeChallengeBody(realW.Body.String())

			for i := 0; i < 3; i++ {
				fake := h.login(email, disguiseTestWrong)
				require.Equal(t, http.StatusOK, fake.Code, fake.Body.String())
				require.Equal(t, "no-store", fake.Header().Get("Cache-Control"))
				require.Equal(t, realW.Header().Get("Content-Type"), fake.Header().Get("Content-Type"))
				// 密码错和密码对：除随机值（temp_token / challenge / session token 的随机段）外逐字节一致，
				// 包括凭据 ID、transports、timeout、rpId 和 token 里的用户 ID 段。
				require.Equal(t, realBody, normalizeChallengeBody(fake.Body.String()), "密码错的诱饵必须与密码对的真实挑战同形")
			}
			// 大小写不改变镜像（登录对邮箱大小写不敏感）。
			variant := h.login(strings.ToUpper(email), disguiseTestWrong)
			require.Equal(t, normalizeIgnoringMask(realW.Body.String()), normalizeIgnoringMask(variant.Body.String()))
		})
	}
}

// 没有 2FA 的真实账号 / 不存在的邮箱：同一邮箱永远同一种形状，大小写 / 空白 / 另一个实例（同密钥）一致。
func TestLoginShapeDisguiseStableShapeForEmailsWithoutRealShape(t *testing.T) {
	h := newDisguiseHarness(t, "true")
	other := newDisguiseHarness(t, "true") // 另一个实例：同一个 JWT secret
	secret := h.cfg.JWT.Secret

	emails := []string{
		h.emailOf(disguiseUserNoneID),
		findDisguiseEmail(t, secret, shapeTOTP, 0),
		findDisguiseEmail(t, secret, shapePasskey, 0),
		findDisguiseEmail(t, secret, shapePasskey, 1),
		findDisguiseEmail(t, secret, shapeTOTPPass, 0),
	}
	for _, email := range emails {
		first := normalizeChallengeBody(h.login(email, disguiseTestWrong).Body.String())
		for i := 0; i < 5; i++ {
			require.Equal(t, first, normalizeChallengeBody(h.login(email, fmt.Sprintf("guess-%d", i)).Body.String()), "同一邮箱换密码也必须同形")
		}
		require.Equal(t, normalizeIgnoringMask(h.login(email, disguiseTestWrong).Body.String()),
			normalizeIgnoringMask(h.login(strings.ToUpper(email), disguiseTestWrong).Body.String()), "大小写不得改变伪装")
		require.Equal(t, first, normalizeChallengeBody(other.login(email, disguiseTestWrong).Body.String()), "另一个实例（同密钥）对同一邮箱必须给出同一套伪装")
	}

	// 形状覆盖：[totp] [passkey] [totp passkey] 三种都出现，且与 Shape() 的结论一致。
	seen := map[string]bool{}
	for _, email := range emails[1:] {
		body := h.login(email, disguiseTestWrong).Body.String()
		seen[disguiseShapeOf(body)] = true
	}
	require.Equal(t, map[string]bool{
		`required=["totp"] next=totp passkey=false`:           true,
		`required=["passkey"] next=passkey passkey=true`:      true,
		`required=["totp","passkey"] next=totp passkey=false`: true,
	}, seen)
}

// 伪造的 Passkey 选项与真实选项同构：同样的字段集合与顺序、challenge 长度 / 编码、rpId、timeout、
// userVerification，allowCredentials 里的 ID / transports 落在真实分布内；session token 形态一致、
// Redis TTL 一致，且伪造的会话带 Decoy 标记。
func TestDecoyPasskeyOptionsAreIndistinguishableFromRealOptions(t *testing.T) {
	h := newDisguiseHarness(t, "true")
	realW := h.login(h.emailOf(disguiseUserPasskeyID), disguiseTestPassword)
	realBody := realW.Body.String()
	realVars := parseChallenge(t, realW)

	tokenPattern := regexp.MustCompile(`^v1\.login_2fa\.(\d+)\.[A-Za-z0-9_-]{43}$`)
	require.Regexp(t, tokenPattern, realVars.sessionToken)

	realTTL := h.redis.TTL("passkey:session:" + realVars.sessionToken)
	require.Equal(t, 5*time.Minute, realTTL)

	allowedLengths := map[int]bool{16: true, 20: true, 32: true, 64: true}
	realTransports := map[string]bool{
		`["internal"]`: true, `["hybrid","internal"]`: true, ``: true, `["usb"]`: true, `["nfc","usb"]`: true,
	}

	// 多个 HMAC 取形为单 Passkey 的邮箱，覆盖个数 / 长度 / transports 的取值。
	secret := h.cfg.JWT.Secret
	for skip := 0; skip < 40; skip++ {
		email := findDisguiseEmail(t, secret, shapePasskey, skip)
		w := h.login(email, disguiseTestWrong)
		body := w.Body.String()
		vars := parseChallenge(t, w)

		// 顶层 / publicKey 的字段集合与顺序和真实响应完全一致。
		require.Equal(t, objectKeys(gjson.Get(realBody, "data")), objectKeys(gjson.Get(body, "data")))
		require.Equal(t, objectKeys(gjson.Get(realBody, "data.passkey_options")), objectKeys(gjson.Get(body, "data.passkey_options")))
		require.Equal(t, objectKeys(gjson.Get(realBody, "data.passkey_options.publicKey")), objectKeys(gjson.Get(body, "data.passkey_options.publicKey")))
		require.Equal(t, gjson.Get(realBody, "data.passkey_options.publicKey.rpId").String(), gjson.Get(body, "data.passkey_options.publicKey.rpId").String())
		require.Equal(t, gjson.Get(realBody, "data.passkey_options.publicKey.timeout").Raw, gjson.Get(body, "data.passkey_options.publicKey.timeout").Raw)
		require.Equal(t, "required", gjson.Get(body, "data.passkey_options.publicKey.userVerification").String())
		require.Len(t, vars.challenge, 43)
		require.NotEqual(t, realVars.challenge, vars.challenge, "challenge 每次随机")

		entries := gjson.Get(body, "data.passkey_options.publicKey.allowCredentials").Array()
		require.NotEmpty(t, entries)
		require.LessOrEqual(t, len(entries), 3)
		for _, entry := range entries {
			keys := objectKeys(entry)
			require.Contains(t, [][]string{{"type", "id", "transports"}, {"type", "id"}}, keys)
			require.Equal(t, "public-key", entry.Get("type").String())
			id, err := base64.RawURLEncoding.DecodeString(entry.Get("id").String())
			require.NoError(t, err)
			require.True(t, allowedLengths[len(id)], "ID 字节数 %d", len(id))
			require.True(t, realTransports[strings.ReplaceAll(entry.Get("transports").Raw, " ", "")], "transports %s", entry.Get("transports").Raw)
		}

		// session token 形态与 TTL。
		match := tokenPattern.FindStringSubmatch(vars.sessionToken)
		require.NotNil(t, match, vars.sessionToken)
		var scope int64
		_, err := fmt.Sscan(match[1], &scope)
		require.NoError(t, err)
		require.GreaterOrEqual(t, scope, int64(1))
		require.LessOrEqual(t, scope, int64(16384), "用户 ID 段取值上界 = 现网最大用户 ID 向上取 2 的幂")
		require.Equal(t, realTTL, h.redis.TTL("passkey:session:"+vars.sessionToken))
		stored, err := h.redis.Get("passkey:session:" + vars.sessionToken)
		require.NoError(t, err)
		require.True(t, gjson.Get(stored, "decoy").Bool(), "伪造的 Passkey 会话必须带 Decoy 标记")
		require.Equal(t, "login_2fa", gjson.Get(stored, "kind").String())
		// 登录会话（temp_token）同样带标记、UserID 恒为 0。
		session, err := h.handler.totpService.GetLoginSession(context.Background(), vars.tempToken)
		require.NoError(t, err)
		require.True(t, session.Decoy)
		require.EqualValues(t, 0, session.UserID)
		require.Equal(t, []service.Factor{service.FactorPasskey}, session.RequiredFactors)
	}
	// 真实会话不带 Decoy 标记。
	storedReal, err := h.redis.Get("passkey:session:" + realVars.sessionToken)
	require.NoError(t, err)
	require.False(t, gjson.Get(storedReal, "decoy").Exists())
}

// 现网出现的真实挑战，每一种都能在伪装里找到同形的响应（结构级对照，随机取值之外字节一致）。
func TestDecoyShapesCoverEveryRealChallengeShape(t *testing.T) {
	h := newDisguiseHarness(t, "true")
	secret := h.cfg.JWT.Secret
	for _, tc := range []struct {
		name   string
		realID int64
		fake   []service.Factor
	}{
		{"totp", disguiseUserTOTPID, shapeTOTP},
		{"passkey", disguiseUserPasskeyID, shapePasskey},
		{"totp+passkey", disguiseUserBothID, shapeTOTPPass},
	} {
		t.Run(tc.name, func(t *testing.T) {
			realBody := h.login(h.emailOf(tc.realID), disguiseTestPassword).Body.String()
			fakeBody := h.login(findDisguiseEmail(t, secret, tc.fake, 0), disguiseTestWrong).Body.String()
			require.Equal(t, disguiseShapeOf(realBody), disguiseShapeOf(fakeBody))
			require.Equal(t, objectKeys(gjson.Get(realBody, "data")), objectKeys(gjson.Get(fakeBody, "data")))
			require.Equal(t, len(gjson.Get(realBody, "data.temp_token").String()), len(gjson.Get(fakeBody, "data.temp_token").String()))
		})
	}
}

// ---- 3. 第二步：伪装会话与真实会话逐步一致 ----

type transcriptEntry struct {
	status     int
	body       string
	retryAfter bool
}

func record(w *httptest.ResponseRecorder) transcriptEntry {
	return transcriptEntry{status: w.Code, body: w.Body.String(), retryAfter: w.Header().Get("Retry-After") != ""}
}

// firstStep 取一个会话：password 对 → 真实会话；password 错 → 伪装会话。
func (h *disguiseHarness) firstStep(email, password string) challengeVars {
	h.t.Helper()
	return parseChallenge(h.t, h.login(email, password))
}

// 同一份探测脚本分别打在「真实会话」和「伪装会话」上，逐条对比状态码 + 响应体 + 是否带 Retry-After。
// 脚本覆盖：自然重放（同一个密码）、乱填密码、不带密码、换邮箱、失败后会话是否保留、额度耗尽。
func TestDecoyLogin2FAMatchesRealSessionTranscript(t *testing.T) {
	h := newDisguiseHarness(t, "true")
	for _, tc := range []struct {
		name string
		id   int64
	}{
		{"totp only", disguiseUserTOTPID},
		{"passkey only", disguiseUserPasskeyID},
		{"totp + passkey", disguiseUserBothID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			email := h.emailOf(tc.id)
			steps := []struct {
				name string
				body func(vars challengeVars, password string) string
				n    int
				// last 是第 n 次请求的预期状态码：证明探测脚本确实走到了限速（429）或确实没有耗额度（401）。
				last int
			}{
				{"natural replay with the step-1 password", func(v challengeVars, p string) string { return login2FABody(v.tempToken, email, p, "000000") }, 8, http.StatusTooManyRequests},
				{"garbage password", func(v challengeVars, _ string) string {
					return login2FABody(v.tempToken, email, "garbage-password", "000000")
				}, 8, http.StatusUnauthorized},
				{"no password (legacy clients)", func(v challengeVars, _ string) string { return login2FABody(v.tempToken, "", "", "000000") }, 8, http.StatusTooManyRequests},
				{"email mismatch", func(v challengeVars, p string) string {
					return login2FABody(v.tempToken, "someone-else@example.test", p, "000000")
				}, 3, http.StatusUnauthorized},
				{"empty request email", func(v challengeVars, p string) string { return login2FABody(v.tempToken, "", p, "000000") }, 8, http.StatusTooManyRequests},
			}
			for _, step := range steps {
				t.Run(step.name, func(t *testing.T) {
					run := func(password string) []transcriptEntry {
						h.limiter.clear()
						vars := h.firstStep(email, password)
						var out []transcriptEntry
						for i := 0; i < step.n; i++ {
							out = append(out, record(h.post("/api/v1/auth/login/2fa", step.body(vars, password))))
						}
						return out
					}
					realRun := run(disguiseTestPassword)
					decoyRun := run(disguiseTestWrong)
					require.Equal(t, realRun, decoyRun, "伪装会话在第二步必须与真实会话逐条一致")
					// 保险：探测脚本不是空转——真实会话自己就走到了预期的终态（429 限速 / 401 不耗额度），
					// 并且失败后会话仍然保留（第一条和最后一条之前的请求都落在同一类失败上，而不是「会话不存在」的 400）。
					require.Equal(t, step.last, realRun[len(realRun)-1].status, "真实会话的终态")
					require.NotEqual(t, http.StatusTooManyRequests, realRun[0].status)
					if step.last == http.StatusTooManyRequests {
						require.True(t, realRun[len(realRun)-1].retryAfter, "429 必须带 Retry-After")
					}
					require.NotContains(t, realRun[0].body, "Invalid or expired 2FA session", "失败一次不得销毁会话")
				})
			}

			// 额度桶（审计 AUD-6）：同一个 IP 上，伪装会话与真实会话共用「账号 + IP」桶——伪装会话耗尽它之后，
			// 真实会话在同一个 IP 上同样被限速（两边分开的话，429 与 401 的差别就是「密码对不对」的信号）；
			// 换一个 IP 就互不影响：伪装会话不能被拿来锁别处的真实用户。
			// 同口径 Redis 限速器上的完整对拍见 auth_login_disguise_budget_test.go。
			h.limiter.clear()
			decoy := h.firstStep(email, disguiseTestWrong)
			for i := 0; i < 8; i++ {
				h.post("/api/v1/auth/login/2fa", login2FABody(decoy.tempToken, email, disguiseTestWrong, "000000"))
			}
			realVars := h.firstStep(email, disguiseTestPassword)
			w := h.post("/api/v1/auth/login/2fa", login2FABody(realVars.tempToken, email, disguiseTestPassword, "000000"))
			require.Equal(t, http.StatusTooManyRequests, w.Code, "同一个 IP：真实会话与伪装会话共用桶，必须同样被限速")

			h.remoteAddr = "198.51.100.77:4000"
			defer func() { h.remoteAddr = "" }()
			realElsewhere := h.firstStep(email, disguiseTestPassword)
			w = h.post("/api/v1/auth/login/2fa", login2FABody(realElsewhere.tempToken, email, disguiseTestPassword, "000000"))
			require.NotEqual(t, http.StatusTooManyRequests, w.Code, "换一个 IP：伪装会话不得耗尽别处真实账号的额度")
		})
	}
}

// 伪装会话永远不会放行：就算持有真实账号的有效动态码，也不能用密码错误时拿到的会话登录。
func TestDecoySessionNeverAuthenticates(t *testing.T) {
	h := newDisguiseHarness(t, "true")
	email := h.emailOf(disguiseUserTOTPID)
	vars := h.firstStep(email, disguiseTestWrong)
	code, err := totp.GenerateCode(h.secrets[disguiseUserTOTPID], time.Now())
	require.NoError(t, err)

	for _, tc := range []struct {
		name     string
		password string
	}{
		{"no password (old protocol)", ""},
		{"wrong password", disguiseTestWrong},
		{"correct password", disguiseTestPassword},
	} {
		h.limiter.clear()
		w := h.post("/api/v1/auth/login/2fa", login2FABody(vars.tempToken, email, tc.password, code))
		require.Equal(t, http.StatusUnauthorized, w.Code, tc.name+": "+w.Body.String())
		require.NotContains(t, w.Body.String(), "access_token", tc.name)
		require.NotContains(t, w.Body.String(), "refresh_token", tc.name)
		require.Contains(t, w.Body.String(), "INVALID_CREDENTIALS", tc.name)
	}

	// 单 Passkey 形状的伪装会话：Passkey finish 同样永远不放行。
	passkeyEmail := h.emailOf(disguiseUserPasskeyID)
	pv := h.firstStep(passkeyEmail, disguiseTestWrong)
	w := h.post("/api/v1/auth/login/2fa/passkey/finish",
		passkeyFinishBody(pv.tempToken, pv.sessionToken, craftAssertion(t, pv, correctOrigin, correctRPHash, correctChallenge)))
	require.NotEqual(t, http.StatusOK, w.Code, w.Body.String())
	require.NotContains(t, w.Body.String(), "access_token")
}

// ---- 4. Passkey finish：伪装会话与真实会话的错误码一致 ----

type assertionVariant int

const (
	correctChallenge assertionVariant = iota
	wrongChallenge
	correctOrigin
	wrongOrigin
	correctRPHash
	wrongRPHash
)

// craftAssertion 构造一份结构完整、只有签名一定验不过的 assertion（攻击者不需要认证器就能做到的最强探测）。
func craftAssertion(t *testing.T, vars challengeVars, origin, rpHash, challenge assertionVariant) string {
	t.Helper()
	require.NotEmpty(t, vars.allowedIDs)
	challengeValue := vars.challenge
	if challenge == wrongChallenge {
		challengeValue = base64.RawURLEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	}
	originValue := disguiseTestOrigin
	if origin == wrongOrigin {
		originValue = "https://evil.example.test"
	}
	hashInput := disguiseTestRPID
	if rpHash == wrongRPHash {
		hashInput = "evil.example.test"
	}
	clientData, err := json.Marshal(map[string]string{"type": "webauthn.get", "challenge": challengeValue, "origin": originValue})
	require.NoError(t, err)
	sum := sha256.Sum256([]byte(hashInput))
	authData := append(append([]byte{}, sum[:]...), 0x05, 0, 0, 0, 1) // UP|UV + signCount
	rawID := base64.RawURLEncoding.EncodeToString(vars.allowedIDs[0])
	raw, err := json.Marshal(map[string]any{
		"id": rawID, "rawId": rawID, "type": "public-key",
		"response": map[string]string{
			"clientDataJSON":    base64.RawURLEncoding.EncodeToString(clientData),
			"authenticatorData": base64.RawURLEncoding.EncodeToString(authData),
			"signature":         base64.RawURLEncoding.EncodeToString([]byte("not-a-real-signature")),
		},
	})
	require.NoError(t, err)
	return string(raw)
}

func TestDecoyPasskeyFinishMatchesRealSessionErrors(t *testing.T) {
	h := newDisguiseHarness(t, "true")
	secret := h.cfg.JWT.Secret
	passkeyOnly := h.emailOf(disguiseUserPasskeyID)
	hmacPasskey := findDisguiseEmail(t, secret, shapePasskey, 0)

	// 真实会话（密码对）与两种伪装会话（镜像账号 / HMAC 邮箱）各自走同一份探测。
	type subject struct {
		name     string
		email    string
		password string
	}
	subjects := []subject{
		{"real", passkeyOnly, disguiseTestPassword},
		{"decoy mirroring the real account", passkeyOnly, disguiseTestWrong},
		{"decoy for an email without a real shape", hmacPasskey, disguiseTestWrong},
	}

	variants := []struct {
		name  string
		build func(t *testing.T, vars challengeVars) (sessionToken, credential string)
	}{
		{"garbage credential", func(_ *testing.T, v challengeVars) (string, string) {
			return v.sessionToken, `{"id":"AA","rawId":"AA","type":"public-key","response":{}}`
		}},
		{"unknown credential id", func(t *testing.T, v challengeVars) (string, string) {
			other := v
			other.allowedIDs = [][]byte{[]byte("some-other-credential-id")}
			return v.sessionToken, craftAssertion(t, other, correctOrigin, correctRPHash, correctChallenge)
		}},
		{"wrong challenge", func(t *testing.T, v challengeVars) (string, string) {
			return v.sessionToken, craftAssertion(t, v, correctOrigin, correctRPHash, wrongChallenge)
		}},
		{"wrong origin", func(t *testing.T, v challengeVars) (string, string) {
			return v.sessionToken, craftAssertion(t, v, wrongOrigin, correctRPHash, correctChallenge)
		}},
		{"wrong rp id hash", func(t *testing.T, v challengeVars) (string, string) {
			return v.sessionToken, craftAssertion(t, v, correctOrigin, wrongRPHash, correctChallenge)
		}},
		{"well-formed assertion with a bad signature", func(t *testing.T, v challengeVars) (string, string) {
			return v.sessionToken, craftAssertion(t, v, correctOrigin, correctRPHash, correctChallenge)
		}},
		{"malformed session token", func(_ *testing.T, _ challengeVars) (string, string) {
			return "garbage", `{"id":"AA","rawId":"AA","type":"public-key","response":{}}`
		}},
		{"well-formed but unknown session token", func(_ *testing.T, _ challengeVars) (string, string) {
			return "v1.login_2fa.7." + strings.Repeat("A", 43), `{"id":"AA","rawId":"AA","type":"public-key","response":{}}`
		}},
	}

	transcripts := map[string][]transcriptEntry{}
	for _, variant := range variants {
		for _, s := range subjects {
			h.limiter.clear()
			vars := h.firstStep(s.email, s.password)
			sessionToken, credential := variant.build(t, vars)
			var entries []transcriptEntry
			entries = append(entries, record(h.post("/api/v1/auth/login/2fa/passkey/finish", passkeyFinishBody(vars.tempToken, sessionToken, credential))))
			// 一次性：同一个 ceremony 第二次提交，真实会话和伪装会话都说「已用过 / 已过期」。
			entries = append(entries, record(h.post("/api/v1/auth/login/2fa/passkey/finish", passkeyFinishBody(vars.tempToken, sessionToken, credential))))
			transcripts[variant.name+"/"+s.name] = entries
			require.NotContains(t, entries[0].body, "access_token")
		}
		realTranscript := transcripts[variant.name+"/real"]
		for _, s := range subjects[1:] {
			require.Equal(t, realTranscript, transcripts[variant.name+"/"+s.name], "变体「%s」：%s 必须与真实会话一致", variant.name, s.name)
		}
	}

	// 对照：错误码确实有区分度（探测脚本不是空转）。
	require.Contains(t, transcripts["wrong origin/real"][0].body, "PASSKEY_ORIGIN_MISMATCH")
	require.Contains(t, transcripts["wrong challenge/real"][0].body, "PASSKEY_CHALLENGE_MISMATCH")
	require.Contains(t, transcripts["wrong rp id hash/real"][0].body, "PASSKEY_RP_ID_MISMATCH")
	require.Contains(t, transcripts["well-formed assertion with a bad signature/real"][0].body, "PASSKEY_VERIFICATION_FAILED")
	require.Contains(t, transcripts["garbage credential/real"][1].body, "PASSKEY_")
}

// 诱饵 ceremony 被塞给别的登录会话：与「同一用户的另一个真实 ceremony」被塞进去时的表现一致。
// 攻击者可以用必错的密码拿一个已知是诱饵的 ceremony，再把它塞给「真假未知」的会话，看会话「认不认得」它；
// 真实会话认得的条件是「ceremony 的用户 ID 段 = 登录会话的用户 ID」，伪装会话必须用同一个条件。
func TestDecoyCeremonyCrossUseMatchesRealSessionBehavior(t *testing.T) {
	h := newDisguiseHarness(t, "true")
	passkeyEmail := h.emailOf(disguiseUserPasskeyID)
	bothEmail := h.emailOf(disguiseUserBothID)
	hmacPasskey := findDisguiseEmail(t, h.cfg.JWT.Secret, shapePasskey, 0)

	probes := []struct{ name, targetEmail, sourceEmail string }{
		{"decoy ceremony of the same account", passkeyEmail, passkeyEmail},
		{"decoy ceremony of another account into a totp+passkey session", bothEmail, passkeyEmail},
		{"decoy ceremony of an unknown email into the passkey account's session", passkeyEmail, hmacPasskey},
	}
	variants := []struct {
		name  string
		build func(t *testing.T, source challengeVars) string
	}{
		{"garbage credential", func(*testing.T, challengeVars) string {
			return `{"id":"AA","rawId":"AA","type":"public-key","response":{}}`
		}},
		{"well-formed assertion with a bad signature", func(t *testing.T, source challengeVars) string {
			return craftAssertion(t, source, correctOrigin, correctRPHash, correctChallenge)
		}},
		{"wrong challenge", func(t *testing.T, source challengeVars) string {
			return craftAssertion(t, source, correctOrigin, correctRPHash, wrongChallenge)
		}},
	}

	results := map[string]transcriptEntry{}
	for _, probe := range probes {
		for _, variant := range variants {
			run := func(targetPassword string) transcriptEntry {
				h.limiter.clear()
				target := h.firstStep(probe.targetEmail, targetPassword)
				source := h.firstStep(probe.sourceEmail, disguiseTestWrong) // 必错的密码：保证它是诱饵
				body := passkeyFinishBody(target.tempToken, source.sessionToken, variant.build(t, source))
				return record(h.post("/api/v1/auth/login/2fa/passkey/finish", body))
			}
			realEntry := run(disguiseTestPassword)
			decoyEntry := run(disguiseTestWrong)
			require.Equal(t, realEntry, decoyEntry, "%s / %s：真假未知的会话拿到诱饵 ceremony 时必须表现一致", probe.name, variant.name)
			require.NotContains(t, realEntry.body, "access_token")
			results[probe.name+"/"+variant.name] = realEntry
		}
	}

	// 对照：探测脚本不是空转，并且确实走了不同的校验路径。
	require.Contains(t, results["decoy ceremony of another account into a totp+passkey session/garbage credential"].body, "PASSKEY_SESSION_INVALID")
	require.Contains(t, results["decoy ceremony of an unknown email into the passkey account's session/garbage credential"].body, "PASSKEY_SESSION_INVALID")
	require.Contains(t, results["decoy ceremony of the same account/wrong challenge"].body, "PASSKEY_CHALLENGE_MISMATCH")
	require.Contains(t, results["decoy ceremony of the same account/well-formed assertion with a bad signature"].body, "PASSKEY_VERIFICATION_FAILED")
}

// 没有要求 Passkey 的伪装会话（[totp]）在 Passkey finish 上与真实的 [totp] 会话一致；
// [passkey] 伪装会话的 TOTP 步骤与真实 [passkey] 会话一致（TOTP_NOT_SETUP，见 transcript 测试）。
func TestDecoyTOTPOnlySessionRejectsPasskeyFinishLikeRealOne(t *testing.T) {
	h := newDisguiseHarness(t, "true")
	email := h.emailOf(disguiseUserTOTPID)
	run := func(password string) transcriptEntry {
		h.limiter.clear()
		vars := h.firstStep(email, password)
		return record(h.post("/api/v1/auth/login/2fa/passkey/finish",
			passkeyFinishBody(vars.tempToken, "v1.login_2fa.21."+strings.Repeat("A", 43), `{"id":"AA","rawId":"AA","type":"public-key","response":{}}`)))
	}
	realEntry := run(disguiseTestPassword)
	require.Equal(t, http.StatusBadRequest, realEntry.status)
	require.Contains(t, realEntry.body, "No passkey step is required")
	require.Equal(t, realEntry, run(disguiseTestWrong))
}

// ---- 4b. 库读次数：诱饵路径与真实路径做同样多的点查（耗时量级一致的确定性证据） ----

func TestDecoyPasskeyPathsDoTheSameNumberOfDBReadsAsTheRealPath(t *testing.T) {
	h := newDisguiseHarness(t, "true")
	total := func(f func()) int64 {
		before := h.dbReads()
		f()
		after := h.dbReads()
		var n int64
		for i := range after {
			n += after[i] - before[i]
		}
		return n
	}
	passkeyEmail := h.emailOf(disguiseUserPasskeyID)
	hmacPasskey := findDisguiseEmail(t, h.cfg.JWT.Secret, shapePasskey, 0)

	// 首步：真实挑战 / 镜像诱饵 / HMAC 诱饵。
	realStep1 := total(func() { h.login(passkeyEmail, disguiseTestPassword) })
	require.EqualValues(t, 5, realStep1, "真实首步：按邮箱查用户 + 凭据列表 + begin（用户行 + user handle + 凭据列表）")
	require.Equal(t, realStep1, total(func() { h.login(passkeyEmail, disguiseTestWrong) }), "镜像诱饵")
	require.Equal(t, realStep1, total(func() { h.login(hmacPasskey, disguiseTestWrong) }), "HMAC 诱饵")

	// passkey finish：真实会话读 用户行 + user handle + 凭据列表；两类诱饵同样读三次。
	finishReads := func(email, password string) int64 {
		vars := h.firstStep(email, password)
		body := passkeyFinishBody(vars.tempToken, vars.sessionToken, craftAssertion(t, vars, correctOrigin, correctRPHash, correctChallenge))
		h.limiter.clear()
		return total(func() { h.post("/api/v1/auth/login/2fa/passkey/finish", body) })
	}
	realFinish := finishReads(passkeyEmail, disguiseTestPassword)
	require.EqualValues(t, 3, realFinish)
	require.Equal(t, realFinish, finishReads(passkeyEmail, disguiseTestWrong), "镜像诱饵")
	require.Equal(t, realFinish, finishReads(hmacPasskey, disguiseTestWrong), "HMAC 诱饵")
}

// ---- 4c. 首步对 5 分钟因子凭证 Cookie 的处理 ----

// 攻击者可以在首步请求里带一个假的因子凭证 Cookie（名字是公开常量）。真实路径在「密码正确」时会校验它，
// 不过就在响应里清掉这个 Cookie，并且受「每用户 + IP 每分钟 10 次」的校验限速——「有没有 Set-Cookie 清除头」
// 因此是一个独立于响应体的「密码是否正确 + 账号要求哪些因子」的信号。伪装挑战必须做同样的事。
func TestDecoyFirstStepClearsPresentedGrantCookiesLikeTheRealPath(t *testing.T) {
	bogus := []*http.Cookie{
		{Name: service.TwoFactorGrantCookieName, Value: "bogus-totp-grant"},
		{Name: service.TwoFactorPasskeyGrantCookieName, Value: "bogus-passkey-grant"},
	}
	totpCleared := service.TwoFactorGrantCookieName + ":-1"
	passkeyCleared := service.TwoFactorPasskeyGrantCookieName + ":-1"
	const attempts = 12 // 校验限速是每分钟 10 次校验，12 次请求足以越过它

	for _, tc := range []struct {
		name      string
		realID    int64
		hmacShape []service.Factor
		cleared   []string
	}{
		{"totp only", disguiseUserTOTPID, shapeTOTP, []string{totpCleared}},
		{"passkey only", disguiseUserPasskeyID, shapePasskey, []string{passkeyCleared}},
		{"totp + passkey", disguiseUserBothID, shapeTOTPPass, []string{totpCleared, passkeyCleared}}, // 已按名字排序
	} {
		t.Run(tc.name, func(t *testing.T) {
			// 每条路径用全新的夹具，校验限速的计数从 0 开始。
			run := func(pickEmail func(h *disguiseHarness) string, password string) [][]string {
				h := newDisguiseHarness(t, "true")
				var out [][]string
				for i := 0; i < attempts; i++ {
					out = append(out, setCookieSummary(h.loginWithCookies(pickEmail(h), password, bogus...)))
				}
				return out
			}
			realRun := run(func(h *disguiseHarness) string { return h.emailOf(tc.realID) }, disguiseTestPassword)
			mirrorRun := run(func(h *disguiseHarness) string { return h.emailOf(tc.realID) }, disguiseTestWrong)
			hmacRun := run(func(h *disguiseHarness) string {
				return findDisguiseEmail(t, h.cfg.JWT.Secret, tc.hmacShape, 0)
			}, disguiseTestWrong)

			// 真实路径本身：校验限速按「校验次数」算（每个被账号要求的因子、每个请求各一次，每分钟 10 次），
			// 额度内清掉账号要求的那些因子的 Cookie，之后限速、不再清；没有被账号要求的因子的 Cookie 不碰。
			clearingRequests := 10 / len(tc.cleared)
			for i := 0; i < attempts; i++ {
				if i < clearingRequests {
					require.Equal(t, tc.cleared, realRun[i], "真实路径第 %d 次", i+1)
				} else {
					require.Empty(t, realRun[i], "真实路径第 %d 次已被校验限速", i+1)
				}
			}
			require.Equal(t, realRun, mirrorRun, "镜像诱饵必须与真实路径清同样的 Cookie、同样的限速节奏")
			require.Equal(t, realRun, hmacRun, "同形状的 HMAC 诱饵必须与真实账号清同样的 Cookie、同样的限速节奏")
		})
	}

	// 不带 Cookie：任何路径都不下发 Set-Cookie。
	h := newDisguiseHarness(t, "true")
	for _, id := range []int64{disguiseUserTOTPID, disguiseUserPasskeyID, disguiseUserBothID} {
		require.Empty(t, setCookieSummary(h.loginWithCookies(h.emailOf(id), disguiseTestPassword)))
		require.Empty(t, setCookieSummary(h.loginWithCookies(h.emailOf(id), disguiseTestWrong)))
	}
}

// ---- 5. 开关读取 ----

func TestLoginShapeDisguiseSwitchParsing(t *testing.T) {
	for _, tc := range []struct {
		name   string
		value  string
		errKey bool
		want   bool
	}{
		{"unset", "", false, false},
		{"true", "true", false, true},
		{"false", "false", false, false},
		{"garbage", "yes", false, false},
		{"read error fails to legacy", "true", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newDisguiseHarness(t, tc.value)
			if tc.errKey {
				h.settings.errKey = service.SettingKeyLoginShapeDisguiseEnabled
			}
			w := h.login(h.emailOf(disguiseUserPasskeyID), disguiseTestWrong)
			if tc.want {
				require.Equal(t, http.StatusOK, w.Code, w.Body.String())
				require.Equal(t, `required=["passkey"] next=passkey passkey=true`, disguiseShapeOf(w.Body.String()))
			} else {
				require.Equal(t, http.StatusUnauthorized, w.Code, w.Body.String())
				require.Contains(t, w.Body.String(), "INVALID_CREDENTIALS")
				require.NotContains(t, w.Body.String(), "requires_2fa")
			}
		})
	}
}

// 没有可用密钥（JWT secret 为空）时即使开关打开也退回默认 401，不会拿空密钥做伪装。
func TestLoginShapeDisguiseWithoutSecretFallsBackToLegacy(t *testing.T) {
	h := newDisguiseHarness(t, "true")
	h.handler.disguise = service.NewLoginDisguise("")
	w := h.login(h.emailOf(disguiseUserPasskeyID), disguiseTestWrong)
	require.Equal(t, http.StatusUnauthorized, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "INVALID_CREDENTIALS")
	require.NotContains(t, w.Body.String(), "requires_2fa")
}

// 打开开关后又关闭：已经下发的伪装会话仍按伪装处理（会话自带标记），不会掉进旧分支露出差异。
func TestDecoySessionsSurviveSwitchBeingTurnedOff(t *testing.T) {
	h := newDisguiseHarness(t, "true")
	email := h.emailOf(disguiseUserPasskeyID)
	vars := h.firstStep(email, disguiseTestWrong)

	h.settings.values[service.SettingKeyLoginShapeDisguiseEnabled] = "false"
	w := h.post("/api/v1/auth/login/2fa", login2FABody(vars.tempToken, email, disguiseTestWrong, "000000"))
	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "TOTP_NOT_SETUP", "单 Passkey 会话在 TOTP 步骤的真实表现")
	// 并且没有被旧分支删掉。
	again := h.post("/api/v1/auth/login/2fa", login2FABody(vars.tempToken, email, disguiseTestWrong, "000000"))
	require.Equal(t, http.StatusBadRequest, again.Code, again.Body.String())
}

// ---- 6. 耗时（默认不跑；LOGIN_DISGUISE_TIMING=1 时打印分布，PR 描述里的数字来自这里） ----

func TestLoginShapeDisguiseTimingReport(t *testing.T) {
	if testing.Short() || os.Getenv("LOGIN_DISGUISE_TIMING") != "1" {
		t.Skip("set LOGIN_DISGUISE_TIMING=1 to measure")
	}
	h := newDisguiseHarness(t, "true")
	const n = 40
	measure := func(f func()) []time.Duration {
		out := make([]time.Duration, 0, n)
		for i := 0; i < n; i++ {
			start := time.Now()
			f()
			out = append(out, time.Since(start))
		}
		return out
	}
	passkeyEmail := h.emailOf(disguiseUserPasskeyID)
	hmacPasskey := findDisguiseEmail(t, h.cfg.JWT.Secret, shapePasskey, 0)
	report := func(name string, samples []time.Duration) {
		sorted := append([]time.Duration(nil), samples...)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
		t.Logf("%-52s p50=%-10v p90=%-10v max=%v", name, sorted[len(sorted)/2], sorted[len(sorted)*9/10], sorted[len(sorted)-1])
	}
	countReads := func(name string, f func()) {
		before := h.dbReads()
		f()
		after := h.dbReads()
		t.Logf("%-52s DB point reads: user-by-email=%d user-by-id=%d passkey-list=%d user-handle=%d", name,
			after[0]-before[0], after[1]-before[1], after[2]-before[2], after[3]-before[3])
	}
	countReads("step1 real passkey account, correct password", func() { h.login(passkeyEmail, disguiseTestPassword) })
	countReads("step1 mirrored decoy, wrong password", func() { h.login(passkeyEmail, disguiseTestWrong) })
	countReads("step1 HMAC passkey decoy, unknown email", func() { h.login(hmacPasskey, disguiseTestWrong) })
	countReads("step1 real totp account, correct password", func() { h.login(h.emailOf(disguiseUserTOTPID), disguiseTestPassword) })
	countReads("step1 mirrored totp decoy, wrong password", func() { h.login(h.emailOf(disguiseUserTOTPID), disguiseTestWrong) })
	countReads("step1 HMAC decoy, unknown email (totp shape)", func() { h.login(findDisguiseEmail(t, h.cfg.JWT.Secret, shapeTOTP, 0), disguiseTestWrong) })
	report("step1 real passkey account, correct password", measure(func() { h.login(passkeyEmail, disguiseTestPassword) }))
	report("step1 mirrored decoy, wrong password", measure(func() { h.login(passkeyEmail, disguiseTestWrong) }))
	report("step1 HMAC passkey decoy, unknown email", measure(func() { h.login(hmacPasskey, disguiseTestWrong) }))
	report("step1 legacy fixed decoy (switch off), wrong password", func() []time.Duration {
		legacy := newDisguiseHarness(t, "false")
		return measure(func() { legacy.login(passkeyEmail, disguiseTestWrong) })
	}())

	realVars := h.firstStep(passkeyEmail, disguiseTestPassword)
	decoyVars := h.firstStep(passkeyEmail, disguiseTestWrong)
	report("step2 TOTP endpoint, real session", measure(func() {
		h.limiter.clear()
		h.post("/api/v1/auth/login/2fa", login2FABody(realVars.tempToken, passkeyEmail, disguiseTestPassword, "000000"))
	}))
	report("step2 TOTP endpoint, decoy session", measure(func() {
		h.limiter.clear()
		h.post("/api/v1/auth/login/2fa", login2FABody(decoyVars.tempToken, passkeyEmail, disguiseTestWrong, "000000"))
	}))
	// passkey finish 只计时 finish 请求本身（会话先批量建好，避免把首步的 bcrypt 算进来）。
	// 单次不到计时器分辨率，取 200 次的平均值。
	const finishRuns = 200
	prepare := func(password string) []challengeVars {
		sessions := make([]challengeVars, 0, finishRuns)
		for i := 0; i < finishRuns; i++ {
			h.limiter.clear()
			sessions = append(sessions, h.firstStep(passkeyEmail, password))
		}
		return sessions
	}
	finishMean := func(sessions []challengeVars) time.Duration {
		bodies := make([]string, len(sessions))
		for i, vars := range sessions {
			bodies[i] = passkeyFinishBody(vars.tempToken, vars.sessionToken, craftAssertion(t, vars, correctOrigin, correctRPHash, correctChallenge))
		}
		start := time.Now()
		for _, body := range bodies {
			h.limiter.clear()
			h.post("/api/v1/auth/login/2fa/passkey/finish", body)
		}
		return time.Since(start) / time.Duration(len(bodies))
	}
	t.Logf("%-52s mean over %d = %v", "step2 passkey finish request only, real session", finishRuns, finishMean(prepare(disguiseTestPassword)))
	t.Logf("%-52s mean over %d = %v", "step2 passkey finish request only, decoy session", finishRuns, finishMean(prepare(disguiseTestWrong)))
}
