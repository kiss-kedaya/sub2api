//go:build unit

package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 登录发令牌：失败先短退避重试一次；仍失败才退回只发 access token，并计数 + 限频 ERROR。

// loginRefreshCacheStub 只实现登录发令牌路径用到的三个方法；前 failFirst 次 StoreRefreshToken 失败。
type loginRefreshCacheStub struct {
	service.RefreshTokenCache
	failFirst  int32
	storeCalls atomic.Int32
}

func (s *loginRefreshCacheStub) StoreRefreshToken(context.Context, string, *service.RefreshTokenData, time.Duration) error {
	if s.storeCalls.Add(1) <= s.failFirst {
		return errors.New("redis timeout")
	}
	return nil
}

func (s *loginRefreshCacheStub) AddToUserTokenSet(context.Context, int64, string, time.Duration) error {
	return nil
}

func (s *loginRefreshCacheStub) AddToFamilyTokenSet(context.Context, string, string, time.Duration) error {
	return nil
}

type loginLogBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *loginLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *loginLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func captureLoginLogs(t *testing.T) *loginLogBuffer {
	t.Helper()
	prevLogger, prevWriter, prevFlags := slog.Default(), log.Writer(), log.Flags()
	buf := &loginLogBuffer{}
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() {
		slog.SetDefault(prevLogger)
		log.SetOutput(prevWriter)
		log.SetFlags(prevFlags)
	})
	return buf
}

func newLoginTokenPairService(cache service.RefreshTokenCache) *service.AuthService {
	cfg := &config.Config{
		JWT: config.JWTConfig{
			Secret:                   "test-login-pair-secret-0123456789abcdef",
			ExpireHour:               24,
			AccessTokenExpireMinutes: 60,
			RefreshTokenExpireDays:   14,
		},
	}
	return service.NewAuthService(nil, nil, nil, cache, cfg, nil, nil, nil, nil, nil, nil, nil, nil)
}

type loginAuthBody struct {
	Code int `json:"code"`
	Data struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	} `json:"data"`
}

func runLoginTokenPair(t *testing.T, ctx context.Context, authService *service.AuthService) loginAuthBody {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil).WithContext(ctx)

	user := &service.User{ID: 77, Email: "login-pair@example.com", Role: service.RoleUser, Status: service.StatusActive, Concurrency: 1}
	respondWithTokenPair(c, authService, user)

	require.Equal(t, http.StatusOK, recorder.Code, "a failing refresh token store must never fail the login itself")
	var body loginAuthBody
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	require.Zero(t, body.Code)
	require.NotEmpty(t, body.Data.AccessToken, "the login always yields an access token")
	return body
}

func useFastLoginRetryBackoff(t *testing.T) {
	t.Helper()
	prev := loginTokenPairRetryBackoff
	loginTokenPairRetryBackoff = time.Millisecond
	t.Cleanup(func() { loginTokenPairRetryBackoff = prev })
}

// 一次性的存储抖动：重试一次就成功，用户拿到完整的 token 对（带 refresh token）。
func TestRespondWithTokenPairRetriesOnceAndKeepsTheRefreshToken(t *testing.T) {
	useFastLoginRetryBackoff(t)
	cache := &loginRefreshCacheStub{failFirst: 1}
	authService := newLoginTokenPairService(cache)

	body := runLoginTokenPair(t, context.Background(), authService)

	require.NotEmpty(t, body.Data.RefreshToken, "the retry must save the refresh token")
	require.EqualValues(t, 2, cache.storeCalls.Load(), "exactly one retry")
	stats := authService.AuthStatsSnapshot()
	require.EqualValues(t, 1, stats["login_token_pair_retry_ok"])
	require.Zero(t, stats["login_token_pair_fallback"])
}

// 一次就成功：不重试、不计数。
func TestRespondWithTokenPairDoesNotRetryOnSuccess(t *testing.T) {
	useFastLoginRetryBackoff(t)
	cache := &loginRefreshCacheStub{}
	authService := newLoginTokenPairService(cache)

	body := runLoginTokenPair(t, context.Background(), authService)

	require.NotEmpty(t, body.Data.RefreshToken)
	require.EqualValues(t, 1, cache.storeCalls.Load())
	require.Empty(t, authService.AuthStatsSnapshot())
}

// 重试之后仍失败：登录不能整体失败，退回只发 access token，并计数 + 留一条 ERROR（不含令牌）。
func TestRespondWithTokenPairFallsBackToAccessTokenOnlyAfterTheRetryFails(t *testing.T) {
	useFastLoginRetryBackoff(t)
	logs := captureLoginLogs(t)
	cache := &loginRefreshCacheStub{failFirst: 99}
	authService := newLoginTokenPairService(cache)

	body := runLoginTokenPair(t, context.Background(), authService)

	require.Empty(t, body.Data.RefreshToken, "no refresh token when the store keeps failing")
	require.EqualValues(t, 2, cache.storeCalls.Load(), "the original attempt plus exactly one retry")
	stats := authService.AuthStatsSnapshot()
	require.EqualValues(t, 1, stats["login_token_pair_fallback"])
	require.Zero(t, stats["login_token_pair_retry_ok"])

	out := logs.String()
	require.Contains(t, out, "level=ERROR")
	require.Contains(t, out, "refresh token could not be stored")
	require.Contains(t, out, "user_id=77")
	require.NotContains(t, out, body.Data.AccessToken, "logs must not contain tokens")
}

// ERROR 是限频的：一阵存储故障只留固定条数，其余只计数。
func TestRespondWithTokenPairFallbackErrorLogIsRateLimited(t *testing.T) {
	useFastLoginRetryBackoff(t)
	logs := captureLoginLogs(t)
	authService := newLoginTokenPairService(&loginRefreshCacheStub{failFirst: 9999})

	const logins = 25
	for i := 0; i < logins; i++ {
		runLoginTokenPair(t, context.Background(), authService)
	}

	require.Equal(t, 10, strings.Count(logs.String(), "refresh token could not be stored"))
	stats := authService.AuthStatsSnapshot()
	require.EqualValues(t, logins, stats["login_token_pair_fallback"], "every fallback is still counted")
	require.EqualValues(t, logins-10, stats["warn_suppressed"])
}

// 没配置 refresh token 存储是配置问题，重试没用：不退避、不重试，直接退回只发 access token。
func TestRespondWithTokenPairDoesNotRetryWhenNoStoreIsConfigured(t *testing.T) {
	prev := loginTokenPairRetryBackoff
	loginTokenPairRetryBackoff = 5 * time.Second
	t.Cleanup(func() { loginTokenPairRetryBackoff = prev })
	authService := newLoginTokenPairService(nil)

	started := time.Now()
	body := runLoginTokenPair(t, context.Background(), authService)

	require.Less(t, time.Since(started), time.Second, "no backoff sleep for a configuration problem")
	require.Empty(t, body.Data.RefreshToken)
	require.EqualValues(t, 1, authService.AuthStatsSnapshot()["login_token_pair_fallback"])
}

// 请求已经取消（客户端走了）：不重试。
func TestRespondWithTokenPairDoesNotRetryWhenTheRequestIsCanceled(t *testing.T) {
	useFastLoginRetryBackoff(t)
	cache := &loginRefreshCacheStub{failFirst: 99}
	authService := newLoginTokenPairService(cache)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	runLoginTokenPair(t, ctx, authService)

	require.EqualValues(t, 1, cache.storeCalls.Load(), "a canceled request is not retried")
}
