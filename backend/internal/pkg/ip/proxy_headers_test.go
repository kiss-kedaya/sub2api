package ip

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestIsTrustedProxyPeer(t *testing.T) {
	t.Parallel()

	require.False(t, IsTrustedProxyPeer("127.0.0.1:12345", []string{"127.0.0.1/32"}, false))
	require.False(t, IsTrustedProxyPeer("127.0.0.1:12345", nil, true))
	require.False(t, IsTrustedProxyPeer("127.0.0.1:12345", []string{"0.0.0.0/0"}, true))
	require.False(t, IsTrustedProxyPeer("[::1]:12345", []string{"::/0"}, true))
	require.False(t, IsTrustedProxyPeer("127.0.0.1:12345", []string{"::ffff:0.0.0.0/96"}, true))
	require.False(t, IsTrustedProxyPeer("203.0.113.10:443", []string{"127.0.0.1/32", "10.10.0.1/32"}, true))

	require.True(t, IsTrustedProxyPeer("127.0.0.1:54321", []string{"127.0.0.1/32"}, true))
	require.True(t, IsTrustedProxyPeer("[::ffff:127.0.0.1]:54321", []string{"127.0.0.1/32"}, true))
	require.True(t, IsTrustedProxyPeer("[::1]:54321", []string{"::1/128"}, true))
	require.True(t, IsTrustedProxyPeer("10.10.0.2:443", []string{"10.10.0.1/32", "10.10.0.2/32"}, true))
}

func TestIsRequestHTTPSAndContextHTTPS(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	makeCtx := func(remoteAddr string, headers map[string][]string, withTLS bool) *gin.Context {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		req := httptest.NewRequest(http.MethodGet, "http://app.example.com/", nil)
		req.RemoteAddr = remoteAddr
		if withTLS {
			req.TLS = &tls.ConnectionState{}
		}
		for k, vals := range headers {
			for _, v := range vals {
				req.Header.Add(k, v)
			}
		}
		c.Request = req
		return c
	}

	// Direct TLS always counts as HTTPS.
	require.True(t, IsRequestHTTPS(makeCtx("203.0.113.9:443", nil, true), nil, false))
	require.True(t, IsContextHTTPS(makeCtx("203.0.113.9:443", nil, true)))

	// Untrusted peer or unconfigured / wildcard proxy never trusts X-Forwarded-Proto.
	cUntrusted := makeCtx("203.0.113.9:443", map[string][]string{"X-Forwarded-Proto": {"https"}}, false)
	require.False(t, IsRequestHTTPS(cUntrusted, []string{"127.0.0.1/32"}, true))
	require.False(t, IsContextHTTPS(cUntrusted))

	cLoopback := makeCtx("127.0.0.1:1234", map[string][]string{"X-Forwarded-Proto": {"https"}}, false)
	require.False(t, IsRequestHTTPS(cLoopback, []string{"127.0.0.1/32"}, false))
	require.False(t, IsRequestHTTPS(cLoopback, []string{"0.0.0.0/0"}, true))
	require.True(t, IsRequestHTTPS(cLoopback, []string{"127.0.0.1/32"}, true))

	// Multi-value / duplicate X-Forwarded-Proto is rejected.
	cComma := makeCtx("127.0.0.1:1234", map[string][]string{"X-Forwarded-Proto": {"https, http"}}, false)
	require.False(t, IsRequestHTTPS(cComma, []string{"127.0.0.1/32"}, true))
	cDup := makeCtx("127.0.0.1:1234", map[string][]string{"X-Forwarded-Proto": {"https", "http"}}, false)
	require.False(t, IsRequestHTTPS(cDup, []string{"127.0.0.1/32"}, true))

	// Context-based trust honors SetTrustedProxyConfig and ignores wildcards.
	SetTrustedProxyConfig(cLoopback, []string{"0.0.0.0/0"}, true)
	require.False(t, IsContextHTTPS(cLoopback))
	SetTrustedProxyConfig(cLoopback, []string{"127.0.0.1/32"}, true)
	require.True(t, IsContextHTTPS(cLoopback))
}

func TestValidateRequestHostAndTrustedForwardedHost(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	for _, valid := range []string{
		"app.example.com",
		"app.example.com:443",
		"sub.domain-1.example.com:8443",
		"127.0.0.1:17777",
		"[::1]",
		"[::1]:8080",
	} {
		got, ok := ValidateRequestHost(valid)
		require.True(t, ok, "expected valid host %q", valid)
		require.Equal(t, valid, got)
	}

	for _, invalid := range []string{
		"",
		"   ",
		"evil.example, app.example.com",
		"user@evil.example",
		"evil.example/path",
		`evil.example\path`,
		"evil.example?q=1",
		"evil.example#frag",
		"evil.example extra",
		"evil.example\r\nX: y",
		"evil.example:",
		"evil.example:0",
		"evil.example:080",
		"evil.example:0443",
		"evil.example:65536",
		"evil.example:abc",
		"[fe80::1%eth0]",
		"[fe80::1%25eth0]:443",
		"-bad.example",
		"bad..example",
	} {
		_, ok := ValidateRequestHost(invalid)
		require.False(t, ok, "expected invalid host %q to be rejected", invalid)
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodGet, "http://app.example.com/", nil)
	req.RemoteAddr = "127.0.0.1:5000"
	req.Header.Set("X-Forwarded-Host", "pay.app.example.com:8443")
	c.Request = req

	_, ok := TrustedForwardedHost(c, []string{"127.0.0.1/32"}, false)
	require.False(t, ok)
	_, ok = TrustedForwardedHost(c, []string{"0.0.0.0/0"}, true)
	require.False(t, ok)
	got, ok := TrustedForwardedHost(c, []string{"127.0.0.1/32"}, true)
	require.True(t, ok)
	require.Equal(t, "pay.app.example.com:8443", got)

	req.Header.Add("X-Forwarded-Host", "second.example")
	_, ok = TrustedForwardedHost(c, []string{"127.0.0.1/32"}, true)
	require.False(t, ok)
}

func TestExtractHTTPOrigin(t *testing.T) {
	t.Parallel()

	got, ok := ExtractHTTPOrigin("https://app.example.com/v1")
	require.True(t, ok)
	require.Equal(t, "https://app.example.com", got)

	got, ok = ExtractHTTPOrigin("HTTP://Example.COM:8080/payment/result?a=1")
	require.True(t, ok)
	require.Equal(t, "http://Example.COM:8080", got)

	for _, bad := range []string{
		"",
		"/relative",
		"javascript:alert(1)",
		"https://user:pass@app.example.com",
		`https://app.example.com\evil.example`,
		"https://evil.example,app.example.com",
	} {
		_, ok = ExtractHTTPOrigin(bad)
		require.False(t, ok, "expected %q to fail", bad)
	}
}
