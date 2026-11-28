//go:build unit

package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateMonitorEndpoint_BasePath(t *testing.T) {
	for _, endpoint := range []string{
		"https://8.8.8.8",
		"https://8.8.8.8/anthropic",
		"https://8.8.8.8/anthropic/",
		"https://8.8.8.8/anthropic/v1",
		"https://8.8.8.8/tenant%2Fone/anthropic",
	} {
		t.Run(endpoint, func(t *testing.T) {
			require.NoError(t, validateEndpoint(endpoint))
		})
	}

	for _, tc := range []struct {
		endpoint string
		want     error
	}{
		{"http://8.8.8.8/anthropic", ErrChannelMonitorEndpointScheme},
		{"https://8.8.8.8/anthropic?key=secret", ErrChannelMonitorEndpointPath},
		{"https://8.8.8.8/anthropic#fragment", ErrChannelMonitorEndpointPath},
		{"https://127.0.0.1/anthropic", ErrChannelMonitorEndpointPrivate},
		{"https://10.0.0.1/anthropic", ErrChannelMonitorEndpointPrivate},
		{"https://169.254.169.254/anthropic", ErrChannelMonitorEndpointPrivate},
		{"https://[::1]/anthropic", ErrChannelMonitorEndpointPrivate},
		{"https://[fd00::1]/anthropic", ErrChannelMonitorEndpointPrivate},
		{"https:///anthropic", ErrChannelMonitorInvalidEndpoint},
	} {
		t.Run(tc.endpoint, func(t *testing.T) {
			require.ErrorIs(t, validateEndpoint(tc.endpoint), tc.want)
		})
	}
}

func TestJoinMonitorURL_DoesNotMatchHostOrEncodedSlash(t *testing.T) {
	require.Equal(t, "https://v1/v1/messages", joinURL("https://v1", "/v1/messages"))
	require.Equal(t, "https://relay.example/tenant%2Fv1/v1/messages",
		joinURL("https://relay.example/tenant%2Fv1", "/v1/messages"))
}

func TestJoinMonitorURL_DoesNotDuplicateFullAPIPath(t *testing.T) {
	require.Equal(t, "https://x.example/v1/messages", joinURL("https://x.example/v1/messages", "/v1/messages"))
	require.Equal(t, "https://x.example/anthropic/v1/messages", joinURL("https://x.example/anthropic/v1/messages", "/v1/messages"))
}

func TestNormalizeMonitorEndpoint_PreservesBasePath(t *testing.T) {
	require.Equal(t, "https://api.deepseek.com/anthropic",
		normalizeEndpoint("  https://api.deepseek.com/anthropic/  "))
	require.Equal(t, "https://relay.example/tenant%2Fone/anthropic/v1",
		normalizeEndpoint("https://relay.example/tenant%2Fone/anthropic/v1/"))
}

func TestCallProvider_BasePath(t *testing.T) {
	swapMonitorHTTPClient(t)
	requests := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.RequestURI
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{}"))
	}))
	t.Cleanup(server.Close)

	for _, tc := range []struct {
		name     string
		provider string
		apiMode  string
		basePath string
		wantPath string
	}{
		{"anthropic origin", MonitorProviderAnthropic, "", "", "/v1/messages"},
		{"anthropic prefix", MonitorProviderAnthropic, "", "/anthropic", "/anthropic/v1/messages"},
		{"anthropic trailing slash", MonitorProviderAnthropic, "", "/anthropic/", "/anthropic/v1/messages"},
		{"anthropic version", MonitorProviderAnthropic, "", "/anthropic/v1", "/anthropic/v1/messages"},
		{"version lookalike", MonitorProviderAnthropic, "", "/anthropic/v10", "/anthropic/v10/v1/messages"},
		{"encoded prefix", MonitorProviderAnthropic, "", "/tenant%2Fone/anthropic", "/tenant%2Fone/anthropic/v1/messages"},
		{"openai chat version", MonitorProviderOpenAI, "", "/relay/v1/", "/relay/v1/chat/completions"},
		{"openai responses version", MonitorProviderOpenAI, MonitorAPIModeResponses, "/relay/v1", "/relay/v1/responses"},
		{"gemini version", MonitorProviderGemini, "", "/relay/v1beta", "/relay/v1beta/models/test-model:generateContent"},
		{"zhipu version", MonitorProviderZhipu, "", "/api/paas/v4", "/api/paas/v4/chat/completions"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, status, err := callProvider(context.Background(), tc.provider,
				server.URL+tc.basePath, "test-key", "test-model", "hello", &CheckOptions{APIMode: tc.apiMode})
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, status)
			require.Equal(t, tc.wantPath, <-requests)
		})
	}
}

type updateChannelMonitorRepoStub struct {
	ChannelMonitorRepository
	stored  *ChannelMonitor
	updated []*ChannelMonitor
}

func (r *updateChannelMonitorRepoStub) GetByID(_ context.Context, id int64) (*ChannelMonitor, error) {
	if r.stored == nil || r.stored.ID != id {
		return nil, ErrChannelMonitorNotFound
	}
	cp := *r.stored
	return &cp, nil
}

func (r *updateChannelMonitorRepoStub) Update(_ context.Context, m *ChannelMonitor) error {
	cp := *m
	r.stored = &cp
	r.updated = append(r.updated, &cp)
	return nil
}

func TestChannelMonitorServiceUpdateRefusesStoredAPIKeyReuseOnOriginChange(t *testing.T) {
	baseMonitor := func(endpoint, encKey string) *ChannelMonitor {
		return &ChannelMonitor{
			ID:              7,
			Name:            "prod-relay",
			Provider:        MonitorProviderOpenAI,
			APIMode:         MonitorAPIModeChatCompletions,
			CheckMode:       MonitorCheckModeProbe,
			Endpoint:        endpoint,
			APIKey:          encKey,
			PrimaryModel:    "gpt-5",
			IntervalSeconds: 60,
			Enabled:         true,
		}
	}

	t.Run("same origin path change preserves stored encrypted key", func(t *testing.T) {
		repo := &updateChannelMonitorRepoStub{stored: baseMonitor("https://8.8.8.8/v1", "OLD:stored-secret")}
		svc := NewChannelMonitorService(repo, &duplicateChannelMonitorEncryptor{})
		nextEndpoint := "https://8.8.8.8:443/v2"
		updated, err := svc.Update(context.Background(), 7, ChannelMonitorUpdateParams{Endpoint: &nextEndpoint})
		require.NoError(t, err)
		require.Equal(t, "stored-secret", updated.APIKey)
		require.Len(t, repo.updated, 1)
		require.Equal(t, "OLD:stored-secret", repo.updated[0].APIKey)
	})

	t.Run("different host without new key is rejected", func(t *testing.T) {
		repo := &updateChannelMonitorRepoStub{stored: baseMonitor("https://8.8.8.8/v1", "OLD:stored-secret")}
		svc := NewChannelMonitorService(repo, &duplicateChannelMonitorEncryptor{})
		nextEndpoint := "https://1.1.1.1/v1"
		_, err := svc.Update(context.Background(), 7, ChannelMonitorUpdateParams{Endpoint: &nextEndpoint})
		require.ErrorIs(t, err, ErrChannelMonitorAPIKeyRequiredOnEndpointChange)
		require.Empty(t, repo.updated)
	})

	t.Run("different port without new key is rejected", func(t *testing.T) {
		repo := &updateChannelMonitorRepoStub{stored: baseMonitor("https://8.8.8.8/v1", "OLD:stored-secret")}
		svc := NewChannelMonitorService(repo, &duplicateChannelMonitorEncryptor{})
		nextEndpoint := "https://8.8.8.8:8443/v1"
		_, err := svc.Update(context.Background(), 7, ChannelMonitorUpdateParams{Endpoint: &nextEndpoint})
		require.ErrorIs(t, err, ErrChannelMonitorAPIKeyRequiredOnEndpointChange)
		require.Empty(t, repo.updated)
	})

	t.Run("two-step empty endpoint to attacker host without new key is rejected", func(t *testing.T) {
		repo := &updateChannelMonitorRepoStub{stored: baseMonitor("", "OLD:stored-secret")}
		svc := NewChannelMonitorService(repo, &duplicateChannelMonitorEncryptor{})
		nextEndpoint := "https://1.1.1.1/v1"
		_, err := svc.Update(context.Background(), 7, ChannelMonitorUpdateParams{Endpoint: &nextEndpoint})
		require.ErrorIs(t, err, ErrChannelMonitorAPIKeyRequiredOnEndpointChange)
		require.Empty(t, repo.updated)
	})

	t.Run("different host with explicit new key succeeds and replaces secret", func(t *testing.T) {
		repo := &updateChannelMonitorRepoStub{stored: baseMonitor("https://8.8.8.8/v1", "OLD:stored-secret")}
		svc := NewChannelMonitorService(repo, &duplicateChannelMonitorEncryptor{})
		nextEndpoint := "https://1.1.1.1/v1"
		newKey := "fresh-monitor-key"
		updated, err := svc.Update(context.Background(), 7, ChannelMonitorUpdateParams{
			Endpoint: &nextEndpoint,
			APIKey:   &newKey,
		})
		require.NoError(t, err)
		require.Equal(t, "fresh-monitor-key", updated.APIKey)
		require.Len(t, repo.updated, 1)
		require.Equal(t, "NEW:fresh-monitor-key", repo.updated[0].APIKey)
	})
}
