package admin

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func TestGeminiOAuthDeriveRedirectURIHardening(t *testing.T) {
	t.Parallel()

	makeCtx := func(host, remoteAddr string, withTLS bool, headers map[string]string) *gin.Context {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		req := httptest.NewRequest(http.MethodPost, "http://"+host+"/api/v1/admin/gemini/oauth/auth-url", nil)
		req.Host = host
		req.RemoteAddr = remoteAddr
		if withTLS {
			req.TLS = &tls.ConnectionState{}
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		c.Request = req
		return c
	}

	// Server-configured frontend_url overrides Origin, X-Forwarded-Host, X-Forwarded-Proto, and Host.
	hWithCfg := NewGeminiOAuthHandler(nil)
	hWithCfg.SetConfig(&config.Config{
		Server: config.ServerConfig{
			FrontendURL:              "https://app.example.com/",
			TrustedProxiesConfigured: true,
			TrustedProxies:           []string{"127.0.0.1/32"},
		},
	})
	cAttack := makeCtx("internal.local", "127.0.0.1:44321", false, map[string]string{
		"Origin":            "https://evil.example",
		"X-Forwarded-Host":  "evil.example",
		"X-Forwarded-Proto": "http",
	})
	require.Equal(t, "https://app.example.com/auth/callback", hWithCfg.deriveGeminiRedirectURI(cAttack))

	// Without server frontend_url, raw Origin and X-Forwarded-Host are NEVER trusted;
	// X-Forwarded-Proto is only trusted from explicitly configured non-wildcard proxies.
	hTrustedProxy := NewGeminiOAuthHandler(nil)
	hTrustedProxy.SetConfig(&config.Config{
		Server: config.ServerConfig{
			TrustedProxiesConfigured: true,
			TrustedProxies:           []string{"127.0.0.1/32"},
		},
	})
	cTrusted := makeCtx("app.example.com", "127.0.0.1:44321", false, map[string]string{
		"Origin":            "https://evil.example",
		"X-Forwarded-Host":  "evil.example",
		"X-Forwarded-Proto": "https",
	})
	require.Equal(t, "https://app.example.com/auth/callback", hTrustedProxy.deriveGeminiRedirectURI(cTrusted))

	// Untrusted peer cannot spoof X-Forwarded-Proto or Origin/X-Forwarded-Host.
	cUntrusted := makeCtx("app.example.com", "203.0.113.5:5555", false, map[string]string{
		"Origin":            "https://evil.example",
		"X-Forwarded-Host":  "evil.example",
		"X-Forwarded-Proto": "https",
	})
	require.Equal(t, "http://app.example.com/auth/callback", hTrustedProxy.deriveGeminiRedirectURI(cUntrusted))

	// Wildcard trusted proxies cannot authorize X-Forwarded-Proto.
	hWildcard := NewGeminiOAuthHandler(nil)
	hWildcard.SetConfig(&config.Config{
		Server: config.ServerConfig{
			TrustedProxiesConfigured: true,
			TrustedProxies:           []string{"0.0.0.0/0"},
		},
	})
	require.Equal(t, "http://app.example.com/auth/callback", hWildcard.deriveGeminiRedirectURI(cTrusted))

	// Direct TLS sets https even without trusted proxies.
	cTLS := makeCtx("app.example.com", "203.0.113.5:5555", true, map[string]string{
		"Origin": "https://evil.example",
	})
	require.Equal(t, "https://app.example.com/auth/callback", NewGeminiOAuthHandler(nil).deriveGeminiRedirectURI(cTLS))

	// Malformed Host returns empty string rather than injecting into redirect_uri.
	cBadHost := makeCtx("evil.example/path", "127.0.0.1:44321", false, nil)
	require.Equal(t, "", hTrustedProxy.deriveGeminiRedirectURI(cBadHost))
}
