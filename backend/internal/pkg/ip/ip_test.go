//go:build unit

package ip

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGetTrustedClientIPUsesGinClientIP(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	require.NoError(t, r.SetTrustedProxies(nil))

	r.GET("/t", func(c *gin.Context) {
		c.String(200, GetTrustedClientIP(c))
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/t", nil)
	req.RemoteAddr = "9.9.9.9:12345"
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	req.Header.Set("X-Real-IP", "1.2.3.4")
	req.Header.Set("CF-Connecting-IP", "1.2.3.4")
	r.ServeHTTP(w, req)

	require.Equal(t, 200, w.Code)
	require.Equal(t, "9.9.9.9", w.Body.String())
}

// 直连源站的对端不是可信代理：所有转发头（包括 CF-Connecting-IP）都必须被丢弃。
func TestGetClientIPDirectPeerIgnoresSpoofedHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	require.NoError(t, r.SetTrustedProxies(nil))
	r.GET("/t", func(c *gin.Context) {
		SetForwardedIPSettings(c, true, nil, []string{"10.0.0.0/8", "127.0.0.1"})
		c.String(200, GetClientIP(c))
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/t", nil)
	req.RemoteAddr = "198.51.100.7:12345"
	req.Header.Set("CF-Connecting-IP", "203.0.113.9")
	req.Header.Set("X-Real-IP", "203.0.113.9")
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	r.ServeHTTP(w, req)

	require.Equal(t, 200, w.Code)
	require.Equal(t, "198.51.100.7", w.Body.String())
}

// 反代/容器链路：只有 TCP 对端在 trusted_proxies 内才解析转发头。
func TestGetClientIPTrustedProxyResolvesRealClient(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	require.NoError(t, r.SetTrustedProxies(nil))
	r.GET("/t", func(c *gin.Context) {
		SetForwardedIPSettings(c, true, nil, []string{"192.168.32.1"})
		c.String(200, GetClientIP(c))
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/t", nil)
	req.RemoteAddr = "192.168.32.1:12345"
	req.Header.Set("X-Forwarded-For", "10.0.0.2, 203.0.113.42")
	req.Header.Set("X-Real-IP", "192.168.32.1")
	r.ServeHTTP(w, req)

	require.Equal(t, 200, w.Code)
	require.Equal(t, "203.0.113.42", w.Body.String())
}

// 未配置 trusted_proxies 时 fail-closed：即使开启兼容开关也不认转发头。
func TestGetSecurityClientIPWithoutTrustedProxiesFailsClosed(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	require.NoError(t, r.SetTrustedProxies(nil))
	r.GET("/t", func(c *gin.Context) {
		SetForwardedIPSettings(c, true, nil, nil)
		c.String(200, GetSecurityClientIP(c, true))
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/t", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("CF-Connecting-IP", "203.0.113.9")
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	r.ServeHTTP(w, req)

	require.Equal(t, 200, w.Code)
	require.Equal(t, "127.0.0.1", w.Body.String())
}

// XFF 从右往左跳过可信代理，取第一个非可信地址；客户端自带的左侧值不能胜出。
func TestGetSecurityClientIPTrustedProxyXFFRightmost(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	require.NoError(t, r.SetTrustedProxies(nil))
	r.GET("/t", func(c *gin.Context) {
		SetForwardedIPSettings(c, true, nil, []string{"10.0.0.0/8"})
		c.String(200, GetSecurityClientIP(c, true))
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/t", nil)
	req.RemoteAddr = "10.0.0.5:12345"
	// 客户端伪造了最左侧 1.2.3.4；10.0.0.5 是最近一跳可信代理。
	req.Header.Set("X-Forwarded-For", "1.2.3.4, 203.0.113.9, 10.0.0.5")
	r.ServeHTTP(w, req)

	require.Equal(t, 200, w.Code)
	require.Equal(t, "203.0.113.9", w.Body.String())
}

func TestGetSecurityClientIPTrustedProxyIPv6(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	require.NoError(t, r.SetTrustedProxies(nil))
	r.GET("/t", func(c *gin.Context) {
		SetForwardedIPSettings(c, true, nil, []string{"2001:db8::/32"})
		c.String(200, GetSecurityClientIP(c, true))
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/t", nil)
	req.RemoteAddr = "[2001:db8::5]:12345"
	req.Header.Set("X-Forwarded-For", "2001:db9::1, 2001:db8::9, 2001:db8::5")
	r.ServeHTTP(w, req)

	require.Equal(t, 200, w.Code)
	require.Equal(t, "2001:db9::1", w.Body.String())
}

func TestRightmostForwardedForClientIP(t *testing.T) {
	trusted := parseTrustedProxyPrefixes([]string{"10.0.0.0/8", "2001:db8::/32"})

	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "empty", raw: "", want: ""},
		{name: "single client", raw: "203.0.113.9", want: "203.0.113.9"},
		{name: "skips rightmost trusted hops", raw: "198.51.100.1, 203.0.113.9, 10.0.0.5", want: "203.0.113.9"},
		{name: "all hops trusted means no client", raw: "10.0.0.2, 10.0.0.5", want: ""},
		{name: "invalid entries are skipped", raw: "unknown, 203.0.113.9", want: "203.0.113.9"},
		{name: "ipv6 trusted hop", raw: "2001:db9::1, 2001:db8::5", want: "2001:db9::1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, rightmostForwardedForClientIP(test.raw, trusted))
		})
	}
}

// API Key IP 白名单：伪造头不能放行，真实可信链地址可以放行也可以被拒绝。
func TestAPIKeyIPWhitelistBothDirections(t *testing.T) {
	gin.SetMode(gin.TestMode)
	whitelist := CompileIPRules([]string{"203.0.113.9"})

	resolve := func(remoteAddr string, headers map[string]string) string {
		var clientIP string
		r := gin.New()
		require.NoError(t, r.SetTrustedProxies(nil))
		r.GET("/t", func(c *gin.Context) {
			SetForwardedIPSettings(c, true, nil, []string{"10.0.0.0/8"})
			clientIP = GetSecurityClientIP(c, true)
			c.Status(200)
		})
		req := httptest.NewRequest("GET", "/t", nil)
		req.RemoteAddr = remoteAddr
		for name, value := range headers {
			req.Header.Set(name, value)
		}
		r.ServeHTTP(httptest.NewRecorder(), req)
		return clientIP
	}

	// 拒绝方向：不可信直连对端伪造白名单 IP 无效。
	spoofed := resolve("198.51.100.7:12345", map[string]string{
		"CF-Connecting-IP": "203.0.113.9",
		"X-Forwarded-For":  "203.0.113.9",
	})
	require.Equal(t, "198.51.100.7", spoofed)
	allowed, _ := CheckIPRestrictionWithCompiledRules(spoofed, whitelist, nil)
	require.False(t, allowed)

	// 放行方向：可信反代传来的真实客户端命中白名单。
	trusted := resolve("10.0.0.5:12345", map[string]string{
		"X-Forwarded-For": "203.0.113.9, 10.0.0.5",
	})
	require.Equal(t, "203.0.113.9", trusted)
	allowed, _ = CheckIPRestrictionWithCompiledRules(trusted, whitelist, nil)
	require.True(t, allowed)

	// 拒绝方向：可信反代后真实客户端不在白名单。
	other := resolve("10.0.0.5:12345", map[string]string{
		"X-Forwarded-For": "198.51.100.7, 10.0.0.5",
	})
	require.Equal(t, "198.51.100.7", other)
	allowed, _ = CheckIPRestrictionWithCompiledRules(other, whitelist, nil)
	require.False(t, allowed)
}

func TestCheckIPRestrictionWithCompiledRules(t *testing.T) {
	whitelist := CompileIPRules([]string{"10.0.0.0/8", "192.168.1.2"})
	blacklist := CompileIPRules([]string{"10.1.1.1"})

	allowed, reason := CheckIPRestrictionWithCompiledRules("10.2.3.4", whitelist, blacklist)
	require.True(t, allowed)
	require.Equal(t, "", reason)

	allowed, reason = CheckIPRestrictionWithCompiledRules("10.1.1.1", whitelist, blacklist)
	require.False(t, allowed)
	require.Equal(t, "access denied", reason)
}

func TestCheckIPRestrictionWithCompiledRules_InvalidWhitelistStillDenies(t *testing.T) {
	// 与旧实现保持一致：白名单有配置但全无效时，最终应拒绝访问。
	invalidWhitelist := CompileIPRules([]string{"not-a-valid-pattern"})
	allowed, _ := CheckIPRestrictionWithCompiledRules("8.8.8.8", invalidWhitelist, nil)
	require.False(t, allowed)
}

func TestGetSecurityClientIPSwitchEnabledUsesLegacyHeadersOnlyBehindTrustedProxy(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	require.NoError(t, r.SetTrustedProxies(nil))
	r.GET("/t", func(c *gin.Context) {
		SetForwardedIPSettings(c, true, nil, []string{"9.9.9.9"})
		c.String(200, GetSecurityClientIP(c, true))
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/t", nil)
	req.RemoteAddr = "9.9.9.9:12345"
	req.Header.Set("X-Real-IP", "1.2.3.4")
	r.ServeHTTP(w, req)

	require.Equal(t, 200, w.Code)
	require.Equal(t, "1.2.3.4", w.Body.String())
}

func TestGetSecurityClientIPCustomHeaderPrecedenceAndFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name           string
		trustForward   bool
		trustedProxies []string
		headers        []string
		requestHeaders map[string]string
		want           string
	}{
		{
			name:           "configured order precedes built-ins",
			trustForward:   true,
			trustedProxies: []string{"9.9.9.9"},
			headers:        []string{"X-CDN-First", "X-CDN-Second"},
			requestHeaders: map[string]string{
				"X-CDN-First":      "198.51.100.10",
				"X-CDN-Second":     "203.0.113.20",
				"CF-Connecting-IP": "8.8.8.8",
			},
			want: "198.51.100.10",
		},
		{
			name:           "comma candidates skip invalid and private values",
			trustForward:   true,
			trustedProxies: []string{"9.9.9.9"},
			headers:        []string{"X-CDN-First", "X-CDN-Second"},
			requestHeaders: map[string]string{
				"X-CDN-First":  "not-an-ip, 10.0.0.8",
				"X-CDN-Second": "also-bad, 203.0.113.9",
			},
			want: "203.0.113.9",
		},
		{
			name:           "legacy public header wins over custom private fallback",
			trustForward:   true,
			trustedProxies: []string{"9.9.9.9"},
			headers:        []string{"X-CDN-IP"},
			requestHeaders: map[string]string{
				"X-CDN-IP":  "10.0.0.8",
				"X-Real-IP": "1.2.3.4",
			},
			want: "1.2.3.4",
		},
		{
			name:           "trusted legacy header is returned as-is",
			trustForward:   true,
			trustedProxies: []string{"9.9.9.9"},
			headers:        []string{"X-CDN-IP"},
			requestHeaders: map[string]string{
				"X-CDN-IP":  "10.0.0.8",
				"X-Real-IP": "192.168.1.4",
			},
			want: "192.168.1.4",
		},
		{
			name:           "invalid custom value continues to built-ins",
			trustForward:   true,
			trustedProxies: []string{"9.9.9.9"},
			headers:        []string{"X-CDN-IP"},
			requestHeaders: map[string]string{
				"X-CDN-IP":         "1.2.3.4:443",
				"CF-Connecting-IP": "4.4.4.4",
			},
			want: "4.4.4.4",
		},
		{
			name:           "invalid legacy values continue to a valid forwarded address",
			trustForward:   true,
			trustedProxies: []string{"9.9.9.9"},
			requestHeaders: map[string]string{
				"CF-Connecting-IP": "unknown",
				"X-Real-IP":        "proxy.internal",
				"X-Forwarded-For":  "also-invalid, 203.0.113.50",
			},
			want: "203.0.113.50",
		},
		{
			name:           "all invalid legacy values fall back to the connection address",
			trustForward:   true,
			trustedProxies: []string{"9.9.9.9"},
			requestHeaders: map[string]string{
				"CF-Connecting-IP": "unknown",
				"X-Real-IP":        "proxy.internal",
				"X-Forwarded-For":  "also-invalid",
			},
			want: "9.9.9.9",
		},
		{
			name:           "disabled mode ignores custom and legacy headers",
			trustForward:   false,
			trustedProxies: []string{"9.9.9.9"},
			headers:        []string{"X-CDN-IP"},
			requestHeaders: map[string]string{
				"X-CDN-IP":  "1.2.3.4",
				"X-Real-IP": "4.4.4.4",
			},
			want: "9.9.9.9",
		},
		{
			name:           "untrusted peer ignores every header",
			trustForward:   true,
			trustedProxies: []string{"10.0.0.0/8"},
			headers:        []string{"X-CDN-IP"},
			requestHeaders: map[string]string{
				"X-CDN-IP":         "198.51.100.10",
				"CF-Connecting-IP": "198.51.100.10",
			},
			want: "9.9.9.9",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			r := gin.New()
			require.NoError(t, r.SetTrustedProxies(nil))
			r.GET("/t", func(c *gin.Context) {
				SetForwardedIPSettings(c, test.trustForward, test.headers, test.trustedProxies)
				c.String(200, GetSecurityClientIP(c, !test.trustForward))
			})

			w := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/t", nil)
			req.RemoteAddr = "9.9.9.9:12345"
			for name, value := range test.requestHeaders {
				req.Header.Set(name, value)
			}
			r.ServeHTTP(w, req)

			require.Equal(t, test.want, w.Body.String())
		})
	}
}

func TestGetSecurityClientIPSwitchDisabledUsesConfiguredTrustedProxy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	require.NoError(t, r.SetTrustedProxies([]string{"9.9.9.9"}))
	r.GET("/t", func(c *gin.Context) { c.String(200, GetSecurityClientIP(c, false)) })

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/t", nil)
	req.RemoteAddr = "9.9.9.9:12345"
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	r.ServeHTTP(w, req)

	require.Equal(t, "1.2.3.4", w.Body.String())
}

func TestGetClientIPSwitchDisabledUsesTrustedProxyChain(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	require.NoError(t, r.SetTrustedProxies(nil))
	r.GET("/t", func(c *gin.Context) {
		SetLegacyForwardedIPTrust(c, false)
		c.String(200, GetClientIP(c))
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/t", nil)
	req.RemoteAddr = "9.9.9.9:12345"
	req.Header.Set("X-Real-IP", "1.2.3.4")
	r.ServeHTTP(w, req)

	require.Equal(t, "9.9.9.9", w.Body.String())
}

func TestGetSecurityClientIPRequestSnapshotCopiesCustomHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	require.NoError(t, r.SetTrustedProxies(nil))
	r.GET("/t", func(c *gin.Context) {
		headers := []string{"X-Original-IP"}
		SetForwardedIPSettings(c, true, headers, []string{"9.9.9.9"})
		headers[0] = "X-Mutated-IP"
		c.String(200, GetSecurityClientIP(c, false))
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/t", nil)
	req.RemoteAddr = "9.9.9.9:12345"
	req.Header.Set("X-Original-IP", "1.2.3.4")
	req.Header.Set("X-Mutated-IP", "4.4.4.4")
	r.ServeHTTP(w, req)

	require.Equal(t, "1.2.3.4", w.Body.String())
}

func TestGetSecurityClientIPRequestSnapshotOverridesLiveFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name          string
		requestTrust  bool
		trustedProxy  []string
		fallbackTrust bool
		want          string
	}{
		{name: "captured secure mode wins", requestTrust: false, trustedProxy: []string{"9.9.9.9"}, fallbackTrust: true, want: "9.9.9.9"},
		{name: "captured compatibility mode wins behind trusted peer", requestTrust: true, trustedProxy: []string{"9.9.9.9"}, fallbackTrust: false, want: "1.2.3.4"},
		{name: "captured compatibility mode without trusted peer falls back to peer", requestTrust: true, fallbackTrust: false, want: "9.9.9.9"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			r := gin.New()
			require.NoError(t, r.SetTrustedProxies(nil))
			r.GET("/t", func(c *gin.Context) {
				SetForwardedIPSettings(c, test.requestTrust, nil, test.trustedProxy)
				c.String(200, GetSecurityClientIP(c, test.fallbackTrust))
			})

			w := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/t", nil)
			req.RemoteAddr = "9.9.9.9:12345"
			req.Header.Set("X-Real-IP", "1.2.3.4")
			r.ServeHTTP(w, req)

			require.Equal(t, test.want, w.Body.String())
		})
	}
}
