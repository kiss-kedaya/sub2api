package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

func TestCloudflareOpenAIBaseURLIgnoresManualBaseURL(t *testing.T) {
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeCloudflare,
		Credentials: map[string]any{
			"account_id": "a1b2c3d4e5f67890",
			"base_url":   "https://evil.example/ai/run",
			"api_key":    "cf-token",
		},
	}
	require.Equal(t, "https://api.cloudflare.com/client/v4/accounts/a1b2c3d4e5f67890/ai/v1", account.GetOpenAIBaseURL())
	require.Equal(t, "https://api.cloudflare.com/client/v4/accounts/a1b2c3d4e5f67890/ai/v1/chat/completions", buildOpenAIChatCompletionsURL(account.GetOpenAIBaseURL()))
	require.Equal(t, "cf-token", account.GetOpenAIProtocolAPIKey())
	require.True(t, shouldForwardOpenAIResponsesViaRawChatCompletions(account))
	require.True(t, shouldPreemptivelyConvertInboundResponsesToChat(account))
}

func TestCloudflareOpenAIRejectsUnsafeAccountID(t *testing.T) {
	t.Parallel()
	account := &Account{
		Platform:    PlatformOpenAI,
		Type:        AccountTypeCloudflare,
		Credentials: map[string]any{"account_id": "../etc", "api_key": "cf-token"},
	}
	require.Empty(t, account.GetOpenAIBaseURL())
	require.Error(t, validateCloudflareAccount(account))
}

func TestCloudflareOpenAIDoesNotSendTokenToOpenAI(t *testing.T) {
	t.Parallel()
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeCloudflare,
		Credentials: map[string]any{
			"account_id": "a1b2c3d4e5f67890",
			"api_key":    "cf-token",
		},
	}
	require.True(t, shouldEstimateOpenAIInputTokensLocally(account))
	svc := &OpenAIGatewayService{}
	_, err := svc.buildOpenAIResponsesWSURL(account)
	require.Error(t, err)
	_, err = svc.buildInputTokensUpstreamRequest(context.Background(), nil, account, []byte(`{}`), "cf-token")
	require.Error(t, err)
	_, err = svc.buildUpstreamRequest(context.Background(), nil, account, []byte(`{}`), "cf-token", false, "", false)
	require.Error(t, err)
	_, err = svc.buildUpstreamRequestOpenAIPassthrough(context.Background(), nil, account, []byte(`{}`), "cf-token")
	require.Error(t, err)
}

func TestParseCloudflareModelSearchUsesName(t *testing.T) {
	t.Parallel()
	body := []byte(`{"success":true,"result":[{"id":"uuid-1","name":"@cf/meta/llama-3.1-8b-instruct"},{"id":"uuid-2","name":"  "},{"id":"uuid-3","name":"@cf/meta/llama-3.1-8b-instruct"}],"result_info":{"page":1,"per_page":50,"count":2,"total_count":2}}`)
	names, total, err := parseCloudflareModelSearch(body)
	require.NoError(t, err)
	require.Equal(t, []string{"@cf/meta/llama-3.1-8b-instruct"}, names)
	require.Equal(t, 2, total)
}

func TestFetchCloudflareModelNamesPaginates(t *testing.T) {
	var pages []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages = append(pages, r.URL.RawQuery)
		if r.Header.Get("Authorization") != "Bearer cf-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("page") == "1" {
			_, _ = w.Write([]byte(`{"success":true,"result":[{"name":"@cf/one"}],"result_info":{"total_count":2,"count":1}}`))
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"result":[{"name":"@cf/two"}],"result_info":{"total_count":2,"count":1}}`))
	}))
	defer server.Close()

	originalRoot := cloudflareOpenAIAPIRoot
	cloudflareOpenAIAPIRoot = server.URL + "/client/v4/accounts/"
	t.Cleanup(func() { cloudflareOpenAIAPIRoot = originalRoot })

	account := &Account{
		ID:       7,
		Platform: PlatformOpenAI,
		Type:     AccountTypeCloudflare,
		Credentials: map[string]any{
			"account_id": "a1b2c3d4e5f67890",
			"api_key":    "cf-token",
		},
	}
	names, err := fetchCloudflareModelNames(context.Background(), cloudflareTestUpstream{client: server.Client()}, account)
	require.NoError(t, err)
	require.Equal(t, []string{"@cf/one", "@cf/two"}, names)
	require.Equal(t, []string{"page=1&per_page=50", "page=2&per_page=50"}, pages)
}

type cloudflareTestUpstream struct {
	client *http.Client
}

func (c cloudflareTestUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return c.client.Do(req)
}

func (c cloudflareTestUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return c.Do(req, proxyURL, accountID, accountConcurrency)
}
