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

// fakeResolverContext 把一个固定 DNS 结果挂到 ctx 上，供出站校验在本用例内
// 使用。返回的 ctx 只在本用例里传递，不读写任何包级变量，所以用例可以
// 安全地 t.Parallel()：这是取代「SetResolveOutboundHostForTest + 全局还原」
// 的注入方式（后者并行时会互相覆盖，曾导致 CI 间歇失败）。
func fakeResolverContext(t *testing.T, ips ...string) context.Context {
	t.Helper()
	addrs := make([]netip.Addr, 0, len(ips))
	for _, raw := range ips {
		addrs = append(addrs, netip.MustParseAddr(raw))
	}
	return WithResolver(context.Background(), func(context.Context, string) ([]netip.Addr, error) {
		return addrs, nil
	})
}

// failingResolverContext 同上，但让解析失败（模拟 DNS 超时/不存在）。
func failingResolverContext(t *testing.T) context.Context {
	t.Helper()
	return WithResolver(context.Background(), func(context.Context, string) ([]netip.Addr, error) {
		return nil, errors.New("dns timeout")
	})
}

func TestValidateOutboundURLPublicTargetPasses(t *testing.T) {
	t.Parallel()
	ctx := fakeResolverContext(t, "93.184.216.34")

	got, err := ValidateOutboundURL(ctx, " https://api.example.com/v1/ ", OutboundPolicy{})
	require.NoError(t, err)
	require.Equal(t, "https://api.example.com/v1", got)

	got, err = ValidateOutboundURL(ctx, "https://8.8.8.8/v1", OutboundPolicy{})
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
	ctx := fakeResolverContext(t, "93.184.216.34", "10.0.0.7")

	_, err := ValidateOutboundURL(ctx, "https://rebind.example.com/", OutboundPolicy{})
	require.ErrorIs(t, err, ErrOutboundTargetNotAllowed)
}

func TestValidateOutboundURLRejectsDNSFailure(t *testing.T) {
	t.Parallel()
	ctx := failingResolverContext(t)

	_, err := ValidateOutboundURL(ctx, "https://unresolvable.example.com/", OutboundPolicy{})
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
	} {
		require.ErrorIs(t, ValidateOutboundHost(context.Background(), host, false), ErrOutboundTargetNotAllowed, host)
	}
	require.NoError(t, ValidateOutboundHost(context.Background(), "8.8.8.8", false))
	require.NoError(t, ValidateOutboundHost(context.Background(), "100.100.100.200", true))

	// 域名解析失败/超时一律拒绝：用注入的失败解析器固定结果，不依赖真实 DNS，
	// 也不改全局解析器（本用例与其它用例都可并行）。
	failingCtx := failingResolverContext(t)
	require.ErrorIs(t, ValidateOutboundHost(failingCtx, "metadata.google.internal", false), ErrOutboundTargetNotAllowed)
	require.ErrorIs(t, ValidateOutboundHost(failingCtx, "unresolvable.example.com", false), ErrOutboundTargetNotAllowed)
}

func TestValidateOutboundURLRequiresHTTPSByDefault(t *testing.T) {
	t.Parallel()
	ctx := fakeResolverContext(t, "93.184.216.34")

	_, err := ValidateOutboundURL(ctx, "http://api.example.com/", OutboundPolicy{})
	require.ErrorIs(t, err, ErrOutboundTargetNotAllowed)
}

func TestRedirectGuardRejectsPrivateRedirect(t *testing.T) {
	t.Parallel()
	guard := NewRedirectGuard(OutboundPolicy{})

	req := httptest.NewRequest(http.MethodGet, "https://127.0.0.1/internal", nil)
	require.ErrorIs(t, guard(req, []*http.Request{{}}), ErrOutboundTargetNotAllowed)

	req = httptest.NewRequest(http.MethodGet, "https://rebind.example.com/internal", nil)
	req = req.WithContext(fakeResolverContext(t, "10.20.30.40"))
	require.ErrorIs(t, guard(req, []*http.Request{{}}), ErrOutboundTargetNotAllowed)

	req = httptest.NewRequest(http.MethodGet, "https://api.example.com/next", nil)
	req = req.WithContext(fakeResolverContext(t, "93.184.216.34"))
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

	// 注入解析器锁死 DNS 结果，避免依赖真实解析：默认策略下裸 IP 拨号前必须被拒。
	blocked := NewSafeHTTPClient(OutboundPolicy{}, HTTPClientOptions{})
	req, err := http.NewRequestWithContext(fakeResolverContext(t, "127.0.0.1"), http.MethodGet, server.URL, nil)
	require.NoError(t, err)
	_, err = blocked.Do(req)
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
	// 起始请求本身也注入解析器：重定向守卫跟随请求 ctx 判断，结果只取决于用例内
	// 的假解析器，不受并行用例或真实 DNS 影响。
	req, err := http.NewRequestWithContext(fakeResolverContext(t, "93.184.216.34"), http.MethodGet, redirector.URL, nil)
	require.NoError(t, err)
	_, err = client.Do(req)
	require.Error(t, err)
	require.True(t, IsOutboundTargetRejected(err), "unexpected error: %v", err)
}

func isConnRefused(err error) bool {
	var opErr *net.OpError
	return errors.As(err, &opErr)
}
