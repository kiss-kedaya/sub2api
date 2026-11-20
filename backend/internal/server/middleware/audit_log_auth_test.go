package middleware

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// oauthAuditSuccessOAuth 在模拟 handler 里写入「OAuth 登录成功」标记。
func oauthAuditSuccessOAuth(c *gin.Context) {
	MarkAuthAuditActor(c, 4242, "some.user@example.com")
	MarkAuthAuditResult(c, service.AuditResultSuccess, "")
	c.Status(http.StatusOK)
}

// oauthAuditFailure 模拟 OAuth 回调失败（POST 类）。
func oauthAuditFailure(c *gin.Context) {
	MarkAuthAuditResult(c, service.AuditResultFailure, "invalid_state")
	c.Status(http.StatusUnauthorized)
}

// oauthAuditChallenge 模拟 2FA 挑战下发。
func oauthAuditChallenge(c *gin.Context) {
	SetAuditAction(c, service.AuditActionLogin2FAChallenge)
	MarkAuthAuditActor(c, 4242, "some.user@example.com")
	MarkAuthAuditRequires2FA(c)
	MarkAuthAuditResult(c, service.AuditResultChallengeIssued, "")
	c.Status(http.StatusOK)
}

func newAuthAuditTestService(t *testing.T) (*service.AuditLogService, *auditCaptureRepository) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	repository := &auditCaptureRepository{}
	auditService := service.NewAuditLogService(repository, nil)
	auditService.Start()
	return auditService, repository
}

func collectAuthAuditLogs(t *testing.T, auditService *service.AuditLogService, repository *auditCaptureRepository) []*service.AuditLog {
	t.Helper()
	auditService.Stop()
	repository.mu.Lock()
	defer repository.mu.Unlock()
	return append([]*service.AuditLog(nil), repository.logs...)
}

// TestAuditAuthEventRoutesAreRegistered 守住「所有第三方登录入口都登记了审计」，
// 防止以后新增 provider 路由时漏掉回调埋点。
func TestAuditAuthEventRoutesAreRegistered(t *testing.T) {
	expected := []string{
		"GET /api/v1/auth/oauth/github/start",
		"POST /api/v1/auth/oauth/github/start",
		"GET /api/v1/auth/oauth/github/callback",
		"POST /api/v1/auth/oauth/github/complete-registration",
		"GET /api/v1/auth/oauth/google/start",
		"POST /api/v1/auth/oauth/google/start",
		"GET /api/v1/auth/oauth/google/callback",
		"POST /api/v1/auth/oauth/google/complete-registration",
		"GET /api/v1/auth/oauth/linuxdo/start",
		"POST /api/v1/auth/oauth/linuxdo/start",
		"GET /api/v1/auth/oauth/linuxdo/callback",
		"POST /api/v1/auth/oauth/linuxdo/complete-registration",
		"POST /api/v1/auth/oauth/linuxdo/bind-login",
		"POST /api/v1/auth/oauth/linuxdo/create-account",
		"GET /api/v1/auth/oauth/wechat/start",
		"POST /api/v1/auth/oauth/wechat/start",
		"GET /api/v1/auth/oauth/wechat/callback",
		"POST /api/v1/auth/oauth/wechat/complete-registration",
		"POST /api/v1/auth/oauth/wechat/bind-login",
		"POST /api/v1/auth/oauth/wechat/create-account",
		"GET /api/v1/auth/oauth/oidc/start",
		"POST /api/v1/auth/oauth/oidc/start",
		"GET /api/v1/auth/oauth/oidc/callback",
		"POST /api/v1/auth/oauth/oidc/complete-registration",
		"POST /api/v1/auth/oauth/oidc/bind-login",
		"POST /api/v1/auth/oauth/oidc/create-account",
		"GET /api/v1/auth/oauth/dingtalk/start",
		"POST /api/v1/auth/oauth/dingtalk/start",
		"GET /api/v1/auth/oauth/dingtalk/callback",
		"POST /api/v1/auth/oauth/dingtalk/complete-registration",
		"POST /api/v1/auth/oauth/dingtalk/bind-login",
		"POST /api/v1/auth/oauth/dingtalk/create-account",
		"POST /api/v1/auth/oauth/pending/exchange",
		"POST /api/v1/auth/oauth/pending/send-verify-code",
		"POST /api/v1/auth/oauth/pending/create-account",
		"POST /api/v1/auth/oauth/pending/bind-login",
		"POST /api/v1/auth/passkey/login/finish",
		"POST /api/v1/auth/login",
		"POST /api/v1/auth/login/2fa",
		"POST /api/v1/auth/register",
	}
	for _, routeKey := range expected {
		_, ok := auditAuthEvents[routeKey]
		require.Truef(t, ok, "auth audit route missing: %s", routeKey)
	}
}

// TestAuditAuthEventPerProvider 覆盖每条第三方登录路径的成功/失败留痕，
// 并断言凭证与邮箱明文均不落库。
func TestAuditAuthEventPerProvider(t *testing.T) {
	type testCase struct {
		name           string
		routeKey       string
		handler        gin.HandlerFunc
		status         int
		wantResult     string
		wantAction     string
		wantAuthMethod string
		wantProvider   string
		wantFlow       string
	}
	cases := []testCase{
		{
			name: "github_callback_success", routeKey: "GET /api/v1/auth/oauth/github/callback",
			handler: func(c *gin.Context) {
				MarkAuthAuditActor(c, 4242, "some.user@example.com")
				MarkAuthAuditResult(c, service.AuditResultSuccess, "")
				c.Redirect(http.StatusFound, "/auth/oauth/callback")
			},
			status: http.StatusFound, wantResult: service.AuditResultSuccess,
			wantAction: service.AuditActionOAuthLogin, wantAuthMethod: service.AuditAuthMethodOAuth,
			wantProvider: "github", wantFlow: "login_callback",
		},
		{
			name: "google_callback_failure", routeKey: "GET /api/v1/auth/oauth/google/callback",
			handler: func(c *gin.Context) {
				MarkAuthAuditResult(c, service.AuditResultFailure, "invalid_state")
				c.Redirect(http.StatusFound, "/auth/oauth/callback#error=invalid_state")
			},
			status: http.StatusFound, wantResult: service.AuditResultFailure,
			wantAction: service.AuditActionOAuthLogin, wantAuthMethod: service.AuditAuthMethodOAuth,
			wantProvider: "google", wantFlow: "login_callback",
		},
		{
			name: "linuxdo_callback_pending_without_marker", routeKey: "GET /api/v1/auth/oauth/linuxdo/callback",
			handler: func(c *gin.Context) {
				c.Redirect(http.StatusFound, "/auth/oauth/callback")
			},
			status: http.StatusFound, wantResult: service.AuditResultPending,
			wantAction: service.AuditActionOAuthLogin, wantAuthMethod: service.AuditAuthMethodOAuth,
			wantProvider: "linuxdo", wantFlow: "login_callback",
		},
		{
			name: "oidc_callback_success", routeKey: "GET /api/v1/auth/oauth/oidc/callback",
			handler: oauthAuditSuccessOAuth,
			status:  http.StatusOK, wantResult: service.AuditResultSuccess,
			wantAction: service.AuditActionOAuthLogin, wantAuthMethod: service.AuditAuthMethodOAuth,
			wantProvider: "oidc", wantFlow: "login_callback",
		},
		{
			name: "wechat_callback_failure", routeKey: "GET /api/v1/auth/oauth/wechat/callback",
			handler: oauthAuditFailure,
			status:  http.StatusUnauthorized, wantResult: service.AuditResultFailure,
			wantAction: service.AuditActionOAuthLogin, wantAuthMethod: service.AuditAuthMethodOAuth,
			wantProvider: "wechat", wantFlow: "login_callback",
		},
		{
			name: "dingtalk_callback_failure", routeKey: "GET /api/v1/auth/oauth/dingtalk/callback",
			handler: oauthAuditFailure,
			status:  http.StatusUnauthorized, wantResult: service.AuditResultFailure,
			wantAction: service.AuditActionOAuthLogin, wantAuthMethod: service.AuditAuthMethodOAuth,
			wantProvider: "dingtalk", wantFlow: "login_callback",
		},
		{
			name:     "github_complete_registration_success",
			routeKey: "POST /api/v1/auth/oauth/github/complete-registration",
			handler:  oauthAuditSuccessOAuth, status: http.StatusOK,
			wantResult: service.AuditResultSuccess, wantAction: service.AuditActionOAuthRegister,
			wantAuthMethod: service.AuditAuthMethodOAuth, wantProvider: "github", wantFlow: "register",
		},
		{
			name:     "wechat_create_account_success",
			routeKey: "POST /api/v1/auth/oauth/wechat/create-account",
			handler:  oauthAuditSuccessOAuth, status: http.StatusOK,
			wantResult: service.AuditResultSuccess, wantAction: service.AuditActionOAuthRegister,
			wantAuthMethod: service.AuditAuthMethodOAuth, wantProvider: "wechat", wantFlow: "create_account",
		},
		{
			name:     "pending_exchange_failure",
			routeKey: "POST /api/v1/auth/oauth/pending/exchange",
			handler: func(c *gin.Context) {
				MarkAuthAuditResult(c, service.AuditResultFailure, "pending_auth_session_not_found")
				c.Status(http.StatusBadRequest)
			},
			status: http.StatusBadRequest, wantResult: service.AuditResultFailure,
			wantAction: service.AuditActionOAuthPending, wantAuthMethod: service.AuditAuthMethodOAuth,
			wantProvider: "oauth", wantFlow: "pending_exchange",
		},
		{
			name:     "pending_bind_login_success",
			routeKey: "POST /api/v1/auth/oauth/pending/bind-login",
			handler: func(c *gin.Context) {
				MarkAuthAuditProvider(c, "dingtalk")
				MarkAuthAuditActor(c, 4242, "some.user@example.com")
				MarkAuthAuditResult(c, service.AuditResultSuccess, "")
				c.Status(http.StatusOK)
			},
			status: http.StatusOK, wantResult: service.AuditResultSuccess,
			wantAction: service.AuditActionOAuthBind, wantAuthMethod: service.AuditAuthMethodOAuth,
			wantProvider: "dingtalk", wantFlow: "bind_login",
		},
		{
			name: "passkey_finish_success", routeKey: "POST /api/v1/auth/passkey/login/finish",
			handler: func(c *gin.Context) {
				MarkAuthAuditActor(c, 4242, "some.user@example.com")
				MarkAuthAuditResult(c, service.AuditResultSuccess, "")
				c.Set("auth_method", service.AuditAuthMethodPasskey)
				c.Status(http.StatusOK)
			},
			status: http.StatusOK, wantResult: service.AuditResultSuccess,
			wantAction: service.AuditActionLogin, wantAuthMethod: service.AuditAuthMethodPasskey,
			wantProvider: "passkey", wantFlow: "passkey_login",
		},
		{
			name: "password_login_failure", routeKey: "POST /api/v1/auth/login",
			handler: func(c *gin.Context) {
				MarkAuthAuditEmail(c, "attacker@example.com")
				c.Set("response_error_reason", "INVALID_CREDENTIALS")
				c.Status(http.StatusUnauthorized)
			},
			status: http.StatusUnauthorized, wantResult: service.AuditResultFailure,
			wantAction: service.AuditActionLogin, wantAuthMethod: service.AuditAuthMethodPassword,
			wantProvider: "password", wantFlow: "password",
		},
		{
			name: "login_2fa_success", routeKey: "POST /api/v1/auth/login/2fa",
			handler: func(c *gin.Context) {
				MarkAuthAuditEmail(c, "some.user@example.com")
				MarkAuthAuditActor(c, 4242, "some.user@example.com")
				MarkAuthAuditResult(c, service.AuditResultSuccess, "")
				c.Status(http.StatusOK)
			},
			status: http.StatusOK, wantResult: service.AuditResultSuccess,
			wantAction: service.AuditActionLogin2FA, wantAuthMethod: service.AuditAuthMethodPassword,
			wantProvider: "totp", wantFlow: "two_factor",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			auditService, repository := newAuthAuditTestService(t)
			router := gin.New()
			router.Use(gin.HandlerFunc(NewAuditLogMiddleware(auditService)))

			method, path := splitRouteKey(tc.routeKey)
			switch method {
			case http.MethodGet:
				router.GET(path, tc.handler)
			case http.MethodPost:
				router.POST(path, tc.handler)
			default:
				t.Fatalf("unsupported method %s", method)
			}

			body := ""
			if method == http.MethodPost {
				body = `{"email":"some.user@example.com","password":"p@ssw0rd","code":"oauth-code","state":"oauth-state","temp_token":"temp-token"}`
			}
			request := httptest.NewRequest(method, path+"?code=oauth-code&state=oauth-state&access_token=secret-token", bytes.NewBufferString(body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("User-Agent", "audit-test-agent/1.0")
			request.RemoteAddr = "203.0.113.7:4567"
			request = request.WithContext(context.WithValue(request.Context(), ctxkey.RequestID, "req-auth-1"))
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)

			logs := collectAuthAuditLogs(t, auditService, repository)
			require.Len(t, logs, 1)
			entry := logs[0]
			require.Equal(t, tc.wantAction, entry.Action)
			require.Equal(t, tc.wantAuthMethod, entry.AuthMethod)
			require.Equal(t, tc.wantProvider, entry.Extra["provider"])
			require.Equal(t, tc.wantFlow, entry.Extra["flow"])
			require.Equal(t, tc.wantResult, entry.Extra["result"])
			require.Equal(t, "req-auth-1", entry.RequestID)
			require.Equal(t, "203.0.113.7", entry.ClientIP)
			require.Equal(t, "audit-test-agent/1.0", entry.UserAgent)
			require.Equal(t, tc.status, entry.StatusCode)
			require.Equal(t, "<credential-bearing body omitted>", entry.RequestBody)

			// 凭证不落库：body / query / extra 里都不允许出现 code/state/token 明文。
			encoded := entry.RequestBody
			query, _ := entry.Extra["query"].(string)
			encoded += query
			for key, value := range entry.Extra {
				encoded += key
				if text, ok := value.(string); ok {
					encoded += text
				}
			}
			for _, secret := range []string{"oauth-code", "oauth-state", "temp-token", "secret-token", "p@ssw0rd"} {
				require.NotContainsf(t, encoded, secret, "sensitive value leaked into audit: %s", secret)
			}
		})
	}
}

// TestAuditAuthEventEmailIsHashedOrMasked 认证审计绝不落邮箱全量：
// actor_email 只能是脱敏值，extra.email_hash 必须是不可逆指纹。
func TestAuditAuthEventEmailIsHashedOrMasked(t *testing.T) {
	auditService, repository := newAuthAuditTestService(t)
	router := gin.New()
	router.Use(gin.HandlerFunc(NewAuditLogMiddleware(auditService)))
	router.POST("/api/v1/auth/login", func(c *gin.Context) {
		MarkAuthAuditEmail(c, "victim.user@example.com")
		c.Set("response_error_reason", "INVALID_CREDENTIALS")
		c.Status(http.StatusUnauthorized)
	})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
		bytes.NewBufferString(`{"email":"victim.user@example.com","password":"p@ssw0rd"}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(httptest.NewRecorder(), request)

	logs := collectAuthAuditLogs(t, auditService, repository)
	require.Len(t, logs, 1)
	entry := logs[0]
	require.NotContains(t, entry.ActorEmail, "victim.user@example.com")
	require.Contains(t, entry.ActorEmail, "***")
	hashValue, _ := entry.Extra["email_hash"].(string)
	require.NotEmpty(t, hashValue)
	require.NotContains(t, hashValue, "@")
	require.Equal(t, service.HashAuditEmail("victim.user@example.com"), hashValue)
	require.NotContains(t, entry.RequestBody, "victim.user@example.com")

	// 同一邮箱大小写/空格归一化后指纹稳定，便于跨事件串联撞库。
	require.Equal(t, hashValue, service.HashAuditEmail("  VICTIM.USER@EXAMPLE.COM "))
}

// TestAuditAuthEventChallengeAudit 2FA 挑战下发单独留痕，便于反查「挑战下发但从不验证」。
func TestAuditAuthEventChallengeAudit(t *testing.T) {
	for _, routeKey := range []string{
		"POST /api/v1/auth/login",
		"POST /api/v1/auth/oauth/pending/exchange",
	} {
		auditService, repository := newAuthAuditTestService(t)
		router := gin.New()
		router.Use(gin.HandlerFunc(NewAuditLogMiddleware(auditService)))
		router.POST(routeKey[len("POST "):], oauthAuditChallenge)

		request := httptest.NewRequest(http.MethodPost, routeKey[len("POST "):], bytes.NewBufferString(`{"temp_token":"x"}`))
		request.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(httptest.NewRecorder(), request)

		logs := collectAuthAuditLogs(t, auditService, repository)
		require.Len(t, logs, 1)
		entry := logs[0]
		require.Equal(t, service.AuditActionLogin2FAChallenge, entry.Action)
		require.Equal(t, service.AuditResultChallengeIssued, entry.Extra["result"])
		require.Equal(t, true, entry.Extra["requires_2fa"])
		require.NotNil(t, entry.ActorUserID)
		require.Equal(t, int64(4242), *entry.ActorUserID)
	}
}

// TestAuditAuthEventReasonFallbackToResponseErrorReason 失败原因优先用显式标记，
// 其次回落到 response.ErrorFrom 写入的 machine reason。
func TestAuditAuthEventReasonFallbackToResponseErrorReason(t *testing.T) {
	auditService, repository := newAuthAuditTestService(t)
	router := gin.New()
	router.Use(gin.HandlerFunc(NewAuditLogMiddleware(auditService)))
	router.POST("/api/v1/auth/oauth/pending/exchange", func(c *gin.Context) {
		c.Set("response_error_reason", "PENDING_AUTH_SESSION_NOT_FOUND")
		c.Status(http.StatusBadRequest)
	})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/pending/exchange", bytes.NewBufferString(`{}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(httptest.NewRecorder(), request)

	logs := collectAuthAuditLogs(t, auditService, repository)
	require.Len(t, logs, 1)
	entry := logs[0]
	require.Equal(t, service.AuditResultFailure, entry.Extra["result"])
	require.Equal(t, "PENDING_AUTH_SESSION_NOT_FOUND", entry.Extra["reason"])
	require.Equal(t, service.AuditFailureClassPendingSession, entry.Extra["reason_class"])
}

func splitRouteKey(routeKey string) (string, string) {
	parts := strings.SplitN(routeKey, " ", 2)
	return parts[0], parts[1]
}
