//go:build unit

package middleware

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

// access token 路径上的会话绑定：指纹不一致一律 401（前端据此刷新令牌）；
// 是否同时撤销会话家族由 security.session_binding_on_mismatch 决定。

const (
	bindingTestSecret  = "test-binding-secret-32bytes-long!!!"
	bindUAChrome130Win = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36"
	bindUAChrome131Win = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
	bindUAFirefoxLinux = "Mozilla/5.0 (X11; Linux x86_64; rv:132.0) Gecko/20100101 Firefox/132.0"
)

// familyRecordingCache 只记录被撤销的会话家族；登录路径之外的方法用不到。
type familyRecordingCache struct {
	service.RefreshTokenCache
	mu      sync.Mutex
	deleted []string
}

func (c *familyRecordingCache) DeleteTokenFamily(_ context.Context, familyID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deleted = append(c.deleted, familyID)
	return nil
}

func (c *familyRecordingCache) revokedFamilies() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.deleted...)
}

type bindingEnforceEnv struct {
	router *gin.Engine
	auth   *service.AuthService
	cache  *familyRecordingCache
	user   *service.User
}

func newBindingEnforceEnv(t *testing.T, onMismatch string) *bindingEnforceEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{}
	cfg.JWT.Secret = bindingTestSecret
	cfg.JWT.AccessTokenExpireMinutes = 60
	cfg.Security.SessionBindingMode = config.SessionBindingModeLoose
	cfg.Security.SessionBindingOnMismatch = onMismatch

	user := &service.User{ID: 9, Email: "bind@example.com", Role: "user", Status: service.StatusActive, Concurrency: 1, TokenVersion: 1}
	cache := &familyRecordingCache{}
	userRepo := &stubJWTUserRepo{users: map[int64]*service.User{user.ID: user}}
	authSvc := service.NewAuthService(nil, userRepo, nil, cache, cfg, nil, nil, nil, nil, nil, nil, nil, nil)
	userSvc := service.NewUserService(userRepo, nil, nil, nil)
	// 绑定开关键缺失 = 默认开启
	settingSvc := service.NewSettingService(fakeSettingRepo{values: map[string]string{}}, cfg)

	r := gin.New()
	r.Use(SessionBindingContext(cfg))
	r.Use(jwtAuth(authSvc, userSvc, nil, settingSvc, nil))
	r.GET("/protected", func(c *gin.Context) { c.Status(http.StatusOK) })
	return &bindingEnforceEnv{router: r, auth: authSvc, cache: cache, user: user}
}

func (e *bindingEnforceEnv) tokenFor(t *testing.T, ip, ua string) string {
	t.Helper()
	ctx := service.WithSessionBinding(context.Background(), &service.SessionBinding{IP: ip, UserAgent: ua})
	token, err := e.auth.GenerateToken(ctx, e.user)
	require.NoError(t, err)
	return token
}

func (e *bindingEnforceEnv) call(token, ip, ua string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.RemoteAddr = net.JoinHostPort(ip, "4433")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", ua)
	e.router.ServeHTTP(w, req)
	return w
}

// signLegacyBindingToken 签一枚升级前的 access token：只有旧算法的绑定哈希，没有粗粒度指纹。
func signLegacyBindingToken(t *testing.T, e *bindingEnforceEnv, bindingHash string) string {
	t.Helper()
	claims, err := e.auth.ValidateToken(e.tokenFor(t, "203.0.113.10", bindUAChrome130Win))
	require.NoError(t, err)
	claims.BindingHash = bindingHash
	claims.BindingCoarse = ""
	claims.BindingVersion = 1
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(bindingTestSecret))
	require.NoError(t, err)
	return token
}

func errorCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Code string `json:"code"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	return body.Code
}

// 默认 rebind：换网络/换浏览器的请求返回 401（前端会刷新），但不撤销会话家族——用户不会因此掉线。
func TestEnforceSessionBindingRebindModeReturns401WithoutRevokingTheSession(t *testing.T) {
	e := newBindingEnforceEnv(t, config.SessionBindingOnMismatchRebind)
	token := e.tokenFor(t, "203.0.113.10", bindUAChrome130Win)

	require.Equal(t, http.StatusOK, e.call(token, "203.0.113.10", bindUAChrome130Win).Code)

	w := e.call(token, "198.51.100.77", bindUAFirefoxLinux)
	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.Equal(t, "SESSION_BINDING_MISMATCH", errorCode(t, w))
	require.Contains(t, w.Body.String(), "refresh the token", "the message must say a refresh fixes it, not that a re-login is needed")
	require.Empty(t, e.cache.revokedFamilies(), "rebind mode must not revoke the session family")

	stats := e.auth.AuthStatsSnapshot()
	require.EqualValues(t, 1, stats["access_binding_mismatch"])
	require.Zero(t, stats["access_binding_revoke"])
}

// revoke 模式保留旧行为：撤销整个会话家族，提示重新登录。
func TestEnforceSessionBindingRevokeModeRevokesTheSessionFamily(t *testing.T) {
	e := newBindingEnforceEnv(t, config.SessionBindingOnMismatchRevoke)
	token := e.tokenFor(t, "203.0.113.10", bindUAChrome130Win)

	w := e.call(token, "198.51.100.77", bindUAChrome130Win)
	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.Equal(t, "SESSION_BINDING_MISMATCH", errorCode(t, w))
	require.Contains(t, w.Body.String(), "login again")
	require.Len(t, e.cache.revokedFamilies(), 1)

	stats := e.auth.AuthStatsSnapshot()
	require.EqualValues(t, 1, stats["access_binding_mismatch"])
	require.EqualValues(t, 1, stats["access_binding_revoke"])
}

// 浏览器小版本升级、同 /24 漂移、IPv4 <-> IPv6 切换都不算不一致：access token 照常放行，
// 即使是 revoke 模式也不撤销。靠忽略 IP 家族才通过的请求（IPv4 <-> IPv6）单独计数。
func TestEnforceSessionBindingLooseModeToleratesVersionBumpsAndIPFamilySwitch(t *testing.T) {
	cases := []struct {
		name           string
		loginIP, ua    string
		requestIP, ua2 string
		familySwitch   bool
	}{
		{"browser version bump", "203.0.113.10", bindUAChrome130Win, "203.0.113.10", bindUAChrome131Win, false},
		{"same /24 drift", "203.0.113.10", bindUAChrome130Win, "203.0.113.200", bindUAChrome130Win, false},
		{"ipv4 to ipv6", "203.0.113.10", bindUAChrome130Win, "2001:db8:abcd:12::1", bindUAChrome130Win, true},
		{"ipv6 to ipv4", "2001:db8:abcd:12::1", bindUAChrome130Win, "198.51.100.77", bindUAChrome130Win, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newBindingEnforceEnv(t, config.SessionBindingOnMismatchRevoke)
			token := e.tokenFor(t, tc.loginIP, tc.ua)
			require.Equal(t, http.StatusOK, e.call(token, tc.requestIP, tc.ua2).Code)
			require.Empty(t, e.cache.revokedFamilies())
			stats := e.auth.AuthStatsSnapshot()
			require.Zero(t, stats["access_binding_mismatch"])
			if tc.familySwitch {
				require.EqualValues(t, 1, stats["access_ip_family_switch"], "passing only because the IP family differs is counted")
			} else {
				require.Zero(t, stats["access_ip_family_switch"])
			}
		})
	}
}

// 跨 IP 家族时 IP 不参与比较，但浏览器家族 + 系统家族必须一致：换了 IP 家族又换了系统/浏览器，照样 401。
func TestEnforceSessionBindingAcrossIPFamiliesStillRequiresTheSameBrowserAndOSFamily(t *testing.T) {
	e := newBindingEnforceEnv(t, config.SessionBindingOnMismatchRebind)
	token := e.tokenFor(t, "203.0.113.10", bindUAChrome130Win)

	w := e.call(token, "2001:db8:dead:beef::1", bindUAFirefoxLinux)
	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.EqualValues(t, 1, e.auth.AuthStatsSnapshot()["access_binding_mismatch"])
	require.Zero(t, e.auth.AuthStatsSnapshot()["access_ip_family_switch"])
}

// 升级前签发的 access token（没有粗粒度指纹、旧算法哈希）：同一个客户端照常放行；
// 指纹对不上时 rebind 模式只返回 401，不撤销。
func TestEnforceSessionBindingLegacyTokensKeepWorking(t *testing.T) {
	e := newBindingEnforceEnv(t, config.SessionBindingOnMismatchRebind)
	binding := &service.SessionBinding{IP: "203.0.113.10", UserAgent: bindUAChrome130Win}
	legacy := signLegacyBindingToken(t, e, binding.HashForMode(config.SessionBindingModeLoose))

	require.Equal(t, http.StatusOK, e.call(legacy, "203.0.113.10", bindUAChrome130Win).Code, "same client as before the upgrade")
	require.Equal(t, http.StatusUnauthorized, e.call(legacy, "203.0.113.10", bindUAChrome131Win).Code,
		"a legacy token has no coarse fingerprint, so a UA version bump still mismatches until the next refresh rebinds it")
	require.Empty(t, e.cache.revokedFamilies())
}
