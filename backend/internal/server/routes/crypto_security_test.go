package routes

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/pquerna/otp/totp"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

const cryptoRouteTotpSecret = "JBSWY3DPEHPK3PXP"

type cryptoRouteUserRepo struct {
	service.UserRepository
	user *service.User
}

func (r *cryptoRouteUserRepo) GetByID(context.Context, int64) (*service.User, error) {
	return r.user, nil
}
func (r *cryptoRouteUserRepo) UpdateTotpSecret(_ context.Context, _ int64, encrypted *string) error {
	r.user.TotpSecretEncrypted = encrypted
	return nil
}
func (r *cryptoRouteUserRepo) EnableTotp(context.Context, int64) error {
	r.user.TotpEnabled = true
	now := time.Now()
	r.user.TotpEnabledAt = &now
	return nil
}

func (r *cryptoRouteUserRepo) BindTotpIfDisabled(_ context.Context, _ int64, encrypted string) error {
	if r.user.TotpEnabled || r.user.Status != service.StatusActive || r.user.DeletedAt != nil || encrypted == "" {
		return service.ErrUserSecurityStateChanged
	}
	r.user.TotpSecretEncrypted = &encrypted
	r.user.TotpEnabled = true
	now := time.Now()
	r.user.TotpEnabledAt = &now
	return nil
}
func (r *cryptoRouteUserRepo) DisableTotp(context.Context, int64) error {
	r.user.TotpEnabled = false
	r.user.TotpSecretEncrypted = nil
	r.user.TotpEnabledAt = nil
	return nil
}

func (r *cryptoRouteUserRepo) DisableTotpWithExpectedRole(_ context.Context, _ int64, expectedRole string, expectedSecret *string) error {
	if r.user.Role != expectedRole || r.user.Status != service.StatusActive || r.user.DeletedAt != nil || !r.user.TotpEnabled ||
		(r.user.TotpSecretEncrypted == nil) != (expectedSecret == nil) ||
		(expectedSecret != nil && *r.user.TotpSecretEncrypted != *expectedSecret) {
		return service.ErrUserSecurityStateChanged
	}
	return r.DisableTotp(context.Background(), r.user.ID)
}

type cryptoRouteSettingsRepo struct {
	service.SettingRepository
	values map[string]string
}

func (r *cryptoRouteSettingsRepo) GetValue(_ context.Context, key string) (string, error) {
	if value, ok := r.values[key]; ok {
		return value, nil
	}
	return "", service.ErrSettingNotFound
}

type cryptoRouteTotpCache struct {
	service.TotpCache
	rdb      *redis.Client
	mu       sync.Mutex
	attempts int
	fail     bool
	setup    *service.TotpSetupSession
}

func (c *cryptoRouteTotpCache) GetSetupSession(context.Context, int64) (*service.TotpSetupSession, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fail {
		return nil, errors.New("cache unavailable")
	}
	if c.setup == nil {
		return nil, nil
	}
	copy := *c.setup
	return &copy, nil
}
func (c *cryptoRouteTotpCache) SetSetupSession(_ context.Context, _ int64, session *service.TotpSetupSession, _ time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fail {
		return errors.New("cache unavailable")
	}
	copy := *session
	c.setup = &copy
	return nil
}
func (c *cryptoRouteTotpCache) DeleteSetupSession(context.Context, int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fail {
		return errors.New("cache unavailable")
	}
	c.setup = nil
	return nil
}

func (c *cryptoRouteTotpCache) GetVerifyAttempts(context.Context, int64) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fail {
		return 0, errors.New("cache unavailable")
	}
	return c.attempts, nil
}
func (c *cryptoRouteTotpCache) IncrementVerifyAttempts(context.Context, int64) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fail {
		return 0, errors.New("cache unavailable")
	}
	c.attempts++
	return c.attempts, nil
}
func (c *cryptoRouteTotpCache) ClearVerifyAttempts(context.Context, int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fail {
		return errors.New("cache unavailable")
	}
	c.attempts = 0
	return nil
}

func (c *cryptoRouteTotpCache) IncrementCryptoAttempt(ctx context.Context, key string, ttl time.Duration) (int, error) {
	count, err := redis.NewScript(`
local n = redis.call('INCR', KEYS[1])
if n == 1 then redis.call('PEXPIRE', KEYS[1], ARGV[1]) end
if redis.call('PTTL', KEYS[1]) < 0 then return -1 end
return n
`).Run(ctx, c.rdb, []string{key}, ttl.Milliseconds()).Int()
	return count, err
}

func (c *cryptoRouteTotpCache) SetCryptoValueIfAbsent(ctx context.Context, key, value string, ttl time.Duration) (bool, error) {
	return c.rdb.SetNX(ctx, key, value, ttl).Result()
}

func (c *cryptoRouteTotpCache) CryptoValueExists(ctx context.Context, key string) (bool, error) {
	count, err := c.rdb.Exists(ctx, key).Result()
	return count != 0, err
}

func (c *cryptoRouteTotpCache) ReleaseCryptoValue(ctx context.Context, key, value string) error {
	return redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then return redis.call('DEL', KEYS[1]) end
return 0
`).Run(ctx, c.rdb, []string{key}, value).Err()
}

type cryptoRouteEncryptor struct{}

func (cryptoRouteEncryptor) Encrypt(value string) (string, error) {
	return "enc:" + base64.StdEncoding.EncodeToString([]byte(value)), nil
}
func (cryptoRouteEncryptor) Decrypt(value string) (string, error) {
	if strings.HasPrefix(value, "enc:") {
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(value, "enc:"))
		if err != nil {
			return "", err
		}
		return string(decoded), nil
	}
	return value, nil
}

type cryptoRouteHarness struct {
	router   *gin.Engine
	redis    *redis.Client
	mini     *miniredis.Miniredis
	user     *service.User
	settings *service.SettingService
	cache    *cryptoRouteTotpCache
	identity *cryptoRouteIdentity
}

type cryptoRouteIdentity struct {
	authenticated bool
	method        string
	claimRole     string
	sessionID     string
}

func newCryptoRouteHarness(t *testing.T, user *service.User) *cryptoRouteHarness {
	t.Helper()
	t.Setenv("CRYPTO_ADMIN_USER_IDS", "41,75,94")
	gin.SetMode(gin.TestMode)
	mini := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	settings := service.NewSettingService(&cryptoRouteSettingsRepo{values: map[string]string{
		service.SettingKeyTotpEnabled:   "false",
		service.SettingKeyStepUpEnabled: "false",
	}}, nil)
	userRepo := &cryptoRouteUserRepo{user: user}
	cache := &cryptoRouteTotpCache{rdb: rdb}
	totpService := service.NewTotpService(userRepo, cryptoRouteEncryptor{}, cache, settings, nil, nil)
	cfg := &config.Config{Totp: config.TotpConfig{
		EncryptionKey:           strings.Repeat("a", 64),
		EncryptionKeyConfigured: true,
	}}
	cryptoHandler := handler.NewCryptoHandler(nil, settings, nil, totpService, cfg)

	router := gin.New()
	identity := &cryptoRouteIdentity{authenticated: true, method: service.AuditAuthMethodJWT, claimRole: service.RoleAdmin, sessionID: "route-test-session"}
	admin := router.Group("/api/v1/admin")
	admin.Use(func(c *gin.Context) {
		if identity.authenticated {
			c.Set("auth_method", identity.method)
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: user.ID})
			c.Set(string(middleware.ContextKeyUserRole), identity.claimRole)
			c.Set(middleware.ContextKeySessionID, identity.sessionID)
		}
		c.Next()
	})
	RegisterCryptoAdminRoutes(admin, cryptoHandler)
	harness := &cryptoRouteHarness{router: router, redis: rdb, mini: mini, user: user, settings: settings, cache: cache, identity: identity}
	return harness
}

func TestCryptoNewAdministratorCannotEnrollOrUsePaymentAuthority(t *testing.T) {
	user := activeCryptoAdmin()
	user.ID = 42 // An admin created or promoted through ordinary admin APIs.
	h := newCryptoRouteHarness(t, user)
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/security/status"},
		{http.MethodPost, "/security/setup"},
		{http.MethodPost, "/security/enable"},
		{http.MethodPost, "/wallet/export-mnemonics"},
		{http.MethodPut, "/config"},
	} {
		r := cryptoRouteRequest(h.router, route.method, "/api/v1/admin/crypto"+route.path, "123456", nil, "127.0.0.1:1000", "localhost")
		requireCryptoReason(t, r, http.StatusForbidden, "CRYPTO_OPERATOR_REQUIRED")
	}
}

func activeCryptoAdmin() *service.User {
	secret := cryptoRouteTotpSecret
	return &service.User{
		ID: 41, Email: "admin@example.test", Role: service.RoleAdmin,
		Status: service.StatusActive, TotpEnabled: true, TotpSecretEncrypted: &secret,
	}
}

func cryptoRouteRequest(router http.Handler, method, path, code string, headers map[string]string, peer, host string) *httptest.ResponseRecorder {
	return cryptoRouteRequestBody(router, method, path, `{"passphrase":"test"}`, code, headers, peer, host)
}

func cryptoRouteRequestBody(router http.Handler, method, path, body, code string, headers map[string]string, peer, host string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = peer
	req.Host = host
	req.Header.Set("Authorization", "Bearer route-test-token")
	if code != "" {
		req.Header.Set("X-Crypto-TOTP", code)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	return recorder
}

func requireCryptoReason(t *testing.T, response *httptest.ResponseRecorder, status int, reason string) {
	t.Helper()
	require.Equal(t, status, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), reason)
}

func wrongCryptoRouteCode(t *testing.T, offset time.Duration) string {
	t.Helper()
	valid, err := totp.GenerateCode(cryptoRouteTotpSecret, time.Now())
	require.NoError(t, err)
	wrong, err := totp.GenerateCode("KRUGS4ZANFZSAYJA", time.Now().Add(offset))
	require.NoError(t, err)
	if wrong == valid {
		var value int
		_, err := fmt.Sscanf(wrong, "%d", &value)
		require.NoError(t, err)
		wrong = fmt.Sprintf("%06d", (value+1)%1000000)
	}
	return wrong
}

func expiredCryptoRouteCode(t *testing.T) string {
	t.Helper()
	valid, err := totp.GenerateCode(cryptoRouteTotpSecret, time.Now())
	require.NoError(t, err)
	old, err := totp.GenerateCode(cryptoRouteTotpSecret, time.Now().Add(-10*time.Minute))
	require.NoError(t, err)
	if old == valid {
		old = wrongCryptoRouteCode(t, 0)
	}
	return old
}

func TestCryptoAdminRegisteredRoutesEnforceLiveJWTAndTotp(t *testing.T) {
	cases := []struct {
		name   string
		edit   func(*service.User, *cryptoRouteHarness)
		method string
		code   string
		want   int
		reason string
	}{
		{name: "normal user despite admin JWT claim", edit: func(u *service.User, _ *cryptoRouteHarness) { u.Role = service.RoleUser }, want: http.StatusForbidden, reason: "CRYPTO_ADMIN_REQUIRED"},
		{name: "inactive admin", edit: func(u *service.User, _ *cryptoRouteHarness) { u.Status = service.StatusDisabled }, want: http.StatusForbidden, reason: "CRYPTO_ADMIN_REQUIRED"},
		{name: "demoted admin", edit: func(u *service.User, _ *cryptoRouteHarness) { u.Role = service.RoleUser }, want: http.StatusForbidden, reason: "CRYPTO_ADMIN_REQUIRED"},
		{name: "unbound admin", edit: func(u *service.User, _ *cryptoRouteHarness) { u.TotpEnabled = false; u.TotpSecretEncrypted = nil }, want: http.StatusForbidden, reason: "CRYPTO_TOTP_NOT_SETUP"},
		{name: "bad code", code: wrongCryptoRouteCode(t, 0), want: http.StatusForbidden, reason: "CRYPTO_TOTP_REQUIRED"},
		{name: "expired code", code: expiredCryptoRouteCode(t), want: http.StatusForbidden, reason: "CRYPTO_TOTP_REQUIRED"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CRYPTO_INSTANCE_ENABLED", "true")
			t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
			user := activeCryptoAdmin()
			h := newCryptoRouteHarness(t, user)
			if tc.edit != nil {
				tc.edit(user, h)
			}
			method := tc.method
			if method == "" {
				method = http.MethodPost
			}
			path := "/api/v1/admin/crypto/wallet/export-mnemonics"
			response := cryptoRouteRequest(h.router, method, path, tc.code, nil, "127.0.0.1:1234", "localhost")
			requireCryptoReason(t, response, tc.want, tc.reason)
		})
	}
}

func TestCryptoAdminRejectsUnauthenticatedAndAdminAPIKeyEvenWithCode(t *testing.T) {
	for _, endpoint := range []string{
		"/api/v1/admin/crypto/wallet/export-mnemonics",
		"/api/v1/admin/crypto/wallet/backup",
	} {
		for _, tc := range []struct {
			name   string
			method string
		}{
			{"unauthenticated", ""},
			{"admin api key", service.AuditAuthMethodAdminAPIKey},
		} {
			t.Run(endpoint+"/"+tc.name, func(t *testing.T) {
				t.Setenv("CRYPTO_INSTANCE_ENABLED", "true")
				h := newCryptoRouteHarness(t, activeCryptoAdmin())
				h.identity.authenticated = tc.method != ""
				h.identity.method = tc.method
				response := cryptoRouteRequest(h.router, http.MethodPost, endpoint, "123456", map[string]string{"X-API-Key": "machine"}, "127.0.0.1:1", "localhost")
				requireCryptoReason(t, response, http.StatusForbidden, "CRYPTO_ADMIN_REQUIRED")
			})
		}
	}
}

func TestCryptoAdminEncryptedVaultBackupRouteRequiresFreshTotp(t *testing.T) {
	t.Setenv("CRYPTO_INSTANCE_ENABLED", "true")
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	h := newCryptoRouteHarness(t, activeCryptoAdmin())
	path := "/api/v1/admin/crypto/wallet/backup"
	body := `{"passphrase":"fixture-only"}`

	bad := cryptoRouteRequestBody(h.router, http.MethodPost, path, body, wrongCryptoRouteCode(t, 0), nil, "127.0.0.1:1234", "localhost")
	requireCryptoReason(t, bad, http.StatusForbidden, "CRYPTO_TOTP_REQUIRED")
	require.NotContains(t, bad.Body.String(), "fixture-only")

	code, err := totp.GenerateCode(cryptoRouteTotpSecret, time.Now())
	require.NoError(t, err)
	valid := cryptoRouteRequestBody(h.router, http.MethodPost, path, body, code, nil, "127.0.0.1:1234", "localhost")
	// The harness deliberately has no wallet; 503 proves the authorized request
	// reached the sensitive backup handler instead of returning a route 404.
	require.Equal(t, http.StatusServiceUnavailable, valid.Code, valid.Body.String())
	require.NotContains(t, valid.Body.String(), "fixture-only")
	if cacheControl := valid.Header().Get("Cache-Control"); !strings.Contains(cacheControl, "no-store") {
		t.Fatalf("expected no-store response, got %q", cacheControl)
	}
}

func TestCryptoAdminRiskRoutesConsumeOtpAndFailClosed(t *testing.T) {
	t.Setenv("CRYPTO_INSTANCE_ENABLED", "true")
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	user := activeCryptoAdmin()
	h := newCryptoRouteHarness(t, user)
	code, err := totp.GenerateCode(cryptoRouteTotpSecret, time.Now())
	require.NoError(t, err)
	path := "/api/v1/admin/crypto/wallet/export-mnemonics"
	first := cryptoRouteRequest(h.router, http.MethodPost, path, code, nil, "127.0.0.1:1234", "localhost")
	// The test handler has no wallet; reaching it proves the route guard accepted one valid code.
	require.Equal(t, http.StatusServiceUnavailable, first.Code, first.Body.String())
	second := cryptoRouteRequest(h.router, http.MethodPost, path, code, nil, "127.0.0.1:1234", "localhost")
	requireCryptoReason(t, second, http.StatusConflict, "CRYPTO_TOTP_REPLAYED")

	concurrent := newCryptoRouteHarness(t, activeCryptoAdmin())
	concurrentCode, err := totp.GenerateCode(cryptoRouteTotpSecret, time.Now())
	require.NoError(t, err)
	start := make(chan struct{})
	statuses := make(chan int, 2)
	var group sync.WaitGroup
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			response := cryptoRouteRequest(concurrent.router, http.MethodPost, path, concurrentCode, nil, "127.0.0.1:1234", "localhost")
			statuses <- response.Code
		}()
	}
	close(start)
	group.Wait()
	close(statuses)
	resultCounts := map[int]int{}
	for status := range statuses {
		resultCounts[status]++
	}
	require.Equal(t, 1, resultCounts[http.StatusServiceUnavailable], resultCounts)
	require.Equal(t, 1, resultCounts[http.StatusConflict], resultCounts)

	bad := newCryptoRouteHarness(t, activeCryptoAdmin())
	bad.mini.Close()
	failed := cryptoRouteRequest(bad.router, http.MethodPost, path, code, nil, "127.0.0.1:1234", "localhost")
	requireCryptoReason(t, failed, http.StatusServiceUnavailable, "CRYPTO_TOTP_REQUIRED")
}

func TestCryptoAdminRiskVerificationRateLimitsInvalidCodes(t *testing.T) {
	t.Setenv("CRYPTO_INSTANCE_ENABLED", "true")
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	h := newCryptoRouteHarness(t, activeCryptoAdmin())
	path := "/api/v1/admin/crypto/wallet/export-mnemonics"

	for range 5 {
		response := cryptoRouteRequest(h.router, http.MethodPost, path, wrongCryptoRouteCode(t, 0), nil, "127.0.0.1:1234", "localhost")
		requireCryptoReason(t, response, http.StatusForbidden, "CRYPTO_TOTP_REQUIRED")
	}
	limited := cryptoRouteRequest(h.router, http.MethodPost, path, wrongCryptoRouteCode(t, 0), nil, "127.0.0.1:1234", "localhost")
	requireCryptoReason(t, limited, http.StatusTooManyRequests, "CRYPTO_TOTP_RATE_LIMITED")
}

func TestCryptoAdminEveryRiskRouteRejectsInvalidTotp(t *testing.T) {
	t.Setenv("CRYPTO_INSTANCE_ENABLED", "true")
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	cases := []struct {
		method string
		path   string
	}{
		{http.MethodPut, "/api/v1/admin/crypto/config"},
		{http.MethodPost, "/api/v1/admin/crypto/addresses/1/status"},
		{http.MethodPost, "/api/v1/admin/crypto/sweep/run"},
		{http.MethodPost, "/api/v1/admin/crypto/wallet/unlock"},
		{http.MethodPost, "/api/v1/admin/crypto/wallet/backup"},
		{http.MethodPost, "/api/v1/admin/crypto/wallet/export-mnemonics"},
		{http.MethodPost, "/api/v1/admin/crypto/wallet/hot-mnemonic"},
		{http.MethodPost, "/api/v1/admin/crypto/rpc/test"},
		{http.MethodPost, "/api/v1/admin/crypto/rpc/auto-pick"},
		{http.MethodGet, "/api/v1/admin/crypto/config"},
		{http.MethodGet, "/api/v1/admin/crypto/deposits.csv"},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			h := newCryptoRouteHarness(t, activeCryptoAdmin())
			response := cryptoRouteRequestBody(h.router, tc.method, tc.path, `{}`, wrongCryptoRouteCode(t, 0), nil, "127.0.0.1:1234", "localhost")
			requireCryptoReason(t, response, http.StatusForbidden, "CRYPTO_TOTP_REQUIRED")
		})
	}
}

func TestCryptoAdminRejectsForwardedSchemeAndClientIpSpoofing(t *testing.T) {
	t.Setenv("CRYPTO_INSTANCE_ENABLED", "true")
	for _, headers := range []map[string]string{
		{"X-Forwarded-Proto": "https"},
		{"X-Forwarded-For": "127.0.0.1"},
		{"Forwarded": "for=127.0.0.1;proto=https;host=localhost"},
	} {
		h := newCryptoRouteHarness(t, activeCryptoAdmin())
		response := cryptoRouteRequest(h.router, http.MethodPost, "/api/v1/admin/crypto/wallet/export-mnemonics", "123456", headers, "203.0.113.9:443", "kedaya.ai")
		requireCryptoReason(t, response, http.StatusForbidden, "CRYPTO_HTTPS_REQUIRED")
	}
}

func TestCryptoAdminInstanceGateAndSensitiveCsvRead(t *testing.T) {
	h := newCryptoRouteHarness(t, activeCryptoAdmin())
	t.Setenv("CRYPTO_INSTANCE_ENABLED", "false")
	response := cryptoRouteRequest(h.router, http.MethodPost, "/api/v1/admin/crypto/wallet/export-mnemonics", "123456", nil, "127.0.0.1:1234", "localhost")
	requireCryptoReason(t, response, http.StatusServiceUnavailable, "CRYPTO_DISABLED")

	t.Setenv("CRYPTO_INSTANCE_ENABLED", "true")
	noCode := cryptoRouteRequest(h.router, http.MethodGet, "/api/v1/admin/crypto/deposits.csv", "", nil, "127.0.0.1:1234", "localhost")
	requireCryptoReason(t, noCode, http.StatusForbidden, "CRYPTO_TOTP_REQUIRED")
	status := cryptoRouteRequest(h.router, http.MethodGet, "/api/v1/admin/crypto/security/status", "", nil, "127.0.0.1:1234", "localhost")
	require.Equal(t, http.StatusOK, status.Code, status.Body.String())
	require.Contains(t, status.Header().Get("Cache-Control"), "no-store")
	require.Contains(t, status.Body.String(), `"feature_enabled":true`)
}

func TestCryptoAdminConfigResponseIsNotCacheable(t *testing.T) {
	t.Setenv("CRYPTO_INSTANCE_ENABLED", "true")
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	h := newCryptoRouteHarness(t, activeCryptoAdmin())
	noCode := cryptoRouteRequest(h.router, http.MethodGet, "/api/v1/admin/crypto/config", "", nil, "127.0.0.1:1234", "localhost")
	requireCryptoReason(t, noCode, http.StatusForbidden, "CRYPTO_TOTP_REQUIRED")
	code, err := totp.GenerateCode(cryptoRouteTotpSecret, time.Now())
	require.NoError(t, err)
	response := cryptoRouteRequest(h.router, http.MethodGet, "/api/v1/admin/crypto/config", code, nil, "127.0.0.1:1234", "localhost")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	require.Equal(t, "no-cache", response.Header().Get("Pragma"))
}

func TestCryptoStandaloneSignerSurfaceHasBeenRemoved(t *testing.T) {
	t.Setenv("CRYPTO_INSTANCE_ENABLED", "true")
	h := newCryptoRouteHarness(t, activeCryptoAdmin())
	for _, route := range h.router.Routes() {
		require.NotContains(t, route.Path, "/crypto/signer/")
		require.NotContains(t, route.Path, "/crypto/sign/")
	}
	for _, path := range []string{"/api/v1/admin/crypto/signer/unlock", "/api/v1/admin/crypto/signer/backup", "/api/v1/admin/crypto/sign/evm"} {
		response := cryptoRouteRequest(h.router, http.MethodPost, path, "", nil, "127.0.0.1:1234", "localhost")
		require.Equal(t, http.StatusNotFound, response.Code, path)
	}
}

func TestCryptoSensitiveAdminReadsRequireSecureTransport(t *testing.T) {
	t.Setenv("CRYPTO_INSTANCE_ENABLED", "true")
	for _, path := range []string{"/api/v1/admin/crypto/config", "/api/v1/admin/crypto/wallet/status", "/api/v1/admin/crypto/hot-wallet/address"} {
		h := newCryptoRouteHarness(t, activeCryptoAdmin())
		response := cryptoRouteRequest(h.router, http.MethodGet, path, "", map[string]string{"X-Forwarded-Proto": "https"}, "203.0.113.9:443", "kedaya.ai")
		requireCryptoReason(t, response, http.StatusForbidden, "CRYPTO_HTTPS_REQUIRED")
	}
}

func TestCryptoAdminTestModeIsReadOnlyAndRequiresProductionTwoFactorEnrollment(t *testing.T) {
	t.Setenv("CRYPTO_INSTANCE_ENABLED", "true")
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "true")
	user := &service.User{ID: 94, Email: "admin@example.test", Role: service.RoleAdmin, Status: service.StatusActive}
	require.NoError(t, user.SetPassword("initial-binding-password"))
	h := newCryptoRouteHarness(t, user)

	for _, tc := range []struct {
		name   string
		method string
		path   string
	}{
		{"config write", http.MethodPut, "/api/v1/admin/crypto/config"},
		{"address status", http.MethodPost, "/api/v1/admin/crypto/addresses/1/status"},
		{"manual sweep", http.MethodPost, "/api/v1/admin/crypto/sweep/run"},
		{"wallet unlock", http.MethodPost, "/api/v1/admin/crypto/wallet/unlock"},
		{"encrypted vault backup", http.MethodPost, "/api/v1/admin/crypto/wallet/backup"},
		{"mnemonic export", http.MethodPost, "/api/v1/admin/crypto/wallet/export-mnemonics"},
		{"hot mnemonic import", http.MethodPost, "/api/v1/admin/crypto/wallet/hot-mnemonic"},
		{"rpc test", http.MethodPost, "/api/v1/admin/crypto/rpc/test"},
		{"rpc auto-pick", http.MethodPost, "/api/v1/admin/crypto/rpc/auto-pick"},
		{"sensitive csv", http.MethodGet, "/api/v1/admin/crypto/deposits.csv"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := cryptoRouteRequestBody(h.router, tc.method, tc.path, `{}`, "", nil, "127.0.0.1:1234", "localhost")
			requireCryptoReason(t, response, http.StatusForbidden, "CRYPTO_TEST_MODE_READ_ONLY")
		})
	}

	status := cryptoRouteRequestBody(h.router, http.MethodGet, "/api/v1/admin/crypto/security/status", "", "", nil, "127.0.0.1:1234", "localhost")
	require.Equal(t, http.StatusOK, status.Code, status.Body.String())
	setup := cryptoRouteRequestBody(h.router, http.MethodPost, "/api/v1/admin/crypto/security/setup",
		`{}`, "", nil, "127.0.0.1:1234", "localhost")
	requireCryptoReason(t, setup, http.StatusForbidden, "CRYPTO_TEST_MODE_READ_ONLY")
}

func TestCryptoSecuritySetupAndEnableHTTPFlowIgnoresGlobalTotpSwitch(t *testing.T) {
	t.Setenv("CRYPTO_INSTANCE_ENABLED", "true")
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	user := &service.User{ID: 75, Email: "admin@example.test", Role: service.RoleAdmin, Status: service.StatusActive}
	require.NoError(t, user.SetPassword("first-binding-password"))
	h := newCryptoRouteHarness(t, user)
	setup := cryptoRouteRequestBody(h.router, http.MethodPost, "/api/v1/admin/crypto/security/setup",
		`{}`, "", nil, "127.0.0.1:1234", "localhost")
	require.Equal(t, http.StatusOK, setup.Code, setup.Body.String())
	require.Contains(t, setup.Header().Get("Cache-Control"), "no-store")
	var setupEnvelope struct {
		Data struct {
			Secret     string `json:"secret"`
			QRCodeURL  string `json:"qr_code_url"`
			SetupToken string `json:"setup_token"`
			Countdown  int    `json:"countdown"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(setup.Body.Bytes(), &setupEnvelope))
	require.NotEmpty(t, setupEnvelope.Data.Secret)
	require.NotEmpty(t, setupEnvelope.Data.QRCodeURL)
	require.NotEmpty(t, setupEnvelope.Data.SetupToken)
	require.NotContains(t, h.cache.setup.Secret, setupEnvelope.Data.Secret)
	require.True(t, h.cache.setup.SecretEncrypted)

	code, err := totp.GenerateCode(setupEnvelope.Data.Secret, time.Now())
	require.NoError(t, err)
	body := fmt.Sprintf(`{"totp_code":%q,"setup_token":%q}`, code, setupEnvelope.Data.SetupToken)
	h.identity.sessionID = "different-session"
	wrongSession := cryptoRouteRequestBody(h.router, http.MethodPost, "/api/v1/admin/crypto/security/enable", body, "", nil, "127.0.0.1:1234", "localhost")
	requireCryptoReason(t, wrongSession, http.StatusForbidden, "CRYPTO_TOTP_REQUIRED")

	h.identity.sessionID = "route-test-session"
	badCode := cryptoRouteRequestBody(h.router, http.MethodPost, "/api/v1/admin/crypto/security/enable",
		fmt.Sprintf(`{"totp_code":%q,"setup_token":%q}`, wrongCryptoRouteCode(t, 0), setupEnvelope.Data.SetupToken), "", nil, "127.0.0.1:1234", "localhost")
	requireCryptoReason(t, badCode, http.StatusForbidden, "CRYPTO_TOTP_REQUIRED")

	enabled := cryptoRouteRequestBody(h.router, http.MethodPost, "/api/v1/admin/crypto/security/enable", body, "", nil, "127.0.0.1:1234", "localhost")
	require.Equal(t, http.StatusOK, enabled.Code, enabled.Body.String())
	require.Contains(t, enabled.Body.String(), `"success":true`)
	require.True(t, user.TotpEnabled)
	require.NotNil(t, user.TotpSecretEncrypted)
	require.NotContains(t, enabled.Body.String(), setupEnvelope.Data.Secret)

	status := cryptoRouteRequest(h.router, http.MethodGet, "/api/v1/admin/crypto/security/status", "", nil, "127.0.0.1:1234", "localhost")
	require.Equal(t, http.StatusOK, status.Code, status.Body.String())
	require.Contains(t, status.Body.String(), `"enabled":true`)
	require.Contains(t, status.Body.String(), `"feature_enabled":true`)
	require.Contains(t, status.Body.String(), `"can_setup":false`)

	repeated := cryptoRouteRequestBody(h.router, http.MethodPost, "/api/v1/admin/crypto/security/setup",
		`{}`, "", nil, "127.0.0.1:1234", "localhost")
	require.Equal(t, http.StatusBadRequest, repeated.Code)
	require.Equal(t, "enc:"+base64.StdEncoding.EncodeToString([]byte(setupEnvelope.Data.Secret)), *user.TotpSecretEncrypted)

	replay := cryptoRouteRequestBody(h.router, http.MethodPost, "/api/v1/admin/crypto/security/enable", body, "", nil, "127.0.0.1:1234", "localhost")
	requireCryptoReason(t, replay, http.StatusConflict, "CRYPTO_TOTP_REPLAYED")
}

func TestAdminTotpDisableHTTPRequiresOnlyFreshTotpAndRejectsApiKey(t *testing.T) {
	t.Setenv("CRYPTO_INSTANCE_ENABLED", "true")
	user := activeCryptoAdmin()
	h := newCryptoRouteHarness(t, user)
	totpService := service.NewTotpService(&cryptoRouteUserRepo{user: user}, cryptoRouteEncryptor{}, h.cache, h.settings, nil, nil)
	configKey := &config.Config{Totp: config.TotpConfig{EncryptionKey: strings.Repeat("a", 64), EncryptionKeyConfigured: true}}
	security := service.NewCryptoSecurityService(totpService, configKey)
	require.NotNil(t, security)
	totpHandler := handler.NewTotpHandler(totpService)

	router := gin.New()
	router.POST("/api/v1/user/totp/disable", func(c *gin.Context) {
		if c.GetHeader("X-API-Key") != "" || !strings.HasPrefix(c.GetHeader("Authorization"), "Bearer ") {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: user.ID})
		c.Next()
	}, totpHandler.Disable)
	apiKey := httptest.NewRequest(http.MethodPost, "/api/v1/user/totp/disable", strings.NewReader(`{"totp_code":"123456"}`))
	apiKey.Header.Set("X-API-Key", "legacy-admin-key")
	apiKey.Header.Set("Authorization", "Bearer jwt")
	apiKeyResponse := httptest.NewRecorder()
	router.ServeHTTP(apiKeyResponse, apiKey)
	require.Equal(t, http.StatusUnauthorized, apiKeyResponse.Code)

	missing := httptest.NewRequest(http.MethodPost, "/api/v1/user/totp/disable", strings.NewReader(`{"password":"ignored"}`))
	missing.Header.Set("Authorization", "Bearer jwt")
	missingResponse := httptest.NewRecorder()
	router.ServeHTTP(missingResponse, missing)
	requireCryptoReason(t, missingResponse, http.StatusForbidden, "CRYPTO_TOTP_REQUIRED")
	require.Contains(t, missingResponse.Header().Get("Cache-Control"), "no-store")

	code, err := totp.GenerateCode(cryptoRouteTotpSecret, time.Now())
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/user/totp/disable", strings.NewReader(fmt.Sprintf(`{"totp_code":%q}`, code)))
	request.Header.Set("Authorization", "Bearer jwt")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.False(t, user.TotpEnabled)
}

func TestCryptoUserRoutesRespectInstanceAndAdminTestGates(t *testing.T) {
	t.Setenv("CRYPTO_INSTANCE_ENABLED", "false")
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	user := &service.User{ID: 52, Email: "user@example.test", Role: service.RoleUser, Status: service.StatusActive}
	h := newCryptoRouteHarness(t, user)
	userRouter := gin.New()
	v1 := userRouter.Group("/api/v1")
	RegisterCryptoRoutes(v1, handler.NewCryptoHandler(nil, h.settings, nil, nil, nil), nil,
		middleware.JWTAuthMiddleware(func(c *gin.Context) {
			c.Set("auth_method", service.AuditAuthMethodJWT)
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: user.ID})
			c.Set(string(middleware.ContextKeyUserRole), service.RoleUser)
			c.Next()
		}), nil, nil)
	response := cryptoRouteRequest(userRouter, http.MethodGet, "/api/v1/crypto/deposit-addresses", "", nil, "127.0.0.1:1", "localhost")
	requireCryptoReason(t, response, http.StatusServiceUnavailable, "CRYPTO_DISABLED")

	t.Setenv("CRYPTO_INSTANCE_ENABLED", "true")
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "true")
	response = cryptoRouteRequest(userRouter, http.MethodGet, "/api/v1/crypto/deposit-addresses", "", nil, "127.0.0.1:1", "localhost")
	requireCryptoReason(t, response, http.StatusForbidden, "CRYPTO_TEST_MODE_READ_ONLY")

	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	response = cryptoRouteRequest(userRouter, http.MethodGet, "/api/v1/crypto/deposit-addresses", "", nil, "127.0.0.1:1", "localhost")
	require.Equal(t, http.StatusInternalServerError, response.Code, response.Body.String())
	require.NotContains(t, response.Body.String(), "CRYPTO_TEST_MODE_READ_ONLY")
}
