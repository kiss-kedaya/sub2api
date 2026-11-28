package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/util/urlvalidator"
	"github.com/stretchr/testify/require"
)

func TestNewUpstreamCaptchaSolverRejectsUnsafeEndpointsAndProxies(t *testing.T) {
	for name, endpoint := range map[string]string{
		"loopback":         "https://127.0.0.1:8443",
		"loopback http":    "http://127.0.0.1:8080",
		"rfc1918":          "https://10.0.0.5",
		"link-local":       "https://169.254.169.254",
		"cgnat metadata":   "https://100.100.100.200",
		"ipv6 loopback":    "https://[::1]:8443",
		"localhost":        "https://localhost/api",
		"google metadata":  "https://metadata.google.internal",
		"insecure http":    "http://api.capsolver.com",
		"file scheme":      "file:///etc/passwd",
		"control chars":    "https://api.capsolver.com\r\nX-Inject: 1",
		"backslash in url": `https://api.capsolver.com\evil`,
		"decimal ip":       "https://2130706433",
		"short ip":         "https://127.1",
		"hex metadata ip":  "https://0xa9fea9fe",
		"octal ip":         "https://0177.0.0.1",
		"query string":     "https://api.capsolver.com?x=1",
		"userinfo":         "https://user:pass@api.capsolver.com",
	} {
		t.Run("endpoint/"+name, func(t *testing.T) {
			_, err := NewUpstreamCaptchaSolver(UpstreamCaptchaConfig{
				Provider: UpstreamCaptchaProviderCapSolver,
				APIKey:   "test-captcha-token",
				Endpoint: endpoint,
			})
			require.Error(t, err)
		})
	}

	for name, proxyURL := range map[string]string{
		"loopback http":    "http://127.0.0.1:8080",
		"loopback socks5":  "socks5://127.0.0.1:1080",
		"rfc1918":          "http://192.168.1.5:3128",
		"link-local":       "http://169.254.169.254:80",
		"cgnat metadata":   "http://100.100.100.200:80",
		"ipv6 loopback":    "http://[::1]:8080",
		"localhost":        "http://localhost:8080",
		"google metadata":  "http://metadata.google.internal:80",
		"file scheme":      "file:///etc/passwd",
		"ftp scheme":       "ftp://proxy.example.com:21",
		"gopher scheme":    "gopher://proxy.example.com:70",
		"invalid port":     "http://proxy.example.com:99999",
		"control chars":    "http://proxy.example.com:8080\n",
		"backslash in url": `http://proxy.example.com\evil:8080`,
		"decimal ip":       "http://2130706433:8080",
		"short ip":         "http://127.1:8080",
		"hex metadata ip":  "http://0xa9fea9fe:80",
		"octal ip":         "http://0177.0.0.1:8080",
		"query string":     "http://proxy.example.com:8080?x=1",
		"non-root path":    "http://proxy.example.com:8080/secret",
	} {
		t.Run("proxy/"+name, func(t *testing.T) {
			_, err := NewUpstreamCaptchaSolver(UpstreamCaptchaConfig{
				Provider: UpstreamCaptchaProviderCapSolver,
				APIKey:   "test-captcha-token",
				ProxyURL: proxyURL,
			})
			require.Error(t, err)
		})
	}
}

func TestUpstreamCaptchaSolverHardensTransportAndBlocksRedirectsAndDNSRebinding(t *testing.T) {
	t.Run("default transport does not inherit proxy env and blocks DNS to private", func(t *testing.T) {
		solver, err := NewUpstreamCaptchaSolver(UpstreamCaptchaConfig{
			Provider: UpstreamCaptchaProviderCapSolver,
			APIKey:   "test-captcha-token",
			Endpoint: "https://rebind.example.test",
		})
		require.NoError(t, err)
		impl, ok := solver.(*upstreamCaptchaTaskSolver)
		require.True(t, ok)
		transport, ok := impl.client.Transport.(*http.Transport)
		require.True(t, ok)
		require.Nil(t, transport.Proxy, "transport must not inherit HTTP_PROXY when ProxyURL is unset")
		require.NotNil(t, transport.DialContext)
		require.NotNil(t, impl.client.CheckRedirect)

		privateCtx := urlvalidator.WithResolver(context.Background(), func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("10.0.0.9")}, nil
		})
		_, err = impl.post(privateCtx, "/createTask", map[string]any{"k": "v"})
		require.ErrorIs(t, err, urlvalidator.ErrOutboundTargetNotAllowed)
	})

	t.Run("blocks proxy hostname resolving to private ip before dialing", func(t *testing.T) {
		solver, err := NewUpstreamCaptchaSolver(UpstreamCaptchaConfig{
			Provider: UpstreamCaptchaProviderCapSolver,
			APIKey:   "test-captcha-token",
			Endpoint: "https://solver.example.test",
			ProxyURL: "http://proxy.example.test:8080",
		})
		require.NoError(t, err)
		impl, ok := solver.(*upstreamCaptchaTaskSolver)
		require.True(t, ok)

		ctx := urlvalidator.WithResolver(context.Background(), func(_ context.Context, host string) ([]netip.Addr, error) {
			if host == "proxy.example.test" {
				return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
			}
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		})
		_, err = impl.post(ctx, "/createTask", map[string]any{"k": "v"})
		require.ErrorIs(t, err, urlvalidator.ErrOutboundTargetNotAllowed)
	})

	t.Run("does not follow 302 redirects and rejects 3xx status", func(t *testing.T) {
		var redirectTargetHits atomic.Int32
		target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			redirectTargetHits.Add(1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"errorId":0,"taskId":"leaked"}`))
		}))
		defer target.Close()

		redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target.URL+"/steal", http.StatusFound)
		}))
		defer redirector.Close()

		solver, err := NewUpstreamCaptchaSolver(UpstreamCaptchaConfig{
			Provider:          UpstreamCaptchaProviderCapSolver,
			APIKey:            "test-captcha-token",
			Endpoint:          redirector.URL,
			AllowPrivate:      true,
			AllowInsecureHTTP: true,
		})
		require.NoError(t, err)
		impl, ok := solver.(*upstreamCaptchaTaskSolver)
		require.True(t, ok)
		_, err = impl.post(context.Background(), "/createTask", map[string]any{"clientKey": "secret"})
		require.ErrorContains(t, err, "HTTP 302")
		require.Zero(t, redirectTargetHits.Load(), "redirect target must never receive captcha POST")
	})

	t.Run("transport errors redact proxy credentials", func(t *testing.T) {
		closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		closedAddr := closed.Listener.Addr().String()
		closed.Close()

		solver, err := NewUpstreamCaptchaSolver(UpstreamCaptchaConfig{
			Provider:          UpstreamCaptchaProviderCapSolver,
			APIKey:            "test-captcha-token",
			Endpoint:          "http://127.0.0.1:9",
			ProxyURL:          "http://proxyuser:super-secret-proxy-pass@" + closedAddr,
			AllowPrivate:      true,
			AllowInsecureHTTP: true,
		})
		require.NoError(t, err)
		impl, ok := solver.(*upstreamCaptchaTaskSolver)
		require.True(t, ok)
		_, err = impl.post(context.Background(), "/createTask", map[string]any{"k": "v"})
		require.Error(t, err)
		require.NotContains(t, err.Error(), "super-secret-proxy-pass")
	})
}

type stubUpstreamChannelRepo struct {
	UpstreamChannelRepository
	channel *UpstreamChannel
}

func (r *stubUpstreamChannelRepo) GetUpstreamChannel(_ context.Context, _ int64) (*UpstreamChannel, error) {
	if r.channel == nil {
		return nil, ErrUpstreamChannelNotFound
	}
	copyCh := *r.channel
	return &copyCh, nil
}

func TestUpstreamChannelServiceValidatesProxyURLOnSaveAndLogin(t *testing.T) {
	svc := NewUpstreamChannelService(nil, outboundTestEncryptor{})

	for name, proxyURL := range map[string]string{
		"loopback":       "http://127.0.0.1:8080",
		"rfc1918":        "http://10.1.2.3:3128",
		"link-local":     "http://169.254.169.254:80",
		"cgnat metadata": "http://100.100.100.200:80",
		"socks5 local":   "socks5://[::1]:1080",
		"bad scheme":     "file:///etc/passwd",
	} {
		t.Run("save/"+name, func(t *testing.T) {
			input := channelInput("https://up.example.com")
			input.ProxyURL = proxyURL
			_, err := svc.applyInput(nil, input, true)
			require.Error(t, err)
		})
	}

	t.Run("save allows public proxy resolved via context", func(t *testing.T) {
		ctx := urlvalidator.WithResolver(context.Background(), func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		})
		input := channelInput("https://up.example.com")
		input.ProxyURL = "http://proxy.example.com:8080"
		ch, err := svc.applyInputWithContext(ctx, nil, input, true)
		require.NoError(t, err)
		require.Equal(t, "http://proxy.example.com:8080", ch.ProxyURL)
	})

	t.Run("login rejects tampered stored private proxy before dialing", func(t *testing.T) {
		repo := &stubUpstreamChannelRepo{
			channel: &UpstreamChannel{
				ID:       1,
				Name:     "relay",
				SiteURL:  "https://up.example.com",
				Username: "ops@example.com",
				Password: upstreamChannelCipherPrefix + "ENC:secret",
				ProxyURL: "http://127.0.0.1:8080",
				Status:   UpstreamChannelStatusActive,
			},
		}
		svcWithRepo := NewUpstreamChannelService(repo, outboundTestEncryptor{})
		_, err := svcWithRepo.Login(context.Background(), 1)
		require.ErrorIs(t, err, urlvalidator.ErrOutboundTargetNotAllowed)
	})
}
