package admin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type refreshTokenProxyAdminStub struct {
	service.AdminService
	proxy *service.Proxy
	err   error
	calls int
	id    int64
}

func (s *refreshTokenProxyAdminStub) GetProxy(_ context.Context, id int64) (*service.Proxy, error) {
	s.calls++
	s.id = id
	return s.proxy, s.err
}

type refreshTokenProxyOAuthStub struct {
	service.OpenAIOAuthClient
	calls        int
	refreshToken string
	proxyURL     string
	clientID     string
}

func (s *refreshTokenProxyOAuthStub) RefreshTokenWithClientID(_ context.Context, refreshToken, proxyURL, clientID string) (*openai.TokenResponse, error) {
	s.calls++
	s.refreshToken, s.proxyURL, s.clientID = refreshToken, proxyURL, clientID
	return &openai.TokenResponse{AccessToken: "fixture-access", RefreshToken: "fixture-refreshed", ExpiresIn: 3600}, nil
}

func TestOpenAIRefreshTokenRejectsUnavailableExplicitProxyBeforeUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tt := range []struct {
		name        string
		err         error
		wantStatus  int
		wantMessage string
	}{
		{"not_found_error", service.ErrProxyNotFound, http.StatusNotFound, "PROXY_NOT_FOUND"},
		{"lookup_failure", errors.New("fixture proxy lookup failed"), http.StatusInternalServerError, "internal error"},
		{"nil_proxy", nil, http.StatusBadRequest, "OPENAI_OAUTH_PROXY_NOT_FOUND"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			adminSvc := &refreshTokenProxyAdminStub{err: tt.err}
			oauthClient := &refreshTokenProxyOAuthStub{}
			oauthSvc := service.NewOpenAIOAuthService(nil, oauthClient)
			t.Cleanup(oauthSvc.Stop)
			handler := NewOpenAIOAuthHandler(oauthSvc, adminSvc, nil, nil)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/openai/refresh-token", strings.NewReader(`{"refresh_token":"fixture-refresh","proxy_id":71}`))
			c.Request.Header.Set("Content-Type", "application/json")

			handler.RefreshToken(c)

			require.Equal(t, tt.wantStatus, w.Code, w.Body.String())
			require.Contains(t, w.Body.String(), tt.wantMessage)
			require.Equal(t, 1, adminSvc.calls)
			require.Equal(t, int64(71), adminSvc.id)
			require.Zero(t, oauthClient.calls, "an explicitly requested proxy must never silently fall back to direct refresh")
			require.NotContains(t, w.Body.String(), "fixture-refresh")
		})
	}
}

func TestOpenAIRefreshTokenPreservesExplicitProxyAndOptionalDirectAccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	proxy := &service.Proxy{ID: 71, Protocol: "http", Host: "127.0.0.1", Port: 17890}
	for _, tt := range []struct {
		name           string
		body           string
		wantProxyURL   string
		wantProxyCalls int
	}{
		{"explicit_proxy", `{"refresh_token":"fixture-refresh","proxy_id":71,"client_id":"fixture-client"}`, proxy.URL(), 1},
		{"proxy_omitted", `{"rt":"fixture-refresh","client_id":"fixture-client"}`, "", 0},
		{"proxy_null", `{"refresh_token":"fixture-refresh","proxy_id":null,"client_id":"fixture-client"}`, "", 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			adminSvc := &refreshTokenProxyAdminStub{proxy: proxy}
			oauthClient := &refreshTokenProxyOAuthStub{}
			oauthSvc := service.NewOpenAIOAuthService(nil, oauthClient)
			t.Cleanup(oauthSvc.Stop)
			handler := NewOpenAIOAuthHandler(oauthSvc, adminSvc, nil, nil)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/openai/refresh-token", strings.NewReader(tt.body))
			c.Request.Header.Set("Content-Type", "application/json")

			handler.RefreshToken(c)

			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			require.Equal(t, tt.wantProxyCalls, adminSvc.calls)
			require.Equal(t, 1, oauthClient.calls)
			require.Equal(t, tt.wantProxyURL, oauthClient.proxyURL)
			require.Equal(t, "fixture-refresh", oauthClient.refreshToken)
			require.Equal(t, "fixture-client", oauthClient.clientID)
		})
	}
}
