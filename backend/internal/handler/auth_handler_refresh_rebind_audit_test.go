//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/repository" //nolint:depguard // 需要真实的 refresh token 存储（原子消费脚本）来走完整的刷新路径。
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9" //nolint:depguard // 同上。
	"github.com/stretchr/testify/require"
)

// 刷新时的指纹重绑（只换了 IP）要写审计：成功的刷新默认不进审计，重绑是例外。
// 审计里只带前后两端的粗粒度标签；操作者是刷新所属的用户。

type rebindAuditRepo struct {
	service.AuditLogRepository
	mu   sync.Mutex
	logs []*service.AuditLog
}

func (r *rebindAuditRepo) BatchInsert(_ context.Context, logs []*service.AuditLog) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logs = append(r.logs, logs...)
	return int64(len(logs)), nil
}

func (r *rebindAuditRepo) Insert(_ context.Context, entry *service.AuditLog) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logs = append(r.logs, entry)
	return nil
}

const (
	rebindUAChrome = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36"
	rebindUAFox    = "Mozilla/5.0 (X11; Linux x86_64; rv:132.0) Gecko/20100101 Firefox/132.0"
)

type refreshRebindHarness struct {
	engine       *gin.Engine
	auditService *service.AuditLogService
	audit        *rebindAuditRepo
	authService  *service.AuthService
	refreshToken string
}

func newRefreshRebindHarness(t *testing.T) *refreshRebindHarness {
	t.Helper()
	gin.SetMode(gin.TestMode)

	mini := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	user := &service.User{ID: 12, Email: "rebind-audit@example.com", PasswordHash: "h", Role: service.RoleUser, Status: service.StatusActive, Concurrency: 1}
	cfg := &config.Config{
		JWT: config.JWTConfig{
			Secret: "test-rebind-audit-secret-0123456789abcdef", ExpireHour: 24, AccessTokenExpireMinutes: 60,
			RefreshTokenExpireDays: 14, SessionMaxLifetimeDays: 90, RefreshTokenReuseLeewaySeconds: 30,
		},
		Security: config.SecurityConfig{
			SessionBindingMode:       config.SessionBindingModeLoose,
			SessionBindingOnMismatch: config.SessionBindingOnMismatchRebind,
		},
	}
	authService := service.NewAuthService(nil, &userHandlerRepoStub{user: user}, nil, repository.NewRefreshTokenCache(rdb), cfg, nil, nil, nil, nil, nil, nil, nil, nil)
	loginCtx := service.WithSessionBinding(context.Background(), &service.SessionBinding{IP: "203.0.113.10", UserAgent: rebindUAChrome})
	pair, err := authService.GenerateTokenPair(loginCtx, user, "")
	require.NoError(t, err)

	repo := &rebindAuditRepo{}
	auditService := service.NewAuditLogService(repo, nil)
	auditService.Start()

	h := &AuthHandler{
		cfg:         cfg,
		authService: authService,
		settingSvc:  service.NewSettingService(&loginTOTPSettingRepo{values: map[string]string{}}, cfg),
	}
	r := gin.New()
	r.Use(middleware2.SessionBindingContext(cfg))
	r.Use(gin.HandlerFunc(middleware2.NewAuditLogMiddleware(auditService)))
	r.POST("/api/v1/auth/refresh", h.RefreshToken)
	return &refreshRebindHarness{engine: r, auditService: auditService, audit: repo, authService: authService, refreshToken: pair.RefreshToken}
}

// refresh 用当前持有的 refresh token 发一次 POST /auth/refresh，成功后换成新令牌。
func (h *refreshRebindHarness) refresh(t *testing.T, ip, ua string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", strings.NewReader(`{"refresh_token":"`+h.refreshToken+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", ua)
	req.RemoteAddr = ip + ":4433"
	w := httptest.NewRecorder()
	h.engine.ServeHTTP(w, req)
	if w.Code == http.StatusOK {
		var body struct {
			Data struct {
				RefreshToken string `json:"refresh_token"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		h.refreshToken = body.Data.RefreshToken
	}
	return w
}

func (h *refreshRebindHarness) auditRows() []*service.AuditLog {
	h.auditService.Stop()
	h.audit.mu.Lock()
	defer h.audit.mu.Unlock()
	return append([]*service.AuditLog(nil), h.audit.logs...)
}

func TestRefreshHandlerAuditsAFingerprintRebindButNotAnOrdinaryRefresh(t *testing.T) {
	h := newRefreshRebindHarness(t)

	require.Equal(t, http.StatusOK, h.refresh(t, "203.0.113.10", rebindUAChrome).Code, "an ordinary refresh from the same client")
	require.Equal(t, http.StatusOK, h.refresh(t, "198.51.100.77", rebindUAChrome).Code, "only the IP changed: allowed and re-bound")

	rows := h.auditRows()
	require.Len(t, rows, 1, "the ordinary successful refresh is not audited, the rebind is")
	row := rows[0]
	require.Equal(t, service.AuditActionSessionBindingRebind, row.Action)
	require.NotNil(t, row.ActorUserID)
	require.EqualValues(t, 12, *row.ActorUserID)
	require.Equal(t, http.StatusOK, row.StatusCode)
	require.Equal(t, "chrome/windows/ipv4", row.Extra["binding_from"])
	require.Equal(t, "chrome/windows/ipv4", row.Extra["binding_to"])
	extra := fmt.Sprint(row.Extra)
	for _, forbidden := range []string{"203.0.113", "198.51.100", "Chrome/130", "AppleWebKit"} {
		require.NotContains(t, extra, forbidden, "the audit extra carries coarse labels only")
	}
}

// 同一会话反复换网络：每次都是重绑、计数照常累加，但审计行限频——每个窗口只有第一次写。
func TestRefreshHandlerRebindAuditIsRateLimitedPerSession(t *testing.T) {
	h := newRefreshRebindHarness(t)

	for _, ip := range []string{"198.51.100.77", "192.0.2.50", "198.18.0.9"} { // 每次都是新的 /24
		require.Equal(t, http.StatusOK, h.refresh(t, ip, rebindUAChrome).Code)
	}

	rows := h.auditRows()
	require.Len(t, rows, 1, "three rebinds of one session inside the window leave exactly one audit row")
	require.Equal(t, service.AuditActionSessionBindingRebind, rows[0].Action)
	stats := h.authService.AuthStatsSnapshot()
	require.EqualValues(t, 3, stats["refresh_binding_rebind"], "the counter still counts every rebind")
	require.EqualValues(t, 2, stats["refresh_binding_rebind_audit_suppressed"])
}

// 家族变了（换了浏览器/系统）不是 rebind：撤销会话，刷新失败（失败的刷新照常进审计）。
func TestRefreshHandlerDoesNotRebindWhenTheBrowserFamilyChanged(t *testing.T) {
	h := newRefreshRebindHarness(t)

	w := h.refresh(t, "198.51.100.77", rebindUAFox)
	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.Contains(t, w.Body.String(), "SESSION_BINDING_MISMATCH")

	rows := h.auditRows()
	require.Len(t, rows, 1, "a failed refresh is audited as before")
	require.NotEqual(t, service.AuditActionSessionBindingRebind, rows[0].Action)
	require.Equal(t, http.StatusUnauthorized, rows[0].StatusCode)
}
