package securityaudit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/util/urlvalidator"
	"github.com/stretchr/testify/require"
)

// 审计节点探测（POST /admin/prompt-audit/endpoints/probe）此前注释里明确
// “允许私网”。现在默认必须拒绝私网，只有显式 allow_private_endpoints 才放行。

func TestPromptProbeRejectsPrivateEndpointByDefault(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"` + DefaultGuardModel + `"}]}`))
	}))
	defer server.Close()

	result := newProbeTestService().Probe(context.Background(), ProbeRequest{
		Endpoint: probeEndpoint(server.URL, "temporary-token"),
	})
	require.False(t, result.OK)
	require.Equal(t, "endpoint_unsafe", result.ErrorCode)
	require.NotContains(t, result.Message, "127.0.0.1")
	require.NotContains(t, result.Message, server.URL)
}

func TestPromptProbeRejectsPrivateLiteralEvenWithWhitespace(t *testing.T) {
	for _, baseURL := range []string{
		"http://127.0.0.1:8080",
		"http://10.0.0.5:8080",
		"http://169.254.169.254",
		"http://[::1]:8080",
	} {
		result := newProbeTestService().Probe(context.Background(), ProbeRequest{
			Endpoint: probeEndpoint(baseURL, "temporary-token"),
		})
		require.False(t, result.OK, baseURL)
		require.Equal(t, "endpoint_unsafe", result.ErrorCode, baseURL)
	}
}

func TestPromptProbeCannotElevateAllowPrivateBeyondSavedConfig(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"` + DefaultGuardModel + `"}]}`))
	}))
	defer server.Close()
	allowPrivate := true

	// Saved config has AllowPrivateEndpoints=false: per-request true MUST NOT elevate.
	denied := newProbeTestService().Probe(context.Background(), ProbeRequest{
		Endpoint:              probeEndpoint(server.URL, "temporary-token"),
		AllowPrivateEndpoints: &allowPrivate,
	})
	require.False(t, denied.OK)
	require.Equal(t, "endpoint_unsafe", denied.ErrorCode)
	require.False(t, denied.Retryable)

	// Saved config has AllowPrivateEndpoints=true: probe succeeds with saved policy.
	allowed := newProbeTestServiceWithAllowPrivate(true).Probe(context.Background(), ProbeRequest{
		Endpoint:              probeEndpoint(server.URL, "temporary-token"),
		AllowPrivateEndpoints: &allowPrivate,
	})
	require.True(t, allowed.OK, allowed.Message)

	// Per-request AllowPrivateEndpoints=false may still narrow saved policy to deny private targets.
	disallowPrivate := false
	narrowed := newProbeTestServiceWithAllowPrivate(true).Probe(context.Background(), ProbeRequest{
		Endpoint:              probeEndpoint(server.URL, "temporary-token"),
		AllowPrivateEndpoints: &disallowPrivate,
	})
	require.False(t, narrowed.OK)
	require.Equal(t, "endpoint_unsafe", narrowed.ErrorCode)
}

func TestPromptProbeBlocksRedirectToPrivateAndHTTPSDowngrade(t *testing.T) {
	t.Run("redirect to private IP is blocked when saved allow_private_endpoints is false", func(t *testing.T) {
		privateTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			t.Error("private redirect target must never be reached")
			w.WriteHeader(http.StatusOK)
		}))
		defer privateTarget.Close()

		redirectServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, privateTarget.URL, http.StatusFound)
		}))
		defer redirectServer.Close()

		// Simulate public host resolving to loopback (blocked at dial) and also verify
		// NewSecureHTTPClient + Probe redirect guard blocks redirect to 127.0.0.1.
		allowPrivate := true
		ctx := urlvalidator.WithResolver(context.Background(), func(_ context.Context, _ string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
		})
		res := newProbeTestService().Probe(ctx, ProbeRequest{
			Endpoint:              probeEndpoint("https://public-guard.example.com", "temporary-token"),
			AllowPrivateEndpoints: &allowPrivate,
		})
		require.False(t, res.OK)
		require.Equal(t, "endpoint_unsafe", res.ErrorCode)
		require.False(t, res.Retryable)
	})

	t.Run("HTTPS to HTTP redirect downgrade is rejected by NewSecureHTTPClient and Scanner client", func(t *testing.T) {
		ep := ActiveEndpoint{
			ID:                    "ep-downgrade",
			BaseURL:               "https://guard.example.com",
			AllowPrivateEndpoints: true,
			TimeoutMS:             2000,
		}
		client, err := NewSecureHTTPClient(ep)
		require.NoError(t, err)

		scanner := NewOpenAICompatibleScanner()
		scannerClient, err := scanner.clientFor(ep)
		require.NoError(t, err)

		originReq, err := http.NewRequest(http.MethodPost, "https://guard.example.com/v1/chat/completions", nil)
		require.NoError(t, err)
		downgradeReq, err := http.NewRequest(http.MethodPost, "http://guard.example.com/v1/chat/completions", nil)
		require.NoError(t, err)
		sameHTTPSReq, err := http.NewRequest(http.MethodPost, "https://guard.example.com/v2/chat/completions", nil)
		require.NoError(t, err)

		require.ErrorIs(t, client.CheckRedirect(downgradeReq, []*http.Request{originReq}), urlvalidator.ErrOutboundTargetNotAllowed)
		require.NoError(t, client.CheckRedirect(sameHTTPSReq, []*http.Request{originReq}))
		require.ErrorIs(t, scannerClient.CheckRedirect(downgradeReq, []*http.Request{originReq}), urlvalidator.ErrOutboundTargetNotAllowed)
		require.NoError(t, scannerClient.CheckRedirect(sameHTTPSReq, []*http.Request{originReq}))
	})
}

// 保存配置时：未打开开关不得写入私网字面量；打开开关后才允许。
func TestPromptAuditSaveRejectsPrivateEndpointWithoutSwitch(t *testing.T) {
	manager := &ConfigManager{encryptor: prefixEncryptor{}, encryptionKeyConfigured: true}
	request := UpdateConfigRequest{
		ExpectedConfigVersion: 1, Strategy: "priority", WorkerCount: 1, QueueCapacity: 10,
		Scanners: []string{"PII"}, AllGroups: true,
		Endpoints: []UpdateEndpoint{{
			ID: "one", Name: "One", Protocol: "openai_compatible",
			BaseURL: "http://127.0.0.1:8080", TimeoutMS: 1000, InputLimit: 1000,
		}},
	}
	_, err := manager.buildNextStorage(DefaultStorageConfig(), request, 9)
	require.Error(t, err)
	require.Contains(t, err.Error(), "allow_private_endpoints")

	request.AllowPrivateEndpoints = true
	storage, err := manager.buildNextStorage(DefaultStorageConfig(), request, 9)
	require.NoError(t, err)
	require.True(t, storage.AllowPrivateEndpoints)

	active, err := ActiveFromStorage(storage, true, prefixEncryptor{})
	require.NoError(t, err)
	require.True(t, active.AllowPrivateEndpoints)
	require.True(t, active.Endpoints[0].AllowPrivateEndpoints)
}
