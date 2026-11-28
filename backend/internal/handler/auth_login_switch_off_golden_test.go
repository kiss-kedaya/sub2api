package handler

// 登录首步 / 第二步在 login_shape_disguise_enabled 关闭（缺失 / false / 乱值）时的黄金快照。
//
// 150 个场景（首步：未知邮箱 / 各种因子形状的账号 / 被禁用账号 × 对错密码 × 带不带假凭证 Cookie，各种畸形请求；
// 第二步：无效会话 / 真实会话 × 自然重放 / 乱填密码 / 不带密码 / 换邮箱 / 超限 / Passkey finish），
// 每个场景记录状态码、响应头、Set-Cookie 与响应体。开关关闭时凭据错误一律返回 401 INVALID_CREDENTIALS
// （不弹 2FA 框），且账号不存在与各形态账号密码错误逐字节一致。
//
// 重新生成（仅当明确调整默认登录响应行为时）：
//
//	LOGIN_GOLDEN_WRITE=testdata/login_switch_off.golden.txt go test ./internal/handler -run TestLoginSwitchOffMatchesPreDisguiseGolden
//
// 全部邮箱 / 密码 / 凭据 ID 都是假数据。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/repository" //nolint:depguard // 与 PR 夹具一致：真实 Passkey 会话存储。
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/redis/go-redis/v9" //nolint:depguard // 同上。
	"github.com/stretchr/testify/require"
)

const (
	goldenSwitchKey = "login_shape_disguise_enabled" // 字面量：旧树里没有对应的常量
	goldenFile      = "testdata/login_switch_off.golden.txt"

	goldenPassword = "correct-password"
	goldenWrong    = "wrong-password"
)

type goldenUserRepo struct {
	service.UserRepository
	users []*service.User
}

func (r *goldenUserRepo) GetByEmail(_ context.Context, email string) (*service.User, error) {
	for _, u := range r.users {
		if strings.EqualFold(strings.TrimSpace(email), u.Email) {
			return u, nil
		}
	}
	return nil, service.ErrUserNotFound
}

func (r *goldenUserRepo) GetByID(_ context.Context, id int64) (*service.User, error) {
	for _, u := range r.users {
		if u.ID == id {
			return u, nil
		}
	}
	return nil, service.ErrUserNotFound
}

func (r *goldenUserRepo) GetUserAvatar(context.Context, int64) (*service.UserAvatar, error) {
	return nil, nil
}

func (r *goldenUserRepo) Update(context.Context, *service.User, service.UserUpdateFields) error {
	return nil
}

type goldenPasskeyRepo struct {
	service.PasskeyRepository
	records map[int64][]service.PasskeyCredentialRecord
}

func (r *goldenPasskeyRepo) ListByUserID(_ context.Context, id int64) ([]service.PasskeyCredentialRecord, error) {
	return r.records[id], nil
}

func (r *goldenPasskeyRepo) GetUserHandle(_ context.Context, id int64) ([]byte, error) {
	if len(r.records[id]) == 0 {
		return nil, service.ErrPasskeyNotFound
	}
	return []byte(fmt.Sprintf("golden-handle-%d-padding-to-32-bytes!!", id)), nil
}

// goldenLimiter 同口径：同一个键最多放行 5 次，之后 429（Retry-After 固定 30 秒）；Reset 清零。
type goldenLimiter struct {
	counts map[int64]int
}

func (l *goldenLimiter) Check(_ context.Context, key int64) (bool, time.Duration, error) {
	l.counts[key]++
	if l.counts[key] > 5 {
		return false, 30 * time.Second, nil
	}
	return true, 0, nil
}

func (l *goldenLimiter) Reset(_ context.Context, key int64) { delete(l.counts, key) }

// goldenFixture 是整个快照共用的账号与 Passkey 凭据（bcrypt 哈希只做一次）。场景里没有任何一步会登录成功，账号不会被改写。
type goldenFixture struct {
	repo *goldenUserRepo
	pk   *goldenPasskeyRepo
}

func newGoldenFixture(t *testing.T) *goldenFixture {
	t.Helper()
	pk := &goldenPasskeyRepo{records: map[int64][]service.PasskeyCredentialRecord{}}
	repo := &goldenUserRepo{}
	add := func(id int64, totpOn, disabled bool, creds ...webauthn.Credential) {
		u, _, _ := newTwoFactorGrantTestUser(t, id, goldenPassword, totpOn)
		if disabled {
			u.Status = service.StatusDisabled
		}
		repo.users = append(repo.users, u)
		for i, c := range creds {
			pk.records[id] = append(pk.records[id], service.PasskeyCredentialRecord{ID: id*100 + int64(i), UserID: id, UserHandle: []byte("h"), Credential: c})
		}
	}
	add(21, true, false)
	add(22, false, false, webauthn.Credential{ID: []byte("passkey-only-credential-id-32-bt"), Transport: []protocol.AuthenticatorTransport{protocol.Hybrid, protocol.Internal}})
	add(23, true, false, webauthn.Credential{ID: []byte("both-factors-credential-id-1-32b"), Transport: []protocol.AuthenticatorTransport{protocol.Internal}})
	add(24, false, false)
	add(25, true, true) // 被禁用的账号
	return &goldenFixture{repo: repo, pk: pk}
}

type goldenHarness struct {
	router   *gin.Engine
	settings *loginTOTPSettingRepo
}

// newGoldenHarness 每次都是全新的限速计数 / 会话存储 / 设置，只共用 fx 里的账号。
func newGoldenHarness(t *testing.T, fx *goldenFixture, extra map[string]string) *goldenHarness {
	t.Helper()
	gin.SetMode(gin.TestMode)
	cfg := newTwoFactorGrantTestConfig()
	cfg.WebAuthn = config.WebAuthnConfig{Enabled: true, RPDisplayName: "Sub2API", RPID: "example.test", RPOrigins: []string{"https://example.test"}}

	h := &goldenHarness{settings: &loginTOTPSettingRepo{values: map[string]string{service.SettingKeyTotpEnabled: "true"}}}
	for k, v := range extra {
		h.settings.values[k] = v
	}
	repo, pk := fx.repo, fx.pk

	rs := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: rs.Addr()})
	t.Cleanup(func() { _ = rc.Close() })

	settingSvc := service.NewSettingService(h.settings, cfg)
	authSvc := service.NewAuthService(nil, repo, nil, twoFactorGrantRefreshCache{}, cfg, settingSvc, nil, nil, nil, nil, nil, nil, nil)
	totpSvc := service.NewTotpService(repo, twoFactorGrantTestEncryptor{}, newTwoFactorGrantCacheStub(), settingSvc, nil, nil)
	totpSvc.SetPasskeyRepository(pk)
	userSvc := service.NewUserService(repo, nil, nil, nil)
	pkSvc, err := service.NewPasskeyService(cfg, pk, repository.NewPasskeySessionStore(rc), repo)
	require.NoError(t, err)
	ah := NewAuthHandler(cfg, authSvc, userSvc, settingSvc, nil, nil, totpSvc, nil)
	ah.SetPasskeyService(pkSvc)
	ah.SetTwoFactorRateLimiter(&goldenLimiter{counts: map[int64]int{}})
	h.router = gin.New()
	h.router.POST("/api/v1/auth/login", ah.Login)
	h.router.POST("/api/v1/auth/login/2fa", ah.Login2FA)
	h.router.POST("/api/v1/auth/login/2fa/passkey/finish", ah.Login2FAPasskeyFinish)
	return h
}

// 抹掉随机值（temp_token / 令牌 / challenge / session token 的随机段 / 过期时间），其余逐字节保留。
var (
	goldenReTemp  = regexp.MustCompile(`[0-9a-f]{64}`)
	goldenReJWT   = regexp.MustCompile(`eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`)
	goldenReSess  = regexp.MustCompile(`(v1\.login_2fa\.\d+)\.[A-Za-z0-9_-]{43}`)
	goldenReChal  = regexp.MustCompile(`"challenge":"[A-Za-z0-9_-]{43}"`)
	goldenReRefr  = regexp.MustCompile(`"(refresh_token|access_token)":"[^"]*"`)
	goldenReTime  = regexp.MustCompile(`"expires_at":\d+`)
	goldenReTempT = regexp.MustCompile(`"temp_token":"[^"]*"`)
)

func goldenNormalize(s string) string {
	s = goldenReTempT.ReplaceAllString(s, `"temp_token":"<T>"`)
	s = goldenReRefr.ReplaceAllString(s, `"$1":"<TOK>"`)
	s = goldenReJWT.ReplaceAllString(s, "<JWT>")
	s = goldenReSess.ReplaceAllString(s, "$1.<N>")
	s = goldenReChal.ReplaceAllString(s, `"challenge":"<C>"`)
	s = goldenReTime.ReplaceAllString(s, `"expires_at":<TS>`)
	s = goldenReTemp.ReplaceAllString(s, "<HEX64>")
	return s
}

func (h *goldenHarness) do(path, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "golden-agent")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	return w
}

func goldenRender(w *httptest.ResponseRecorder) string {
	var hdr []string
	for k, v := range w.Header() {
		if k == "X-Request-Id" || k == "Date" {
			continue
		}
		hdr = append(hdr, k+"="+strings.Join(v, "|"))
	}
	sort.Strings(hdr)
	var ck []string
	for _, c := range w.Result().Cookies() {
		ck = append(ck, fmt.Sprintf("%s;max-age=%d;httponly=%t;secure=%t;path=%s;samesite=%d;valnonempty=%t", c.Name, c.MaxAge, c.HttpOnly, c.Secure, c.Path, c.SameSite, c.Value != ""))
	}
	sort.Strings(ck)
	return fmt.Sprintf("status=%d\n  headers=%v\n  cookies=%v\n  body=%s", w.Code, hdr, ck, goldenNormalize(w.Body.String()))
}

func goldenBody(email, pw string) string {
	raw, _ := json.Marshal(map[string]string{"email": email, "password": pw})
	return string(raw)
}

// goldenRun 跑完整的场景脚本并返回渲染结果。每个首步场景用全新的夹具（限速计数从 0 开始）。
func goldenRun(t *testing.T, fx *goldenFixture, extra map[string]string) string {
	t.Helper()
	var out strings.Builder
	emit := func(name string, w *httptest.ResponseRecorder) {
		_, _ = out.WriteString("## " + name + "\n  " + goldenRender(w) + "\n")
	}
	bogus := []*http.Cookie{
		{Name: service.TwoFactorGrantCookieName, Value: "bogus-totp"},
		{Name: service.TwoFactorPasskeyGrantCookieName, Value: "bogus-passkey"},
	}
	users := map[string]string{
		"unknown":        "nobody@example.test",
		"unknown-case":   "  NoBody@Example.TEST ",
		"totp":           "user-21@example.test",
		"passkey":        "user-22@example.test",
		"both":           "user-23@example.test",
		"none":           "user-24@example.test",
		"disabled":       "user-25@example.test",
		"totp-uppercase": "USER-21@EXAMPLE.TEST",
	}
	names := []string{"unknown", "unknown-case", "totp", "totp-uppercase", "passkey", "both", "none", "disabled"}

	// ---- 首步 ----
	for _, mode := range []string{"plain", "bogus-cookies"} {
		for _, nm := range names {
			for _, pw := range []string{goldenWrong, goldenPassword} {
				h := newGoldenHarness(t, fx, extra)
				var ck []*http.Cookie
				if mode == "bogus-cookies" {
					ck = bogus
				}
				emit(fmt.Sprintf("step1 %s %s pw=%s", mode, nm, pw), h.do("/api/v1/auth/login", goldenBody(users[nm], pw), ck...))
			}
		}
	}
	h := newGoldenHarness(t, fx, extra)
	emit("step1 empty body", h.do("/api/v1/auth/login", ""))
	emit("step1 malformed json", h.do("/api/v1/auth/login", "{"))
	emit("step1 missing password", h.do("/api/v1/auth/login", `{"email":"user-21@example.test"}`))
	emit("step1 missing email", h.do("/api/v1/auth/login", `{"password":"x"}`))
	emit("step1 invalid email shape", h.do("/api/v1/auth/login", goldenBody("not-an-email", goldenWrong)))
	emit("step1 empty strings", h.do("/api/v1/auth/login", goldenBody("", "")))
	emit("step1 very long email", h.do("/api/v1/auth/login", goldenBody(strings.Repeat("a", 400)+"@example.test", goldenWrong)))

	// totp_enabled 关闭：没有任何 TOTP 登录挑战
	off := map[string]string{service.SettingKeyTotpEnabled: "false"}
	for k, v := range extra {
		off[k] = v
	}
	h2 := newGoldenHarness(t, fx, off)
	emit("step1 totp_enabled=false unknown", h2.do("/api/v1/auth/login", goldenBody(users["unknown"], goldenWrong)))
	emit("step1 totp_enabled=false totp wrong", h2.do("/api/v1/auth/login", goldenBody(users["totp"], goldenWrong)))

	// 设置读取失败：disguise 开关关闭时凭据错误仍直接返回 401 INVALID_CREDENTIALS
	h3 := newGoldenHarness(t, fx, extra)
	h3.settings.errKey = service.SettingKeyTotpEnabled
	emit("step1 totp_enabled read error unknown", h3.do("/api/v1/auth/login", goldenBody(users["unknown"], goldenWrong)))

	// ---- 第二步：旧诱饵会话 / 真实会话 / 未知 token ----
	tokenOf := func(w *httptest.ResponseRecorder) string {
		var env struct {
			Data struct {
				TempToken string `json:"temp_token"`
			} `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &env)
		return env.Data.TempToken
	}
	step2 := func(label, email, pw string) {
		h := newGoldenHarness(t, fx, extra)
		tok := tokenOf(h.do("/api/v1/auth/login", goldenBody(email, pw)))
		for _, sc := range []struct{ name, body string }{
			{"with-pw", fmt.Sprintf(`{"temp_token":%q,"totp_code":"000000","email":%q,"password":%q}`, tok, email, pw)},
			{"with-pw-again", fmt.Sprintf(`{"temp_token":%q,"totp_code":"000000","email":%q,"password":%q}`, tok, email, pw)},
			{"no-pw", fmt.Sprintf(`{"temp_token":%q,"totp_code":"000000"}`, tok)},
			{"email-mismatch", fmt.Sprintf(`{"temp_token":%q,"totp_code":"000000","email":"other@example.test","password":%q}`, tok, pw)},
			{"bad-code-len", fmt.Sprintf(`{"temp_token":%q,"totp_code":"12","email":%q,"password":%q}`, tok, email, pw)},
		} {
			emit("step2 "+label+" "+sc.name, h.do("/api/v1/auth/login/2fa", sc.body))
		}
		for i := 0; i < 7; i++ { // 走到限速
			w := h.do("/api/v1/auth/login/2fa", fmt.Sprintf(`{"temp_token":%q,"totp_code":"000000","email":%q,"password":%q}`, tok, email, pw))
			emit(fmt.Sprintf("step2 %s burst#%d", label, i+1), w)
		}
		emit("step2 "+label+" passkey-finish", h.do("/api/v1/auth/login/2fa/passkey/finish",
			fmt.Sprintf(`{"temp_token":%q,"session_token":"v1.login_2fa.22.x","credential":{"id":"x"}}`, tok)))
	}
	for _, nm := range []string{"unknown", "totp", "passkey", "both", "none"} {
		step2(nm+"/wrong", users[nm], goldenWrong)
		if nm != "unknown" && nm != "none" {
			step2(nm+"/correct", users[nm], goldenPassword)
		}
	}
	h4 := newGoldenHarness(t, fx, extra)
	emit("step2 unknown temp token", h4.do("/api/v1/auth/login/2fa", `{"temp_token":"deadbeef","totp_code":"000000"}`))
	emit("step2 passkey-finish unknown temp token", h4.do("/api/v1/auth/login/2fa/passkey/finish", `{"temp_token":"deadbeef","session_token":"x","credential":{"id":"x"}}`))
	emit("step2 empty body", h4.do("/api/v1/auth/login/2fa", ""))
	emit("step2 passkey-finish empty credential", h4.do("/api/v1/auth/login/2fa/passkey/finish", `{"temp_token":"t","session_token":"s"}`))
	return out.String()
}

func TestLoginSwitchOffMatchesPreDisguiseGolden(t *testing.T) {
	writePath := os.Getenv("LOGIN_GOLDEN_WRITE")
	variants := []struct {
		name  string
		extra map[string]string
	}{
		{"switch-absent", nil},
		{"switch-false", map[string]string{goldenSwitchKey: "false"}},
		{"switch-garbage", map[string]string{goldenSwitchKey: "yes"}},
	}
	var want string
	if writePath == "" {
		raw, err := os.ReadFile(goldenFile)
		require.NoError(t, err)
		// Windows 上 core.autocrlf=true 的检出会把快照改成 CRLF，比较前统一成 LF。
		want = strings.ReplaceAll(string(raw), "\r\n", "\n")
	}
	var first string
	fx := newGoldenFixture(t)
	for _, v := range variants {
		t.Run(v.name, func(t *testing.T) {
			got := goldenRun(t, fx, v.extra)
			if writePath != "" {
				// 三个变体在旧树上输出完全一致：第一个变体写文件，其余变体必须与它相同。
				if first == "" {
					first = got
					require.NoError(t, os.WriteFile(writePath, []byte(got), 0o644))
					return
				}
				require.Equal(t, first, got, "旧树上三个变体的输出应当一致")
				return
			}
			require.Equal(t, want, got, "开关关闭（%s）时登录响应必须与 #659 之前的快照逐字节一致", v.name)
		})
	}
}
