package repository

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// 端到端（审计 A-P1-04）：模型同步（preview 形态：临时账号、凭据全部来自请求体）经真实的
// AccountTestService 与真实的共享出站客户端，而不是手工挂标记。
//
// 「公网主机」用字面公网 IP 表示（请求前校验放行），实际连接走账号代理：这里的 httptest
// 代理按目的主机应答，回环目的地的请求就是「内网侧收到了请求」。

type modelSyncE2EEnv struct {
	svc          *service.AccountTestService
	upstream     service.HTTPUpstream
	proxy        *service.Proxy
	internalHits *atomic.Int64
}

const modelSyncE2EPublicHost = "93.184.216.34"

func newModelSyncE2EEnv(t *testing.T, publicReply func(w http.ResponseWriter, r *http.Request, internalTarget string)) *modelSyncE2EEnv {
	t.Helper()
	// 默认策略：不放行私网。其它用例为了用假域名会临时打开这个开关，这里显式恢复。
	service.SetAccountTestAllowPrivateTargetsForTest(false)

	var internalHits atomic.Int64
	internalTarget := "http://" + net.JoinHostPort(netip.AddrFrom4([4]byte{127, 0, 0, 1}).String(), "9") + "/v1/models"
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodConnect {
			w.WriteHeader(http.StatusBadGateway) // models.dev 等 https 目标：本测试不关心
			return
		}
		if r.URL.Hostname() == modelSyncE2EPublicHost {
			publicReply(w, r, internalTarget)
			return
		}
		internalHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"internal-only-model"}]}`))
	}))
	t.Cleanup(proxyServer.Close)

	parsed, err := url.Parse(proxyServer.URL)
	require.NoError(t, err)
	port, err := strconv.Atoi(parsed.Port())
	require.NoError(t, err)

	// 与生产默认值一致：allowlist 关闭，allow_private_hosts 取默认值 true。
	cfg := &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{
		Enabled:           false,
		AllowPrivateHosts: true,
		AllowInsecureHTTP: true,
	}}}
	upstream := NewHTTPUpstream(cfg)
	return &modelSyncE2EEnv{
		svc:          service.NewAccountTestService(nil, nil, nil, nil, nil, upstream, cfg, nil),
		upstream:     upstream,
		proxy:        &service.Proxy{ID: 1, Protocol: "http", Host: parsed.Hostname(), Port: port},
		internalHits: &internalHits,
	}
}

func (e *modelSyncE2EEnv) previewAccount() *service.Account {
	proxyID := e.proxy.ID
	return &service.Account{
		Platform: service.PlatformOpenAI,
		Type:     service.AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "preview-key",
			"base_url": "http://" + modelSyncE2EPublicHost + "/v1",
		},
		ProxyID: &proxyID,
		Proxy:   e.proxy,
	}
}

// 公网主机 302 指向回环：不跟随，内网侧零请求，回环侧「像模型列表的 JSON」不会回显给调用方。
func TestModelSyncPreviewDoesNotFollowRedirectToLoopback(t *testing.T) {
	env := newModelSyncE2EEnv(t, func(w http.ResponseWriter, r *http.Request, internalTarget string) {
		http.Redirect(w, r, internalTarget, http.StatusFound)
	})

	catalog, err := env.svc.SyncUpstreamModelCatalog(t.Context(), env.previewAccount())
	require.Error(t, err)
	require.Nil(t, catalog)
	var syncErr *service.UpstreamModelSyncError
	require.ErrorAs(t, err, &syncErr)
	require.Equal(t, http.StatusFound, syncErr.StatusCode, "the redirect itself is the answer; it must not be followed")
	require.NotContains(t, syncErr.SafeMessage(), "internal-only-model")
	require.Zero(t, env.internalHits.Load(), "the loopback side must not receive any request")

	// 对照：同一个请求不挂标记（改动前模型同步的形态）会跟随跳转，内网侧收到请求。
	// 没有这条对照，上面的「零请求」可能只是测试环境本身没有走通。
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+modelSyncE2EPublicHost+"/v1/models", nil)
	require.NoError(t, err)
	resp, err := env.upstream.Do(req, env.proxy.URL(), 1, 1)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, int64(1), env.internalHits.Load(), "control: without the guard the redirect is followed")
}

// 正常的公网上游不受影响：同步照常拿到模型列表。
func TestModelSyncPublicUpstreamStillWorksThroughGuard(t *testing.T) {
	env := newModelSyncE2EEnv(t, func(w http.ResponseWriter, _ *http.Request, _ string) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-5"},{"id":"o3"}]}`))
	})

	models, err := env.svc.FetchUpstreamSupportedModels(t.Context(), env.previewAccount())
	require.NoError(t, err)
	require.Equal(t, []string{"gpt-5", "o3"}, models)
	require.Zero(t, env.internalHits.Load())
}
