//go:build unit

package service_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

const tokenHardeningTestSecret = "test-token-hardening-secret-0123456789"

func newTokenHardeningTestService(t *testing.T, cfg *config.Config) (*service.AuthService, *service.User, *emailBindRefreshTokenCacheStub) {
	t.Helper()
	user := &service.User{
		ID:           77,
		Email:        "token-hardening@example.com",
		PasswordHash: "hash",
		Role:         service.RoleUser,
		Status:       service.StatusActive,
		Concurrency:  1,
	}
	repo := newEmailBindUserRepoStub(user)
	cache := newEmailBindRefreshTokenCacheStub()
	if cfg == nil {
		cfg = &config.Config{}
	}
	if cfg.JWT.Secret == "" {
		cfg.JWT.Secret = tokenHardeningTestSecret
	}
	if cfg.JWT.ExpireHour == 0 {
		cfg.JWT.ExpireHour = 24
	}
	if cfg.JWT.AccessTokenExpireMinutes == 0 {
		cfg.JWT.AccessTokenExpireMinutes = 60
	}
	if cfg.JWT.RefreshTokenExpireDays == 0 {
		cfg.JWT.RefreshTokenExpireDays = 30
	}
	if cfg.JWT.SessionMaxLifetimeDays == 0 {
		cfg.JWT.SessionMaxLifetimeDays = 90
	}
	svc := service.NewAuthService(nil, repo, nil, cache, cfg, nil, nil, nil, nil, nil, nil, nil, nil)
	return svc, user, cache
}

// findStoredRefreshData 返回缓存里唯一一个未被消费的 refresh token 记录。
func findStoredRefreshData(t *testing.T, cache *emailBindRefreshTokenCacheStub) (string, *service.RefreshTokenData) {
	t.Helper()
	cache.mu.Lock()
	defer cache.mu.Unlock()
	for tokenHash, data := range cache.tokens {
		if data != nil && !data.Consumed {
			cloned := *data
			return tokenHash, &cloned
		}
	}
	t.Fatal("no active refresh token in stub cache")
	return "", nil
}

func withBinding(ip, ua string) context.Context {
	return service.WithSessionBinding(context.Background(), &service.SessionBinding{IP: ip, UserAgent: ua})
}

// 默认 access token 有效期 60 分钟；过期 token 必须被拒绝。
func TestGenerateTokenPairIssuesSixtyMinuteAccessToken(t *testing.T) {
	svc, user, _ := newTokenHardeningTestService(t, nil)
	pair, err := svc.GenerateTokenPair(context.Background(), user, "")
	require.NoError(t, err)
	require.Equal(t, 3600, pair.ExpiresIn)

	claims, err := svc.ValidateToken(pair.AccessToken)
	require.NoError(t, err)
	require.Equal(t, 60*time.Minute, claims.ExpiresAt.Time.Sub(claims.IssuedAt.Time))

	expired := &service.JWTClaims{
		UserID: user.ID,
		Email:  user.Email,
		Role:   user.Role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Minute)),
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-61 * time.Minute)),
			NotBefore: jwt.NewNumericDate(time.Now().Add(-61 * time.Minute)),
		},
	}
	expiredToken, err := jwt.NewWithClaims(jwt.SigningMethodHS256, expired).SignedString([]byte(tokenHardeningTestSecret))
	require.NoError(t, err)
	_, err = svc.ValidateToken(expiredToken)
	require.ErrorIs(t, err, service.ErrTokenExpired)
}

// 轮换只换凭证，不延长会话绝对寿命：超过 session_started_at + jwt.session_max_lifetime_days
// 必须重新登录（每枚令牌自己的有效期是滑动的，但会话本身有绝对上限）。
func TestRefreshTokenRejectsSessionBeyondAbsoluteLifetime(t *testing.T) {
	svc, user, cache := newTokenHardeningTestService(t, nil)
	ctx := context.Background()
	pair, err := svc.GenerateTokenPair(ctx, user, "")
	require.NoError(t, err)

	tokenHash, data := findStoredRefreshData(t, cache)
	// 模拟"已经滑动续期了 91 天"：Token 本身未过期，但会话起点已超过 90 天的上限。
	data.SessionStartedAt = time.Now().Add(-91 * 24 * time.Hour)
	data.CreatedAt = time.Now().Add(-30 * 24 * time.Hour)
	data.ExpiresAt = time.Now().Add(24 * time.Hour)
	cache.mu.Lock()
	cache.tokens[tokenHash] = data
	cache.mu.Unlock()

	_, err = svc.RefreshTokenPair(ctx, pair.RefreshToken)
	require.ErrorIs(t, err, service.ErrRefreshTokenSessionExpired)

	// 整个会话家族被撤销，不能再用旧令牌重试。
	_, err = svc.RefreshTokenPair(ctx, pair.RefreshToken)
	require.ErrorIs(t, err, service.ErrRefreshTokenInvalid)
}

// 轮换生成的新 refresh token 继承 session_started_at；有效期从「现在」起滑动
// （30 天，本用例的配置），而不是继承旧令牌的剩余寿命。
func TestRefreshTokenRotationKeepsSessionStart(t *testing.T) {
	svc, user, cache := newTokenHardeningTestService(t, nil)
	ctx := context.Background()
	pair, err := svc.GenerateTokenPair(ctx, user, "")
	require.NoError(t, err)

	tokenHash, data := findStoredRefreshData(t, cache)
	sessionStart := time.Now().Add(-10 * 24 * time.Hour).Truncate(time.Second)
	data.SessionStartedAt = sessionStart
	data.CreatedAt = time.Now().Add(-time.Hour)
	cache.mu.Lock()
	cache.tokens[tokenHash] = data
	cache.mu.Unlock()

	rotated, err := svc.RefreshTokenPair(ctx, pair.RefreshToken)
	require.NoError(t, err)

	_, rotatedData := findStoredRefreshData(t, cache)
	require.WithinDuration(t, sessionStart, rotatedData.SessionStartedAt, time.Second)
	// 滑动有效期：新令牌从现在起再有 30 天，不是旧令牌（或会话上限 90 天）里剩下的天数。
	remaining := time.Until(rotatedData.ExpiresAt)
	require.Greater(t, remaining, 29*24*time.Hour)
	require.LessOrEqual(t, remaining, 30*24*time.Hour)
	require.NotEmpty(t, rotated.RefreshToken)
}

// strict 模式下 IP 变化即拒绝刷新并撤销会话家族（on_mismatch=revoke 保留旧行为）。
func TestRefreshTokenBindingMismatchRevokesFamilyStrict(t *testing.T) {
	cfg := &config.Config{Security: config.SecurityConfig{
		SessionBindingMode:       config.SessionBindingModeStrict,
		SessionBindingOnMismatch: config.SessionBindingOnMismatchRevoke,
	}}
	svc, user, _ := newTokenHardeningTestService(t, cfg)
	loginCtx := withBinding("203.0.113.7", "Mozilla/5.0 test")
	pair, err := svc.GenerateTokenPair(loginCtx, user, "")
	require.NoError(t, err)

	_, err = svc.RefreshTokenPair(withBinding("203.0.113.99", "Mozilla/5.0 test"), pair.RefreshToken)
	require.ErrorIs(t, err, service.ErrSessionBindingMismatch)

	// 家族已撤销：即使回到原网络也不能再刷新。
	_, err = svc.RefreshTokenPair(loginCtx, pair.RefreshToken)
	require.ErrorIs(t, err, service.ErrRefreshTokenInvalid)
}

// loose 模式（生产默认）：同一 /24 内的 IP 漂移允许刷新，换网段则拒绝（revoke 模式）。
func TestRefreshTokenLooseBindingAllowsSameSubnet(t *testing.T) {
	cfg := &config.Config{Security: config.SecurityConfig{
		SessionBindingMode:       config.SessionBindingModeLoose,
		SessionBindingOnMismatch: config.SessionBindingOnMismatchRevoke,
	}}
	svc, user, _ := newTokenHardeningTestService(t, cfg)
	loginCtx := withBinding("198.51.100.10", "Mozilla/5.0 test")
	pair, err := svc.GenerateTokenPair(loginCtx, user, "")
	require.NoError(t, err)

	rotated, err := svc.RefreshTokenPair(withBinding("198.51.100.240", "Mozilla/5.0 test"), pair.RefreshToken)
	require.NoError(t, err)
	require.NotEmpty(t, rotated.RefreshToken)

	// 同一会话（轮换后的 token）换到另一个 /24：拒绝。
	_, err = svc.RefreshTokenPair(withBinding("203.0.113.10", "Mozilla/5.0 test"), rotated.RefreshToken)
	require.ErrorIs(t, err, service.ErrSessionBindingMismatch)
}

// 默认的 rebind：指纹变了也放行，新令牌带当前指纹，之后从新环境刷新不再不一致。
func TestRefreshTokenBindingMismatchRebindsByDefault(t *testing.T) {
	cfg := &config.Config{Security: config.SecurityConfig{SessionBindingMode: config.SessionBindingModeLoose}}
	svc, user, cache := newTokenHardeningTestService(t, cfg)
	pair, err := svc.GenerateTokenPair(withBinding("198.51.100.10", "Mozilla/5.0 test"), user, "")
	require.NoError(t, err)

	moved := withBinding("203.0.113.10", "Mozilla/5.0 test")
	rotated, err := svc.RefreshTokenPair(moved, pair.RefreshToken)
	require.NoError(t, err, "an unset on_mismatch means rebind: a changed network must not log the user out")

	_, data := findStoredRefreshData(t, cache)
	require.Equal(t, (&service.SessionBinding{IP: "203.0.113.10", UserAgent: "Mozilla/5.0 test"}).HashForMode(config.SessionBindingModeLoose), data.BindingHash)

	_, err = svc.RefreshTokenPair(moved, rotated.RefreshToken)
	require.NoError(t, err)
}

// mode=off 是回滚开关：不写入也不校验绑定。
func TestSessionBindingModeOffDisablesBinding(t *testing.T) {
	cfg := &config.Config{Security: config.SecurityConfig{SessionBindingMode: config.SessionBindingModeOff}}
	svc, user, cache := newTokenHardeningTestService(t, cfg)
	pair, err := svc.GenerateTokenPair(withBinding("203.0.113.7", "ua-a"), user, "")
	require.NoError(t, err)

	_, data := findStoredRefreshData(t, cache)
	require.Empty(t, data.BindingHash)

	_, err = svc.RefreshTokenPair(withBinding("198.51.100.8", "ua-b"), pair.RefreshToken)
	require.NoError(t, err)
}

// 指纹本身：loose 按网段归一、strict 保留完整 IP，UA 变化始终导致不同哈希。
func TestSessionBindingHashForMode(t *testing.T) {
	base := &service.SessionBinding{IP: "203.0.113.7", UserAgent: "ua-1"}
	require.Equal(t, "", base.HashForMode(config.SessionBindingModeOff))
	require.Equal(t, base.Hash(), base.HashForMode(config.SessionBindingModeStrict))
	require.NotEqual(t, base.HashForMode(config.SessionBindingModeStrict), base.HashForMode(config.SessionBindingModeLoose))

	sameSubnet := &service.SessionBinding{IP: "203.0.113.200", UserAgent: "ua-1"}
	require.Equal(t, base.HashForMode(config.SessionBindingModeLoose), sameSubnet.HashForMode(config.SessionBindingModeLoose))
	require.NotEqual(t, base.Hash(), sameSubnet.Hash())

	otherSubnet := &service.SessionBinding{IP: "203.0.114.7", UserAgent: "ua-1"}
	require.NotEqual(t, base.HashForMode(config.SessionBindingModeLoose), otherSubnet.HashForMode(config.SessionBindingModeLoose))

	uaChanged := &service.SessionBinding{IP: "203.0.113.7", UserAgent: "ua-2"}
	require.NotEqual(t, base.HashForMode(config.SessionBindingModeLoose), uaChanged.HashForMode(config.SessionBindingModeLoose))
}

// oauthCodeServiceCacheStub 同时实现 RefreshTokenCache 与 OAuthLoginCodeStore。
type oauthCodeServiceCacheStub struct {
	*emailBindRefreshTokenCacheStub
	mu    sync.Mutex
	codes map[string]*service.OAuthLoginCodeData
	ttls  map[string]time.Duration
}

func newOAuthCodeServiceCacheStub() *oauthCodeServiceCacheStub {
	return &oauthCodeServiceCacheStub{
		emailBindRefreshTokenCacheStub: newEmailBindRefreshTokenCacheStub(),
		codes:                          make(map[string]*service.OAuthLoginCodeData),
		ttls:                           make(map[string]time.Duration),
	}
}

func (s *oauthCodeServiceCacheStub) StoreOAuthLoginCode(_ context.Context, codeHash string, data *service.OAuthLoginCodeData, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cloned := *data
	s.codes[codeHash] = &cloned
	s.ttls[codeHash] = ttl
	return nil
}

func (s *oauthCodeServiceCacheStub) ConsumeOAuthLoginCode(_ context.Context, codeHash string) (*service.OAuthLoginCodeData, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.codes[codeHash]
	if !ok {
		return nil, service.ErrOAuthLoginCodeNotFound
	}
	delete(s.codes, codeHash)
	cloned := *data
	return &cloned, nil
}

func newOAuthCodeTestService(t *testing.T) (*service.AuthService, *service.User, *oauthCodeServiceCacheStub) {
	t.Helper()
	user := &service.User{
		ID:           88,
		Email:        "oauth-code@example.com",
		PasswordHash: "hash",
		Role:         service.RoleUser,
		Status:       service.StatusActive,
	}
	cache := newOAuthCodeServiceCacheStub()
	cfg := &config.Config{
		JWT: config.JWTConfig{
			Secret:                   "test-oauth-code-secret-0123456789",
			ExpireHour:               24,
			AccessTokenExpireMinutes: 60,
			RefreshTokenExpireDays:   30,
		},
	}
	svc := service.NewAuthService(nil, newEmailBindUserRepoStub(user), nil, cache, cfg, nil, nil, nil, nil, nil, nil, nil, nil)
	return svc, user, cache
}

// 一次性 code：60 秒 TTL、只能消费一次。
func TestOAuthLoginCodeIsSingleUse(t *testing.T) {
	svc, user, cache := newOAuthCodeTestService(t)
	require.True(t, svc.OAuthCodeExchangeAvailable())

	code, err := svc.CreateOAuthLoginCode(context.Background(), user.ID, "github", "/dashboard")
	require.NoError(t, err)
	require.NotEmpty(t, code)
	for _, ttl := range cache.ttls {
		require.Equal(t, service.OAuthLoginCodeTTL, ttl)
	}

	data, err := svc.ConsumeOAuthLoginCode(context.Background(), code)
	require.NoError(t, err)
	require.Equal(t, user.ID, data.UserID)
	require.Equal(t, "github", data.Provider)
	require.Equal(t, "/dashboard", data.RedirectTo)

	_, err = svc.ConsumeOAuthLoginCode(context.Background(), code)
	require.ErrorIs(t, err, service.ErrOAuthLoginCodeNotFound)
}

// 空码、超长码、未知码一律按无效处理（不区分原因，避免探测）。
func TestOAuthLoginCodeRejectsInvalidInput(t *testing.T) {
	svc, _, _ := newOAuthCodeTestService(t)
	for _, code := range []string{"", "not-a-real-code", string(make([]byte, 300))} {
		_, err := svc.ConsumeOAuthLoginCode(context.Background(), code)
		require.ErrorIs(t, err, service.ErrOAuthLoginCodeNotFound)
	}
}

// 未配置 Redis 能力时明确报告不可用，调用方应退回旧流程。
func TestOAuthCodeExchangeUnavailableWithoutStore(t *testing.T) {
	svc, user, _ := newTokenHardeningTestService(t, nil)
	require.False(t, svc.OAuthCodeExchangeAvailable())
	_, err := svc.CreateOAuthLoginCode(context.Background(), user.ID, "github", "/dashboard")
	require.Error(t, err)
}
