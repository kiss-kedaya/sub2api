package urlvalidator

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"
)

func withFakeResolver(t *testing.T, ips ...string) {
	t.Helper()
	addrs := make([]netip.Addr, 0, len(ips))
	for _, raw := range ips {
		addrs = append(addrs, netip.MustParseAddr(raw))
	}
	restore := SetResolveOutboundHostForTest(func(context.Context, string) ([]netip.Addr, error) {
		return addrs, nil
	})
	t.Cleanup(restore)
}

func withFailingResolver(t *testing.T) {
	t.Helper()
	restore := SetResolveOutboundHostForTest(func(context.Context, string) ([]netip.Addr, error) {
		return nil, errors.New("dns timeout")
	})
	t.Cleanup(restore)
}

func TestValidateOutboundURLPublicTargetPasses(t *testing.T) {
	t.Parallel()
	withFakeResolver(t, "93.184.216.34")

	got, err := ValidateOutboundURL(context.Background(), " https://api.example.com/v1/ ", OutboundPolicy{})
	require.NoError(t, err)
	require.Equal(t, "https://api.example.com/v1", got)

	got, err = ValidateOutboundURL(context.Background(), "https://8.8.8.8/v1", OutboundPolicy{})
	require.NoError(t, err)
	require.Equal(t, "https://8.8.8.8/v1", got)
}

func TestValidateOutboundURLRejectsPrivateLiterals(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{
		"https://127.0.0.1/",
		"https://10.1.2.3/",
		"https://192.168.1.10:8443/",
		"https://169.254.169.254/latest/meta-data",
		"http://localhost:8080/",
		"https://[::1]/",
	} {
		_, err := ValidateOutboundURL(context.Background(), raw, OutboundPolicy{AllowInsecureHTTP: true})
		require.ErrorIs(t, err, ErrOutboundTargetNotAllowed, raw)
	}
}

func TestValidateOutboundURLRejectsResolvedPrivateAddress(t *testing.T) {
	t.Parallel()
	withFakeResolver(t, "93.184.216.34", "10.0.0.7")

	_, err := ValidateOutboundURL(context.Background(), "https://rebind.example.com/", OutboundPolicy{})
	require.ErrorIs(t, err, ErrOutboundTargetNotAllowed)
}

func TestValidateOutboundURLRejectsDNSFailure(t *testing.T) {
	t.Parallel()
	withFailingResolver(t)

	_, err := ValidateOutboundURL(context.Background(), "https://unresolvable.example.com/", OutboundPolicy{})
	require.ErrorIs(t, err, ErrOutboundTargetNotAllowed)
}

func TestValidateOutboundURLHonoursExplicitPrivateSwitch(t *testing.T) {
	t.Parallel()

	// 显式开关打开后，私网字面量与解析结果都放行，且不触发 DNS。
	got, err := ValidateOutboundURL(context.Background(), "http://10.1.2.3:9000/", OutboundPolicy{AllowPrivate: true, AllowInsecureHTTP: true})
	require.NoError(t, err)
	require.Equal(t, "http://10.1.2.3:9000", got)

	got, err = ValidateOutboundURL(context.Background(), "https://internal.guard.local/", OutboundPolicy{AllowPrivate: true})
	require.NoError(t, err)
	require.Equal(t, "https://internal.guard.local", got)
}

func TestValidateOutboundHostRejectsSpecialUseLiterals(t *testing.T) {
	t.Parallel()
	for _, host := range []string{
		"127.0.0.1",
		"10.1.2.3",
		"100.100.100.200", // CGNAT / 云元数据
		"198.18.0.1",      // benchmarking
		"169.254.169.254", // link-local metadata
		"localhost",
		"metadata.google.internal", // 解析失败即拒绝
	} {
		require.ErrorIs(t, ValidateOutboundHost(context.Background(), host, false), ErrOutboundTargetNotAllowed, host)
	}
	require.NoError(t, ValidateOutboundHost(context.Background(), "8.8.8.8", false))
	require.NoError(t, ValidateOutboundHost(context.Background(), "100.100.100.200", true))
}

func TestValidateOutboundURLRequiresHTTPSByDefault(t *testing.T) {
	t.Parallel()
	withFakeResolver(t, "93.184.216.34")

	_, err := ValidateOutboundURL(context.Background(), "http://api.example.com/", OutboundPolicy{})
	require.ErrorIs(t, err, ErrOutboundTargetNotAllowed)
}

func TestRedirectGuardRejectsPrivateRedirect(t *testing.T) {
	t.Parallel()
	guard := NewRedirectGuard(OutboundPolicy{})

	req := httptest.NewRequest(http.MethodGet, "https://127.0.0.1/internal", nil)
	require.ErrorIs(t, guard(req, []*http.Request{{}}), ErrOutboundTargetNotAllowed)

	withFakeResolver(t, "10.20.30.40")
	req = httptest.NewRequest(http.MethodGet, "https://rebind.example.com/internal", nil)
	require.ErrorIs(t, guard(req, []*http.Request{{}}), ErrOutboundTargetNotAllowed)

	withFakeResolver(t, "93.184.216.34")
	req = httptest.NewRequest(http.MethodGet, "https://api.example.com/next", nil)
	require.NoError(t, guard(req, []*http.Request{{}}))
}

func TestRedirectGuardHonoursPrivateSwitch(t *testing.T) {
	t.Parallel()
	guard := NewRedirectGuard(OutboundPolicy{AllowPrivate: true})
	req := httptest.NewRequest(http.MethodGet, "https://127.0.0.1/internal", nil)
	require.NoError(t, guard(req, []*http.Request{{}}))
}

func TestSafeHTTPClientRejectsPrivateDial(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	allowed := NewSafeHTTPClient(OutboundPolicy{AllowPrivate: true}, HTTPClientOptions{})
	resp, err := allowed.Get(server.URL)
	require.NoError(t, err)
	_ = resp.Body.Close()

	blocked := NewSafeHTTPClient(OutboundPolicy{}, HTTPClientOptions{})
	_, err = blocked.Get(server.URL)
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrOutboundTargetNotAllowed) || isConnRefused(err), "unexpected error: %v", err)
}

func TestSafeHTTPClientBlocksRedirectToPrivate(t *testing.T) {
	t.Parallel()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(target.Close)
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	t.Cleanup(redirector.Close)

	// 起点在测试环境里也是 127.0.0.1：用 AllowPrivate 的客户端请求，
	// 但重定向守卫用默认策略（拒绝私网），以此锁定「重定向到私网被拒」。
	client := NewSafeHTTPClient(OutboundPolicy{AllowPrivate: true}, HTTPClientOptions{})
	client.CheckRedirect = NewRedirectGuard(OutboundPolicy{})
	_, err := client.Get(redirector.URL)
	require.Error(t, err)
	require.True(t, IsOutboundTargetRejected(err), "unexpected error: %v", err)
}

func isConnRefused(err error) bool {
	var opErr *net.OpError
	return errors.As(err, &opErr)
}
