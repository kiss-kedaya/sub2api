//go:build unit

package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// 回归：解析后 IP 校验（防 DNS Rebinding）曾经写成
//
//	ValidateResolvedIP: cfg.Security.URLAllowlist.Enabled
//
// 这把两件独立的事耦合在一起——「关掉 allowlist」被静默解释成
// 「连 rebinding 防护也一起关掉」。于是
//
//	ALLOWLIST_ENABLED=false + ALLOW_PRIVATE_HOSTS=false
//
// 这种配置**完全没有解析 IP 校验**，可以直连私网/环回地址。
//
// 现在只由 AllowPrivateHosts 决定：它才是「是否放行私网地址」的那个开关。
// 本测试锁住这个行为——如果有人把它改回绑定 Enabled，这里会失败。
func TestCRSSyncResolvedIPValidationIsNotTiedToAllowlist(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(server.Close)

	t.Run("allowlist off plus private hosts disallowed still blocks loopback", func(t *testing.T) {
		t.Parallel()

		cfg := &config.Config{}
		cfg.Security.URLAllowlist.Enabled = false // allowlist 关着
		cfg.Security.URLAllowlist.AllowInsecureHTTP = true
		cfg.Security.URLAllowlist.AllowPrivateHosts = false // 但私网不允许

		svc := NewCRSSyncService(newCRSLongContextAccountRepo(), nil, nil, nil, nil, cfg)
		_, err := svc.SyncFromCRS(context.Background(), SyncFromCRSInput{
			BaseURL:  server.URL, // 127.0.0.1 —— 环回
			Username: "admin",
			Password: "password",
		})

		require.Error(t, err, "closing the allowlist must not silently disable DNS-rebinding protection")
		require.Contains(t, err.Error(), "is not allowed",
			"the loopback destination must be rejected by the resolved-IP check")
	})

	t.Run("private hosts allowed reaches the server", func(t *testing.T) {
		t.Parallel()

		cfg := &config.Config{}
		cfg.Security.URLAllowlist.Enabled = false
		cfg.Security.URLAllowlist.AllowInsecureHTTP = true
		cfg.Security.URLAllowlist.AllowPrivateHosts = true

		svc := NewCRSSyncService(newCRSLongContextAccountRepo(), nil, nil, nil, nil, cfg)
		_, err := svc.SyncFromCRS(context.Background(), SyncFromCRSInput{
			BaseURL:  server.URL,
			Username: "admin",
			Password: "password",
		})

		// 这里仍会失败（测试服务器返回的不是合法登录响应），但**不能**是 IP 被拦。
		if err != nil {
			require.NotContains(t, err.Error(), "is not allowed",
				"AllowPrivateHosts=true must let the resolved-IP check pass")
		}
	})
}
