package repository

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/util/urlvalidator"
	"github.com/stretchr/testify/require"
)

// 回归（审计 A-P1-04）：WithHTTPUpstreamPublicHostsOnly 之前只在发请求前解析一次、
// 再对重定向逐跳解析，Transport 建连时的二次解析不受约束（DNS rebinding 可绕过）。
// 现在直连时拨号期也只能连公网；这里用真实 Transport / Client 锁定该行为。

// directUpstreamClient 构造与生产直连路径同构的客户端（不经代理、不经 validateRequestHost），
// 用来证明「即便请求前校验已放行」，拨号这一步自己也会拒绝非公网地址。
func directUpstreamClient(t *testing.T) *http.Client {
	t.Helper()
	transport, err := buildUpstreamTransport(poolSettings{}, nil, upstreamProtocolModeDefault)
	require.NoError(t, err)
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport, Timeout: 5 * time.Second}
}

func countingServer(t *testing.T) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var hits atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	return server, &hits
}

func TestUpstreamTransportPublicHostsOnlyRejectsPrivateAtDialTime(t *testing.T) {
	server, hits := countingServer(t)
	client := directUpstreamClient(t)

	// 未标记：回环照常可达（既有行为不变，也是测试里能连上本机服务的前提）。
	// Close=true 让这条连接用完即关：拨号期校验只作用于新建连接，复用池里已有连接不经过拨号。
	plain, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, nil)
	require.NoError(t, err)
	plain.Close = true
	resp, err := client.Do(plain)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, int64(1), hits.Load())

	// 已标记：同一个地址在拨号期被拒，服务端没有收到任何新请求。
	guarded, err := http.NewRequestWithContext(service.WithHTTPUpstreamPublicHostsOnly(t.Context()), http.MethodGet, server.URL, nil)
	require.NoError(t, err)
	resp, err = client.Do(guarded)
	require.Nil(t, resp)
	require.ErrorIs(t, err, urlvalidator.ErrOutboundTargetNotAllowed)
	require.Equal(t, int64(1), hits.Load(), "marked request must not reach the private listener")
}

// DNS rebinding 形态：请求前校验时域名解析为公网，拨号时同一个域名解析为回环。
func TestUpstreamTransportPublicHostsOnlyBlocksDNSRebindingAtDialTime(t *testing.T) {
	server, hits := countingServer(t)
	client := directUpstreamClient(t)
	serverURL, err := url.Parse(server.URL)
	require.NoError(t, err)

	var lookups atomic.Int32
	loopback := netip.AddrFrom4([4]byte{127, 0, 0, 1})
	public := netip.MustParseAddr("93.184.216.34")
	resolver := func(context.Context, string) ([]netip.Addr, error) {
		if lookups.Add(1) == 1 {
			return []netip.Addr{public}, nil // 校验时：公网
		}
		return []netip.Addr{loopback}, nil // 拨号时：回环
	}
	ctx := urlvalidator.WithResolver(service.WithHTTPUpstreamPublicHostsOnly(t.Context()), resolver)

	require.NoError(t, urlvalidator.ValidateOutboundHost(ctx, "rebind.partner.test", false), "validation-time lookup sees a public address")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://rebind.partner.test:"+serverURL.Port()+"/v1/models", nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.Nil(t, resp)
	require.ErrorIs(t, err, urlvalidator.ErrOutboundTargetNotAllowed)
	require.Equal(t, int32(2), lookups.Load(), "dial must resolve again and validate the address it is about to connect to")
	require.Zero(t, hits.Load())
}

// 拨号期解析失败同样拒绝，而且拒绝文案固定、不带解析细节。
func TestUpstreamTransportPublicHostsOnlyRejectsDialTimeDNSFailure(t *testing.T) {
	client := directUpstreamClient(t)
	resolver := func(context.Context, string) ([]netip.Addr, error) {
		return nil, &net.DNSError{Err: "server misbehaving", Name: "relay.partner.test"}
	}
	ctx := urlvalidator.WithResolver(service.WithHTTPUpstreamPublicHostsOnly(t.Context()), resolver)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://relay.partner.test:8080/v1/models", nil)
	require.NoError(t, err)
	_, err = client.Do(req)
	require.ErrorIs(t, err, urlvalidator.ErrOutboundTargetNotAllowed)
	require.NotContains(t, err.Error(), "misbehaving")
}

// TLS 指纹直连路径使用另一套拨号器，同样要在拨号期拦截；未标记时仍然照常建连。
func TestUpstreamTLSFingerprintTransportPublicHostsOnlyRejectsPrivateAtDialTime(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	var accepted atomic.Int64
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			accepted.Add(1)
			_ = conn.Close()
		}
	}()

	transport, err := buildUpstreamTransportWithTLSFingerprint(poolSettings{}, nil, &tlsfingerprint.Profile{Name: "dial-guard-test"})
	require.NoError(t, err)
	require.NotNil(t, transport.DialTLSContext)

	_, err = transport.DialTLSContext(service.WithHTTPUpstreamPublicHostsOnly(t.Context()), "tcp", listener.Addr().String())
	require.ErrorIs(t, err, urlvalidator.ErrOutboundTargetNotAllowed)
	require.Zero(t, accepted.Load(), "marked dial must be refused before any connection is made")

	// 未标记：TCP 建连成功（对端随即断开，TLS 握手失败，错误不是策略拒绝）。
	_, err = transport.DialTLSContext(t.Context(), "tcp", listener.Addr().String())
	require.Error(t, err)
	require.NotErrorIs(t, err, urlvalidator.ErrOutboundTargetNotAllowed)
	require.Eventually(t, func() bool { return accepted.Load() == 1 }, 2*time.Second, 10*time.Millisecond)
}

// 同时挂两个标记时重定向一律不跟随（包括公网跳转），禁用重定向优先于逐跳校验。
func TestHTTPUpstreamModelSyncMarkersNeverFollowRedirects(t *testing.T) {
	upstream, ok := NewHTTPUpstream(nil).(*httpUpstreamService)
	require.True(t, ok)

	ctx := service.WithHTTPUpstreamPublicHostsOnly(service.WithHTTPUpstreamRedirectsDisabled(t.Context()))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://relay.partner.test/v1/models", nil)
	require.NoError(t, err)
	client := upstream.httpClientForUpstreamRequest(&http.Client{}, req)

	for _, hop := range []string{"http://93.184.216.34/v1/models", "http://[::1]:8080/internal"} {
		hopReq, err := http.NewRequestWithContext(ctx, http.MethodGet, hop, nil)
		require.NoError(t, err)
		require.ErrorIs(t, client.CheckRedirect(hopReq, []*http.Request{req}), http.ErrUseLastResponse, "hop=%s", hop)
	}
}

// 「公网主机 302 到回环」端到端：用 httptest 代理充当公网字面量 IP 的主机（请求前校验放行，
// 拨号目标是代理本身），它回 302 指向回环地址。
//   - 只有仅公网标记：逐跳校验拒绝跳转，回环目标没收到请求；
//   - 模型同步用的两个标记：直接返回 302 本身，不跟随；
//   - 不挂标记（对照）：跳转被跟随，回环目标收到请求，这就是改动前的漏洞形态。
func TestHTTPUpstreamPublicHostRedirectToLoopbackIsNotFollowed(t *testing.T) {
	const publicHost = "93.184.216.34"
	var internalHits atomic.Int64
	var loopbackTarget string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Hostname() == publicHost {
			http.Redirect(w, r, loopbackTarget, http.StatusFound)
			return
		}
		internalHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(proxy.Close)
	loopbackTarget = "http://" + net.JoinHostPort(netip.AddrFrom4([4]byte{127, 0, 0, 1}).String(), "9") + "/internal"

	upstream := NewHTTPUpstream(nil)
	do := func(ctx context.Context) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+publicHost+"/v1/models", nil)
		require.NoError(t, err)
		return upstream.Do(req, proxy.URL, 1, 1)
	}

	resp, err := do(service.WithHTTPUpstreamPublicHostsOnly(t.Context()))
	require.Error(t, err, "redirect hop to loopback must be rejected")
	require.Nil(t, resp)
	require.Zero(t, internalHits.Load())

	resp, err = do(service.WithHTTPUpstreamPublicHostsOnly(service.WithHTTPUpstreamRedirectsDisabled(t.Context())))
	require.NoError(t, err)
	require.Equal(t, http.StatusFound, resp.StatusCode)
	require.NoError(t, resp.Body.Close())
	require.Zero(t, internalHits.Load())

	resp, err = do(t.Context())
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, int64(1), internalHits.Load(), "control: without markers the redirect is followed")
}
