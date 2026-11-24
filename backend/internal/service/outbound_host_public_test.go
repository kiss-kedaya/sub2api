package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/util/urlvalidator"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 审计 A-P1-04：管理员填写的出站主机（账号测试/模型同步、CRS 同步、DeepSeek 自填搜索地址）
// 共用 outboundHostIsPublic 判定。解析失败/超时/空结果一律按「不是公网」处理；
// 解析器全部用 context 注入，用例互不影响，也不依赖真实 DNS。

const publicExampleAddr = "93.184.216.34"

func privateTestAddr() netip.Addr {
	return netip.MustParseAddr(strings.Join([]string{"10", "20", "30", "40"}, "."))
}

func resolverTo(addrs ...netip.Addr) urlvalidator.Resolver {
	return func(context.Context, string) ([]netip.Addr, error) { return addrs, nil }
}

func resolverFailing(context.Context, string) ([]netip.Addr, error) {
	return nil, errors.New("dns timeout")
}

func TestOutboundHostIsPublic(t *testing.T) {
	t.Parallel()
	public := netip.MustParseAddr(publicExampleAddr)
	private := privateTestAddr()

	cases := []struct {
		name     string
		host     string
		resolver urlvalidator.Resolver
		want     bool
	}{
		{"public literal needs no resolution", publicExampleAddr, resolverFailing, true},
		{"private literal", private.String(), resolverTo(public), false},
		{"cgnat literal", "100.100.100.200", resolverTo(public), false},
		{"host resolving to public", "relay.partner.test", resolverTo(public), true},
		{"host resolving to public and private", "relay.partner.test", resolverTo(public, private), false},
		{"dns failure", "relay.partner.test", resolverFailing, false},
		{"empty resolution", "relay.partner.test", resolverTo(), false},
		{"localhost", "localhost", resolverTo(public), false},
		{"known metadata name is blocked before resolution", "metadata.google.internal", resolverTo(public), false},
		{"short metadata name", "instance-data", resolverTo(public), false},
		{"empty host", "", resolverTo(public), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := urlvalidator.WithResolver(context.Background(), tc.resolver)
			require.Equal(t, tc.want, outboundHostIsPublic(ctx, tc.host))
		})
	}
}

func crsGateConfig(crsHosts ...string) *config.Config {
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	// 与默认值一致：CRS 入口从不读 allow_private_hosts，只认 crs_hosts 显式放行。
	cfg.Security.URLAllowlist.AllowPrivateHosts = true
	cfg.Security.URLAllowlist.CRSHosts = crsHosts
	return cfg
}

// CRS 同步：base_url 由管理员手填。解析失败 / 解析到私网在登录请求发出前就被拒，
// 文案固定、不带解析细节；显式写进 crs_hosts 的主机不受影响（不会去解析）。
func TestCRSSyncRejectsHostsThatAreNotPublic(t *testing.T) {
	t.Parallel()
	public := netip.MustParseAddr(publicExampleAddr)
	private := privateTestAddr()

	for name, resolver := range map[string]urlvalidator.Resolver{
		"dns failure":         resolverFailing,
		"empty resolution":    resolverTo(),
		"resolves to private": resolverTo(public, private),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			svc := NewCRSSyncService(nil, nil, nil, nil, nil, crsGateConfig())
			ctx := urlvalidator.WithResolver(context.Background(), resolver)

			_, err := svc.SyncFromCRS(ctx, SyncFromCRSInput{BaseURL: "http://crs.partner.test", Username: "admin", Password: "password"})
			require.ErrorContains(t, err, "crs base_url host is not allowed")
			for _, leaked := range []string{private.String(), "dns", "timeout"} {
				require.NotContains(t, err.Error(), leaked)
			}
		})
	}

	t.Run("explicit crs_hosts entry is not resolved or blocked", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{}`))
		}))
		t.Cleanup(server.Close)
		parsed, err := url.Parse(server.URL)
		require.NoError(t, err)

		svc := NewCRSSyncService(nil, nil, nil, nil, nil, crsGateConfig("localhost"))
		// 注入的解析器一律失败：只要闸门去解析，就会被拒。显式放行的主机必须直接通过闸门，
		// 之后才死在「登录响应不合法」上。
		ctx := urlvalidator.WithResolver(context.Background(), resolverFailing)
		_, err = svc.SyncFromCRS(ctx, SyncFromCRSInput{BaseURL: "http://localhost:" + parsed.Port(), Username: "admin", Password: "password"})
		require.Error(t, err)
		require.NotContains(t, err.Error(), "is not allowed")
	})
}

// DeepSeek 搜索/抓取：分组自填搜索地址时，解析失败 / 解析到私网同样在发请求前被拒，
// 上游零调用、不写任何响应；公网地址照常放行。
func TestForwardDeepSeekToolsRejectsSearchProviderThatIsNotPublic(t *testing.T) {
	restrictAccountTestTargets(t)
	gin.SetMode(gin.TestMode)
	public := netip.MustParseAddr(publicExampleAddr)
	private := privateTestAddr()

	cases := []struct {
		name     string
		base     string
		resolver urlvalidator.Resolver
		allowed  bool
	}{
		{"dns failure", "https://search.partner.test", resolverFailing, false},
		{"empty resolution", "https://search.partner.test", resolverTo(), false},
		{"resolves to private", "https://search.partner.test", resolverTo(public, private), false},
		{"private literal", "https://" + private.String(), resolverTo(public), false},
		{"resolves to public", "https://search.partner.test", resolverTo(public), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			body := []byte(`{"query":"probe"}`)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/tools/search", bytes.NewReader(body))

			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"results":[{"title":"t"}]}`)),
			}}
			svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
			account := &Account{ID: 8, Platform: PlatformDeepseek, Type: AccountTypeAPIKey,
				Credentials: map[string]any{"api_key": "model-account-key", "base_url": "https://api.deepseek.com"}}
			groupKey := "search-only-key"
			base := tc.base
			group := &Group{ID: 36, SearchProviderBaseURL: &base, SearchProviderAPIKey: &groupKey}
			ctx := urlvalidator.WithResolver(context.Background(), tc.resolver)

			_, err := svc.ForwardDeepSeekTools(ctx, c, account, group, DeepSeekToolsPathSearch, "", body)
			if tc.allowed {
				require.NoError(t, err)
				require.Len(t, upstream.requests, 1)
				require.Equal(t, tc.base+"/v1/tools/search", upstream.lastReq.URL.String())
				return
			}
			require.ErrorIs(t, err, errTestTargetNotAllowed)
			require.Empty(t, upstream.requests, "a rejected search provider must not receive any request")
			require.Zero(t, recorder.Body.Len(), "nothing may be written to the client for a rejected target")
		})
	}
}

// 模型同步/账号测试的私网放行判定：只有「allowlist 开启且 allow_private_hosts=true」
// 才算管理员显式放行（测试开关除外）。allowlist 关闭时 allow_private_hosts 不参与。
func TestAccountTestPrivateTargetsAllowedTruthTable(t *testing.T) {
	restrictAccountTestTargets(t)

	cases := []struct {
		name string
		cfg  *config.Config
		want bool
	}{
		{"nil config", nil, false},
		{"allowlist off, allow_private_hosts at its default (true)", modelSyncConfig(config.URLAllowlistConfig{Enabled: false, AllowPrivateHosts: true}), false},
		{"allowlist off, allow_private_hosts false", modelSyncConfig(config.URLAllowlistConfig{Enabled: false}), false},
		{"allowlist on, allow_private_hosts false", modelSyncConfig(config.URLAllowlistConfig{Enabled: true}), false},
		{"allowlist on, allow_private_hosts true", modelSyncConfig(config.URLAllowlistConfig{Enabled: true, AllowPrivateHosts: true}), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := &AccountTestService{cfg: tc.cfg}
			require.Equal(t, tc.want, svc.accountTestPrivateTargetsAllowed())
		})
	}

	SetAccountTestAllowPrivateTargetsForTest(true)
	require.True(t, (&AccountTestService{}).accountTestPrivateTargetsAllowed(), "the test switch always allows")
}
