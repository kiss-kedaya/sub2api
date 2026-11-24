package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// 模型同步出站防线回归（审计 A-P1-04）。
//
// preview（base_url / api_key 来自请求体）、已保存账号的 sync-upstream、白名单自动同步
// 都经过 fetchUpstreamModelList；这里锁定三件事：
//   - 目标私网判定解析失败按拒绝处理（不再放行），字面公网 IP 不需要解析；
//   - 模型列表请求默认带「仅公网 + 禁重定向」标记，共享出站客户端据此做请求前/拨号期/重定向校验；
//   - 管理员显式放行私网（allowlist 开启且 allow_private_hosts=true）时不挂标记，行为不变。

// restrictAccountTestTargets 关闭 TestMain 默认打开的「放行私网」开关，让用例走真实策略。
func restrictAccountTestTargets(t *testing.T) {
	t.Helper()
	SetAccountTestAllowPrivateTargetsForTest(false)
	t.Cleanup(func() { SetAccountTestAllowPrivateTargetsForTest(true) })
}

func modelSyncRecorder() *httpUpstreamRecorder {
	return &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"data":[{"id":"gpt-5"}]}`)),
	}}
}

// modelSyncPreviewAccount 与 SyncUpstreamModelsPreview 构造的临时账号同形：
// 无 ID、无代理，凭据全部来自请求体。
func modelSyncPreviewAccount(baseURL string) *Account {
	return &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "preview-key",
			"base_url": baseURL,
		},
	}
}

func modelSyncConfig(allowlist config.URLAllowlistConfig) *config.Config {
	return &config.Config{Security: config.SecurityConfig{URLAllowlist: allowlist}}
}

func TestModelSyncRequestCarriesOutboundGuardByDefault(t *testing.T) {
	restrictAccountTestTargets(t)
	stubOutboundResolver(t, "93.184.216.34")

	for name, allowlist := range map[string]config.URLAllowlistConfig{
		// 默认配置：allowlist 关闭（allow_private_hosts 的默认值 true 在关闭状态下不参与）。
		"default config":             {Enabled: false, AllowPrivateHosts: true, AllowInsecureHTTP: true},
		"allowlist off":              {Enabled: false},
		"allowlist on, private deny": {Enabled: true, UpstreamHosts: []string{"relay.partner.test"}, AllowPrivateHosts: false},
	} {
		t.Run(name, func(t *testing.T) {
			rec := modelSyncRecorder()
			svc := &AccountTestService{httpUpstream: rec, cfg: modelSyncConfig(allowlist)}

			// preview 走的就是 SyncUpstreamModelCatalog(tempAccount)。
			_, err := svc.SyncUpstreamModelCatalog(context.Background(), modelSyncPreviewAccount("https://relay.partner.test/v1"))
			require.NoError(t, err)
			require.NotEmpty(t, rec.requests)

			listReq := rec.requests[0]
			require.Equal(t, "https://relay.partner.test/v1/models", listReq.URL.String())
			require.True(t, HTTPUpstreamPublicHostsOnly(listReq.Context()), "model list request must be public-hosts-only")
			require.True(t, HTTPUpstreamRedirectsDisabled(listReq.Context()), "model list request must not follow redirects")
		})
	}
}

func TestModelSyncSavedAccountRequestCarriesOutboundGuard(t *testing.T) {
	restrictAccountTestTargets(t)
	stubOutboundResolver(t, "93.184.216.34")

	rec := modelSyncRecorder()
	svc := &AccountTestService{httpUpstream: rec, cfg: modelSyncConfig(config.URLAllowlistConfig{})}
	account := modelSyncPreviewAccount("https://relay.partner.test/v1")
	account.ID = 42 // 已保存账号的 sync-upstream / 自动同步走同一个出站函数

	_, err := svc.FetchUpstreamSupportedModels(context.Background(), account)
	require.NoError(t, err)
	require.True(t, HTTPUpstreamPublicHostsOnly(rec.lastReq.Context()))
	require.True(t, HTTPUpstreamRedirectsDisabled(rec.lastReq.Context()))
}

// 管理员显式放行私网：请求照常发出，且不带标记（共享客户端对它的行为与改动前一致）。
func TestModelSyncKeepsPrivateUpstreamWhenAdminAllowsPrivateHosts(t *testing.T) {
	restrictAccountTestTargets(t)

	privateHost := strings.Join([]string{"10", "20", "30", "40"}, ".")
	for name, base := range map[string]string{
		"private host name":  "https://relay.internal.test/v1",
		"private IP literal": "https://" + privateHost + "/v1",
	} {
		t.Run(name, func(t *testing.T) {
			rec := modelSyncRecorder()
			svc := &AccountTestService{httpUpstream: rec, cfg: modelSyncConfig(config.URLAllowlistConfig{
				Enabled:           true,
				AllowPrivateHosts: true,
				UpstreamHosts:     []string{"relay.internal.test", privateHost},
			})}

			models, err := svc.FetchUpstreamSupportedModels(context.Background(), modelSyncPreviewAccount(base))
			require.NoError(t, err)
			require.Equal(t, []string{"gpt-5"}, models)
			require.Equal(t, base+"/models", rec.lastReq.URL.String())
			require.False(t, HTTPUpstreamPublicHostsOnly(rec.lastReq.Context()), "explicit private allowance must stay untouched")
			require.False(t, HTTPUpstreamRedirectsDisabled(rec.lastReq.Context()), "explicit private allowance must stay untouched")
		})
	}
}

// 默认配置下私网目标在发请求之前就被拒绝，且错误里没有主机/解析结果；
// 一个请求都不发，自然没有「状态码 + 响应片段」可回显。
func TestModelSyncPreviewRejectsPrivateTargetsWithoutEcho(t *testing.T) {
	restrictAccountTestTargets(t)
	privateIP := strings.Join([]string{"10", "20", "30", "40"}, ".")

	cases := map[string]func(t *testing.T) string{
		"private literal": func(*testing.T) string { return "https://" + privateIP + "/v1" },
		"resolves to private": func(t *testing.T) string {
			stubOutboundResolver(t, "93.184.216.34", privateIP)
			return "https://relay.partner.test/v1"
		},
		"dns failure": func(t *testing.T) string {
			stubOutboundResolverError(t)
			return "https://relay.partner.test/v1"
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			base := setup(t)
			rec := modelSyncRecorder()
			svc := &AccountTestService{httpUpstream: rec, cfg: modelSyncConfig(config.URLAllowlistConfig{AllowInsecureHTTP: true})}

			_, err := svc.SyncUpstreamModelCatalog(context.Background(), modelSyncPreviewAccount(base))
			require.Error(t, err)
			require.Nil(t, rec.lastReq, "a rejected target must not receive any request")

			var syncErr *UpstreamModelSyncError
			require.True(t, errors.As(err, &syncErr))
			require.Equal(t, UpstreamModelSyncErrorConfiguration, syncErr.Kind)
			require.ErrorIs(t, err, errTestTargetNotAllowed)
			for _, leaked := range []string{privateIP, "relay.partner.test", "dns", "timeout"} {
				require.NotContains(t, syncErr.SafeMessage(), leaked)
			}
		})
	}
}

// 解析失败不再放行；字面公网 IP 不需要解析，解析器坏了也照常通过。
func TestAccountTestValidateUpstreamBaseURLFailsClosedOnDNSError(t *testing.T) {
	restrictAccountTestTargets(t)
	svc := &AccountTestService{cfg: modelSyncConfig(config.URLAllowlistConfig{})}

	t.Run("dns error is rejected", func(t *testing.T) {
		stubOutboundResolverError(t)
		_, err := svc.validateUpstreamBaseURL("https://relay.partner.test/v1")
		require.ErrorIs(t, err, errTestTargetNotAllowed)
		require.NotContains(t, err.Error(), "partner")
		require.NotContains(t, err.Error(), "timeout")
	})

	t.Run("empty resolution is rejected", func(t *testing.T) {
		stubOutboundResolver(t)
		_, err := svc.validateUpstreamBaseURL("https://relay.partner.test/v1")
		require.ErrorIs(t, err, errTestTargetNotAllowed)
	})

	t.Run("public resolution passes", func(t *testing.T) {
		stubOutboundResolver(t, "93.184.216.34")
		got, err := svc.validateUpstreamBaseURL("https://relay.partner.test/v1/")
		require.NoError(t, err)
		require.Equal(t, "https://relay.partner.test/v1", got)
	})

	t.Run("public IP literal needs no resolution", func(t *testing.T) {
		stubOutboundResolverError(t)
		got, err := svc.validateUpstreamBaseURL("https://93.184.216.34/v1")
		require.NoError(t, err)
		require.Equal(t, "https://93.184.216.34/v1", got)
	})
}
