package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpsWSClientIPIgnoresSpoofedLeftmostXForwardedFor(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("SessionBindingContext with trusted proxy resolves rightmost untrusted hop", func(t *testing.T) {
		for _, trustForwarded := range []bool{true, false} {
			cfg := &config.Config{
				Server: config.ServerConfig{
					TrustedProxies:           []string{"127.0.0.1", "10.0.0.0/8"},
					TrustedProxiesConfigured: true,
				},
				Security: config.SecurityConfig{
					TrustForwardedIPForAPIKeyACL: trustForwarded,
				},
			}

			r := gin.New()
			require.NoError(t, r.SetTrustedProxies(cfg.Server.TrustedProxies))
			r.Use(servermiddleware.SessionBindingContext(cfg))
			var gotIP string
			r.GET("/ws", func(c *gin.Context) {
				gotIP = opsWSClientIP(c)
				c.Status(http.StatusOK)
			})

			req := httptest.NewRequest(http.MethodGet, "/ws", nil)
			req.RemoteAddr = "127.0.0.1:43210"
			req.Header.Set("X-Forwarded-For", "1.2.3.4, 192.0.2.5, 203.0.113.9, 10.0.0.5")
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			require.Equal(t, http.StatusOK, rec.Code)
			require.Equal(t, "203.0.113.9", gotIP, "trustForwarded=%v must ignore spoofed left-most XFF", trustForwarded)
		}
	})

	t.Run("SessionBindingContext ignores XFF from untrusted direct peer", func(t *testing.T) {
		cfg := &config.Config{
			Server: config.ServerConfig{
				TrustedProxies:           []string{"127.0.0.1"},
				TrustedProxiesConfigured: true,
			},
			Security: config.SecurityConfig{
				TrustForwardedIPForAPIKeyACL: true,
			},
		}
		r := gin.New()
		require.NoError(t, r.SetTrustedProxies(cfg.Server.TrustedProxies))
		r.Use(servermiddleware.SessionBindingContext(cfg))
		var gotIP string
		r.GET("/ws", func(c *gin.Context) {
			gotIP = opsWSClientIP(c)
			c.Status(http.StatusOK)
		})

		req := httptest.NewRequest(http.MethodGet, "/ws", nil)
		req.RemoteAddr = "198.51.100.22:55555"
		req.Header.Set("X-Forwarded-For", "1.2.3.4, 203.0.113.9")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
		require.Equal(t, "198.51.100.22", gotIP)
	})

	t.Run("Gin fallback without SessionBindingContext respects SetTrustedProxies", func(t *testing.T) {
		rTrusted := gin.New()
		require.NoError(t, rTrusted.SetTrustedProxies([]string{"127.0.0.1"}))
		var gotTrusted string
		rTrusted.GET("/ws", func(c *gin.Context) {
			gotTrusted = opsWSClientIP(c)
			c.Status(http.StatusOK)
		})
		req := httptest.NewRequest(http.MethodGet, "/ws", nil)
		req.RemoteAddr = "127.0.0.1:40000"
		req.Header.Set("X-Forwarded-For", "9.9.9.9, 203.0.113.42")
		rTrusted.ServeHTTP(httptest.NewRecorder(), req)
		require.Equal(t, "203.0.113.42", gotTrusted)

		rUntrusted := gin.New()
		require.NoError(t, rUntrusted.SetTrustedProxies(nil))
		var gotUntrusted string
		rUntrusted.GET("/ws", func(c *gin.Context) {
			gotUntrusted = opsWSClientIP(c)
			c.Status(http.StatusOK)
		})
		req2 := httptest.NewRequest(http.MethodGet, "/ws", nil)
		req2.RemoteAddr = "127.0.0.1:40000"
		req2.Header.Set("X-Forwarded-For", "9.9.9.9, 203.0.113.42")
		rUntrusted.ServeHTTP(httptest.NewRecorder(), req2)
		require.Equal(t, "127.0.0.1", gotUntrusted)
	})

	t.Run("OPS_WS_TRUST_PROXY=false ignores XFF even from trusted proxy peer", func(t *testing.T) {
		prev := opsWSProxyConfig
		opsWSProxyConfig = OpsWSProxyConfig{
			TrustProxy:   false,
			OriginPolicy: OriginPolicyPermissive,
		}
		t.Cleanup(func() { opsWSProxyConfig = prev })

		cfg := &config.Config{
			Server: config.ServerConfig{
				TrustedProxies:           []string{"127.0.0.1"},
				TrustedProxiesConfigured: true,
			},
		}
		r := gin.New()
		require.NoError(t, r.SetTrustedProxies(cfg.Server.TrustedProxies))
		r.Use(servermiddleware.SessionBindingContext(cfg))
		var gotIP string
		r.GET("/ws", func(c *gin.Context) {
			gotIP = opsWSClientIP(c)
			c.Status(http.StatusOK)
		})

		req := httptest.NewRequest(http.MethodGet, "/ws", nil)
		req.RemoteAddr = "127.0.0.1:41234"
		req.Header.Set("X-Forwarded-For", "1.2.3.4, 203.0.113.55")
		r.ServeHTTP(httptest.NewRecorder(), req)
		require.Equal(t, "127.0.0.1", gotIP)
	})

	t.Run("explicit OPS_WS_TRUSTED_PROXIES walks XFF right-to-left and ignores spoofed left-most IP", func(t *testing.T) {
		prefixes, invalid := parseTrustedProxyList("10.0.0.0/8")
		require.Empty(t, invalid)
		prev := opsWSProxyConfig
		opsWSProxyConfig = OpsWSProxyConfig{
			TrustProxy:             true,
			TrustedProxies:         prefixes,
			TrustedProxiesExplicit: true,
			OriginPolicy:           OriginPolicyPermissive,
		}
		t.Cleanup(func() { opsWSProxyConfig = prev })

		// Server trusted_proxies only includes loopback, while OPS_WS_TRUSTED_PROXIES
		// explicitly trusts 10.0.0.0/8.
		cfg := &config.Config{
			Server: config.ServerConfig{
				TrustedProxies:           []string{"127.0.0.1"},
				TrustedProxiesConfigured: true,
			},
		}
		r := gin.New()
		require.NoError(t, r.SetTrustedProxies(cfg.Server.TrustedProxies))
		r.Use(servermiddleware.SessionBindingContext(cfg))
		var gotIP string
		r.GET("/ws", func(c *gin.Context) {
			gotIP = opsWSClientIP(c)
			c.Status(http.StatusOK)
		})

		req := httptest.NewRequest(http.MethodGet, "/ws", nil)
		req.RemoteAddr = "10.0.0.5:41234"
		req.Header.Add("X-Forwarded-For", "1.2.3.4, 192.0.2.5")
		req.Header.Add("X-Forwarded-For", "203.0.113.88, 10.0.0.9")
		r.ServeHTTP(httptest.NewRecorder(), req)
		require.Equal(t, "203.0.113.88", gotIP)

		// Direct untrusted peer still cannot spoof XFF.
		reqUntrusted := httptest.NewRequest(http.MethodGet, "/ws", nil)
		reqUntrusted.RemoteAddr = "198.51.100.44:41234"
		reqUntrusted.Header.Set("X-Forwarded-For", "1.2.3.4, 203.0.113.88")
		r.ServeHTTP(httptest.NewRecorder(), reqUntrusted)
		require.Equal(t, "198.51.100.44", gotIP)
	})
}

func TestOpsWSPerIPLimitCannotBeBypassedBySpoofingLeftmostXFF(t *testing.T) {
	gin.SetMode(gin.TestMode)

	prevLimits := opsWSLimits
	opsWSLimits = opsWSRuntimeLimits{MaxConns: 100, MaxConnsPerIP: 1}
	t.Cleanup(func() {
		opsWSLimits = prevLimits
		qpsWSCache.Stop()
		cancelQPSWSIdleStop()
	})

	const realClientIP = "203.0.113.77"
	require.True(t, tryAcquireOpsWSIPSlot(realClientIP, opsWSLimits.MaxConnsPerIP))
	t.Cleanup(func() {
		releaseOpsWSIPSlot(realClientIP)
	})

	cfg := &config.Config{
		Ops: config.OpsConfig{Enabled: true},
		Server: config.ServerConfig{
			TrustedProxies:           []string{"127.0.0.1"},
			TrustedProxiesConfigured: true,
		},
	}
	opsSvc := service.NewOpsService(nil, nil, cfg, nil, nil, nil, nil, nil, nil, nil, nil)
	h := &OpsHandler{opsService: opsSvc}

	r := gin.New()
	require.NoError(t, r.SetTrustedProxies(cfg.Server.TrustedProxies))
	r.Use(servermiddleware.SessionBindingContext(cfg))
	r.GET("/api/v1/admin/ops/ws/qps", h.QPSWSHandler)

	// Rotating the client-supplied left-most X-Forwarded-For values must still hit
	// the per-IP limit for realClientIP (203.0.113.77).
	for _, spoofedLeft := range []string{"1.1.1.1", "8.8.8.8", "192.0.2.99"} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/ops/ws/qps", nil)
		req.RemoteAddr = "127.0.0.1:50000"
		req.Header.Set("X-Forwarded-For", spoofedLeft+", "+realClientIP)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		require.Equal(t, http.StatusServiceUnavailable, rec.Code, "spoofed left-most IP %s must not bypass per-IP WS limit", spoofedLeft)
		require.Contains(t, rec.Body.String(), "too many connections")
	}
}
