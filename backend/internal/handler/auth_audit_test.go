package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// authAuditCaptureRepo 捕获审计中间件写入的记录（与 middleware 包测试同构）。
type authAuditCaptureRepo struct {
	mu   sync.Mutex
	logs []*service.AuditLog
}

func (r *authAuditCaptureRepo) BatchInsert(_ context.Context, logs []*service.AuditLog) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logs = append(r.logs, logs...)
	return int64(len(logs)), nil
}
func (r *authAuditCaptureRepo) Insert(_ context.Context, log *service.AuditLog) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logs = append(r.logs, log)
	return nil
}
func (r *authAuditCaptureRepo) List(context.Context, *service.AuditLogFilter) (*service.AuditLogList, error) {
	return &service.AuditLogList{}, nil
}
func (r *authAuditCaptureRepo) GetByID(context.Context, int64) (*service.AuditLog, error) {
	return nil, service.ErrAuditLogNotFound
}
func (r *authAuditCaptureRepo) Count(context.Context) (int64, error) { return 0, nil }
func (r *authAuditCaptureRepo) TruncateAll(context.Context) error    { return nil }
func (r *authAuditCaptureRepo) DeleteBefore(context.Context, time.Time, int) (int64, error) {
	return 0, nil
}

// runAuthAuditRoute 用真实审计中间件跑一次请求，返回落库的那一条记录。
func runAuthAuditRoute(t *testing.T, method, path string, handler gin.HandlerFunc) *service.AuditLog {
	t.Helper()
	gin.SetMode(gin.TestMode)
	repo := &authAuditCaptureRepo{}
	auditService := service.NewAuditLogService(repo, nil)
	auditService.Start()

	router := gin.New()
	router.Use(gin.HandlerFunc(middleware.NewAuditLogMiddleware(auditService)))
	switch method {
	case http.MethodGet:
		router.GET(path, handler)
	case http.MethodPost:
		router.POST(path, handler)
	default:
		t.Fatalf("unsupported method: %s", method)
	}

	request := httptest.NewRequest(method, path+"?code=oauth-code&state=oauth-state", nil)
	router.ServeHTTP(httptest.NewRecorder(), request)
	auditService.Stop()

	repo.mu.Lock()
	defer repo.mu.Unlock()
	require.Len(t, repo.logs, 1)
	return repo.logs[0]
}

// TestRedirectOAuthErrorWritesStructuredFailure 验证 OAuth 回调失败埋点：
// 错误码进 reason / reason_class，凭证与上游原文不进审计。
func TestRedirectOAuthErrorWritesStructuredFailure(t *testing.T) {
	entry := runAuthAuditRoute(t, http.MethodGet, "/api/v1/auth/oauth/github/callback", func(c *gin.Context) {
		redirectOAuthError(c, "/auth/oauth/callback", "invalid_state", "invalid oauth state", "provider raw detail")
	})

	require.Equal(t, service.AuditActionOAuthLogin, entry.Action)
	require.Equal(t, service.AuditAuthMethodOAuth, entry.AuthMethod)
	require.Equal(t, "github", entry.Extra["provider"])
	require.Equal(t, "login_callback", entry.Extra["flow"])
	require.Equal(t, service.AuditResultFailure, entry.Extra["result"])
	require.Equal(t, "invalid_state", entry.Extra["reason"])
	require.Equal(t, service.AuditFailureClassOAuthState, entry.Extra["reason_class"])
	// 上游返回的原文/描述不入库。
	require.NotContains(t, entry.Extra, "provider raw detail")
	// 回调 query 里的 code/state 一律脱敏。
	query, _ := entry.Extra["query"].(string)
	require.NotContains(t, query, "oauth-code")
	require.NotContains(t, query, "oauth-state")
}

// TestMarkAuthAuditSuccessWritesActor 验证 OAuth 成功埋点：user_id 落列、邮箱只落脱敏值与指纹。
func TestMarkAuthAuditSuccessWritesActor(t *testing.T) {
	entry := runAuthAuditRoute(t, http.MethodGet, "/api/v1/auth/oauth/oidc/callback", func(c *gin.Context) {
		markAuthAuditSuccess(c, 4242, "someone@example.com")
		redirectWithFragment(c, "/auth/oauth/callback", nil)
	})

	require.Equal(t, service.AuditResultSuccess, entry.Extra["result"])
	require.NotNil(t, entry.ActorUserID)
	require.Equal(t, int64(4242), *entry.ActorUserID)
	require.NotContains(t, entry.ActorEmail, "someone@example.com")
	require.Equal(t, service.HashAuditEmail("someone@example.com"), entry.Extra["email_hash"])
}

// TestMarkAuthAuditChallengeWritesChallengeAction 验证 2FA 挑战下发留痕。
func TestMarkAuthAuditChallengeWritesChallengeAction(t *testing.T) {
	entry := runAuthAuditRoute(t, http.MethodPost, "/api/v1/auth/login", func(c *gin.Context) {
		markAuthAuditChallenge(c, 4242, "someone@example.com")
		writeAuthAuditTestOK(c)
	})

	require.Equal(t, service.AuditActionLogin2FAChallenge, entry.Action)
	require.Equal(t, service.AuditResultChallengeIssued, entry.Extra["result"])
	require.Equal(t, true, entry.Extra["requires_2fa"])
	require.NotNil(t, entry.ActorUserID)
	require.Equal(t, int64(4242), *entry.ActorUserID)
}

// TestMarkAuthAuditPendingWritesPending 验证「回调处理完但需前端继续」的中间态。
func TestMarkAuthAuditPendingWritesPending(t *testing.T) {
	entry := runAuthAuditRoute(t, http.MethodPost, "/api/v1/auth/oauth/pending/exchange", func(c *gin.Context) {
		markAuthAuditPending(c, "registration_choice")
		c.Status(http.StatusOK)
	})

	require.Equal(t, service.AuditResultPending, entry.Extra["result"])
	require.Equal(t, "registration_choice", entry.Extra["reason"])
}

// writeAuthAuditTestOK 是测试内的最小 200 响应。
func writeAuthAuditTestOK(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"requires_2fa": true})
}
