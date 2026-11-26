package handler

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/ent/pendingauthsession"
	"github.com/Wei-Shaw/sub2api/internal/config"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestLogoutClearsOAuthStateCookiesAndConsumesPendingSession(t *testing.T) {
	handler, client := newOAuthPendingFlowTestHandler(t, false)
	ctx := context.Background()

	session, err := client.PendingAuthSession.Create().
		SetSessionToken("logout-pending-session-token").
		SetIntent("login").
		SetProviderType("oidc").
		SetProviderKey("https://issuer.example").
		SetProviderSubject("logout-subject-123").
		SetBrowserSessionKey("logout-browser-session-key").
		SetResolvedEmail("logout@example.com").
		SetExpiresAt(time.Now().UTC().Add(10 * time.Minute)).
		Save(ctx)
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	req.AddCookie(&http.Cookie{Name: oauthPendingSessionCookieName, Value: encodeCookieValue(session.SessionToken)})
	req.AddCookie(&http.Cookie{Name: oauthPendingBrowserCookieName, Value: encodeCookieValue("logout-browser-session-key")})
	req.AddCookie(&http.Cookie{Name: oauthBindAccessTokenCookieName, Value: "bind-access-token"})
	req.AddCookie(&http.Cookie{Name: linuxDoOAuthStateCookieName, Value: encodeCookieValue("linuxdo-state")})
	req.AddCookie(&http.Cookie{Name: oidcOAuthStateCookieName, Value: encodeCookieValue("oidc-state")})
	req.AddCookie(&http.Cookie{Name: wechatOAuthStateCookieName, Value: encodeCookieValue("wechat-state")})
	req.AddCookie(&http.Cookie{Name: wechatPaymentOAuthStateName, Value: encodeCookieValue("wechat-payment-state")})
	ginCtx.Request = req

	handler.Logout(ginCtx)

	require.Equal(t, http.StatusOK, recorder.Code)

	cookies := recorder.Result().Cookies()
	for _, name := range []string{
		oauthPendingSessionCookieName,
		oauthPendingBrowserCookieName,
		oauthBindAccessTokenCookieName,
		linuxDoOAuthStateCookieName,
		oidcOAuthStateCookieName,
		wechatOAuthStateCookieName,
		wechatPaymentOAuthStateName,
	} {
		cookie := findCookie(cookies, name)
		require.NotNil(t, cookie, name)
		require.Equal(t, -1, cookie.MaxAge, name)
		require.True(t, cookie.HttpOnly, name)
	}

	storedSession, err := client.PendingAuthSession.Query().
		Where(pendingauthsession.IDEQ(session.ID)).
		Only(ctx)
	require.NoError(t, err)
	require.NotNil(t, storedSession.ConsumedAt)
	// 只清理请求实际携带的 7 个 OAuth Cookie，不附带其余未携带的 OAuth/2FA 清理头。
	require.Len(t, cookies, 7)
}

func serializedResponseHeaderSize(t *testing.T, resp *http.Response) int {
	t.Helper()
	var buf bytes.Buffer
	_, err := buf.WriteString("HTTP/1.1 200 OK\r\n")
	require.NoError(t, err)
	require.NoError(t, resp.Header.Write(&buf))
	_, err = buf.WriteString("\r\n")
	require.NoError(t, err)
	return buf.Len()
}

func TestLogoutWithoutOAuthOrTwoFactorCookiesKeepsHeadersUnder4KB(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, _ := newOAuthPendingFlowTestHandler(t, false)

	router := gin.New()
	router.Use(middleware2.SecurityHeaders(config.CSPConfig{Enabled: true}, nil))
	router.POST("/api/v1/auth/logout", handler.Logout)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "unrelated_cookie", Value: "keep-me"})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusOK, recorder.Code)
	resp := recorder.Result()
	require.Empty(t, resp.Cookies(), "未携带 OAuth/2FA Cookie 时不应下发任何 Set-Cookie 清理头")
	require.Empty(t, resp.Header.Values("Set-Cookie"))

	headerBytes := serializedResponseHeaderSize(t, resp)
	// 含完整 SecurityHeaders（约 1.3KB CSP）在内，无 OAuth/2FA Cookie 时仍远低于 Nginx 默认 4KB proxy_buffer_size（实测约 1.5KB）。
	require.Less(t, headerBytes, 2048, "无 OAuth/2FA Cookie 时响应头应远低于 Nginx 默认 4KB 缓冲上限，实际=%d", headerBytes)
}

func TestLogoutClearsOnlyPresentTwoFactorAndOAuthCookies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, _ := newOAuthPendingFlowTestHandler(t, false)

	t.Run("only totp grant cookie present", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		ginCtx, _ := gin.CreateTestContext(recorder)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", bytes.NewBufferString(`{}`))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: service.TwoFactorGrantCookieName, Value: "totp-grant-token"})
		req.AddCookie(&http.Cookie{Name: "unrelated_cookie", Value: "ignored"})
		ginCtx.Request = req

		handler.Logout(ginCtx)

		require.Equal(t, http.StatusOK, recorder.Code)
		cookies := recorder.Result().Cookies()
		require.Len(t, cookies, 1)
		totpCookie := findCookie(cookies, service.TwoFactorGrantCookieName)
		require.NotNil(t, totpCookie)
		require.Equal(t, -1, totpCookie.MaxAge)
		require.Equal(t, "/", totpCookie.Path)
		require.True(t, totpCookie.HttpOnly)
		require.True(t, totpCookie.Secure)
		require.Equal(t, http.SameSiteLaxMode, totpCookie.SameSite)
		require.Nil(t, findCookie(cookies, service.TwoFactorPasskeyGrantCookieName))
		require.Less(t, serializedResponseHeaderSize(t, recorder.Result()), 1024)
	})

	t.Run("subset of oauth and passkey grant cookies present", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		ginCtx, _ := gin.CreateTestContext(recorder)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", bytes.NewBufferString(`{}`))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: oidcOAuthStateCookieName, Value: encodeCookieValue("oidc-state")})
		req.AddCookie(&http.Cookie{Name: service.TwoFactorPasskeyGrantCookieName, Value: "passkey-grant-token"})
		req.AddCookie(&http.Cookie{Name: "unrelated_cookie", Value: "ignored"})
		ginCtx.Request = req

		handler.Logout(ginCtx)

		require.Equal(t, http.StatusOK, recorder.Code)
		cookies := recorder.Result().Cookies()
		require.Len(t, cookies, 2)

		oidcCookie := findCookie(cookies, oidcOAuthStateCookieName)
		require.NotNil(t, oidcCookie)
		require.Equal(t, -1, oidcCookie.MaxAge)
		require.Equal(t, oidcOAuthCookiePath, oidcCookie.Path)
		require.True(t, oidcCookie.HttpOnly)

		passkeyCookie := findCookie(cookies, service.TwoFactorPasskeyGrantCookieName)
		require.NotNil(t, passkeyCookie)
		require.Equal(t, -1, passkeyCookie.MaxAge)
		require.Equal(t, "/", passkeyCookie.Path)
		require.True(t, passkeyCookie.HttpOnly)
		require.True(t, passkeyCookie.Secure)

		require.Nil(t, findCookie(cookies, linuxDoOAuthStateCookieName))
		require.Nil(t, findCookie(cookies, service.TwoFactorGrantCookieName))
		require.Less(t, serializedResponseHeaderSize(t, recorder.Result()), 1024)
	})
}
