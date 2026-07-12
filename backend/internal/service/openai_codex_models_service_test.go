package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
)

type codexModelsHTTPUpstreamStub struct {
	do func(req *http.Request, proxyURL string, accountID int64, accountConcurrency int) (*http.Response, error)
}

func (s *codexModelsHTTPUpstreamStub) Do(req *http.Request, proxyURL string, accountID int64, accountConcurrency int) (*http.Response, error) {
	return s.do(req, proxyURL, accountID, accountConcurrency)
}

func (s *codexModelsHTTPUpstreamStub) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return s.Do(req, proxyURL, accountID, accountConcurrency)
}

func newCodexModelsAPIKeyTestService(upstream HTTPUpstream) *OpenAIGatewayService {
	return &OpenAIGatewayService{
		cfg: &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{
			Enabled: false,
		}}},
		httpUpstream: upstream,
	}
}

func newCodexModelsAPIKeyTestAccount(baseURL string) *Account {
	credentials := map[string]any{"api_key": "sk-upstream"}
	if baseURL != "" {
		credentials["base_url"] = baseURL
	}
	return &Account{
		ID:          2,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Credentials: credentials,
		Concurrency: 3,
	}
}

func newCodexModelsTestAccount() *Account {
	return &Account{
		ID:       1,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token":       "test-access-token",
			"chatgpt_account_id": "acc-123",
		},
	}
}

func TestFetchCodexModelsManifestPassthrough(t *testing.T) {
	manifestBody := `{"models":[{"slug":"gpt-5.5","display_name":"GPT-5.5"}]}`

	var gotAuth, gotAccountID, gotOriginator, gotClientVersion string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAccountID = r.Header.Get("chatgpt-account-id")
		gotOriginator = r.Header.Get("Originator")
		gotClientVersion = r.URL.Query().Get("client_version")
		w.Header().Set("ETag", `W/"abc123"`)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(manifestBody))
	}))
	defer server.Close()

	original := chatgptCodexModelsURL
	chatgptCodexModelsURL = server.URL
	defer func() { chatgptCodexModelsURL = original }()

	s := &OpenAIGatewayService{}
	manifest, err := s.FetchCodexModelsManifest(context.Background(), newCodexModelsTestAccount(), "0.137.0", "")
	if err != nil {
		t.Fatalf("FetchCodexModelsManifest returned error: %v", err)
	}

	if string(manifest.Body) != manifestBody {
		t.Errorf("body not passed through verbatim: got %q", manifest.Body)
	}
	if manifest.ETag != `W/"abc123"` {
		t.Errorf("etag not passed through: got %q", manifest.ETag)
	}
	if gotAuth != "Bearer test-access-token" {
		t.Errorf("authorization header: got %q", gotAuth)
	}
	if gotAccountID != "acc-123" {
		t.Errorf("chatgpt-account-id header: got %q", gotAccountID)
	}
	if gotOriginator != "codex_cli_rs" {
		t.Errorf("originator header: got %q", gotOriginator)
	}
	if gotClientVersion != "0.137.0" {
		t.Errorf("client_version query: got %q", gotClientVersion)
	}
}

func TestFetchCodexModelsManifestDefaultClientVersion(t *testing.T) {
	var gotClientVersion string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotClientVersion = r.URL.Query().Get("client_version")
		_, _ = w.Write([]byte(`{"models":[]}`))
	}))
	defer server.Close()

	original := chatgptCodexModelsURL
	chatgptCodexModelsURL = server.URL
	defer func() { chatgptCodexModelsURL = original }()

	s := &OpenAIGatewayService{}
	if _, err := s.FetchCodexModelsManifest(context.Background(), newCodexModelsTestAccount(), "", ""); err != nil {
		t.Fatalf("FetchCodexModelsManifest returned error: %v", err)
	}
	if gotClientVersion != openAICodexProbeVersion {
		t.Errorf("default client_version: got %q, want %q", gotClientVersion, openAICodexProbeVersion)
	}
}

func TestFetchCodexModelsManifestNotModified(t *testing.T) {
	var gotIfNoneMatch string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotIfNoneMatch = r.Header.Get("If-None-Match")
		w.Header().Set("ETag", `W/"abc123"`)
		w.WriteHeader(http.StatusNotModified)
	}))
	defer server.Close()

	original := chatgptCodexModelsURL
	chatgptCodexModelsURL = server.URL
	defer func() { chatgptCodexModelsURL = original }()

	s := &OpenAIGatewayService{}
	manifest, err := s.FetchCodexModelsManifest(context.Background(), newCodexModelsTestAccount(), "0.137.0", `W/"abc123"`)
	if err != nil {
		t.Fatalf("FetchCodexModelsManifest returned error: %v", err)
	}
	if !manifest.NotModified {
		t.Error("expected NotModified to be true")
	}
	if gotIfNoneMatch != `W/"abc123"` {
		t.Errorf("if-none-match header: got %q", gotIfNoneMatch)
	}
}

func TestFetchCodexModelsManifestUpstreamError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"detail":"boom"}`, http.StatusInternalServerError)
	}))
	defer server.Close()

	original := chatgptCodexModelsURL
	chatgptCodexModelsURL = server.URL
	defer func() { chatgptCodexModelsURL = original }()

	s := &OpenAIGatewayService{}
	if _, err := s.FetchCodexModelsManifest(context.Background(), newCodexModelsTestAccount(), "0.137.0", ""); err == nil {
		t.Fatal("expected error for upstream 500, got nil")
	}
}

func TestFetchCodexModelsManifestMissingToken(t *testing.T) {
	account := newCodexModelsTestAccount()
	delete(account.Credentials, "access_token")

	s := &OpenAIGatewayService{}
	if _, err := s.FetchCodexModelsManifest(context.Background(), account, "0.137.0", ""); err == nil {
		t.Fatal("expected error for missing access token, got nil")
	}
}

func TestFetchCodexModelsManifestAPIKeyCustomUpstream(t *testing.T) {
	manifestBody := `{"models":[{"slug":"gpt-5.6"}]}`
	var gotRequest *http.Request
	var gotProxyURL string
	var gotAccountID int64
	var gotConcurrency int
	upstream := &codexModelsHTTPUpstreamStub{do: func(req *http.Request, proxyURL string, accountID int64, accountConcurrency int) (*http.Response, error) {
		gotRequest = req
		gotProxyURL = proxyURL
		gotAccountID = accountID
		gotConcurrency = accountConcurrency
		header := make(http.Header)
		header.Set("ETag", `W/"api-key-manifest"`)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     header,
			Body:       io.NopCloser(strings.NewReader(manifestBody)),
		}, nil
	}}

	s := newCodexModelsAPIKeyTestService(upstream)
	manifest, err := s.FetchCodexModelsManifest(
		context.Background(),
		newCodexModelsAPIKeyTestAccount("https://upstream.example/v1"),
		"0.144.0",
		"",
	)
	if err != nil {
		t.Fatalf("FetchCodexModelsManifest returned error: %v", err)
	}

	if gotRequest == nil {
		t.Fatal("expected request to custom API key upstream")
	}
	if gotRequest.Method != http.MethodGet {
		t.Errorf("method: got %q", gotRequest.Method)
	}
	if gotRequest.URL.String() != "https://upstream.example/v1/models?client_version=0.144.0" {
		t.Errorf("request URL: got %q", gotRequest.URL.String())
	}
	if gotRequest.Header.Get("Authorization") != "Bearer sk-upstream" {
		t.Errorf("authorization header: got %q", gotRequest.Header.Get("Authorization"))
	}
	if gotRequest.Header.Get("Originator") != "codex_cli_rs" {
		t.Errorf("originator header: got %q", gotRequest.Header.Get("Originator"))
	}
	if gotRequest.Header.Get("Version") != "0.144.0" {
		t.Errorf("version header: got %q", gotRequest.Header.Get("Version"))
	}
	if gotRequest.Header.Get("User-Agent") != codexCLIUserAgent {
		t.Errorf("user-agent header: got %q", gotRequest.Header.Get("User-Agent"))
	}
	if gotRequest.Header.Get("chatgpt-account-id") != "" {
		t.Errorf("chatgpt-account-id must not be sent to API key upstream: got %q", gotRequest.Header.Get("chatgpt-account-id"))
	}
	if gotProxyURL != "" || gotAccountID != 2 || gotConcurrency != 3 {
		t.Errorf("upstream routing metadata: proxy=%q account_id=%d concurrency=%d", gotProxyURL, gotAccountID, gotConcurrency)
	}
	if string(manifest.Body) != manifestBody {
		t.Errorf("body not passed through verbatim: got %q", manifest.Body)
	}
	if manifest.ETag != `W/"api-key-manifest"` {
		t.Errorf("etag not passed through: got %q", manifest.ETag)
	}
}

func TestFetchCodexModelsManifestAPIKeyNotModified(t *testing.T) {
	var gotIfNoneMatch string
	upstream := &codexModelsHTTPUpstreamStub{do: func(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
		gotIfNoneMatch = req.Header.Get("If-None-Match")
		header := make(http.Header)
		header.Set("ETag", `W/"api-key-manifest"`)
		return &http.Response{
			StatusCode: http.StatusNotModified,
			Header:     header,
			Body:       http.NoBody,
		}, nil
	}}

	s := newCodexModelsAPIKeyTestService(upstream)
	manifest, err := s.FetchCodexModelsManifest(
		context.Background(),
		newCodexModelsAPIKeyTestAccount("https://upstream.example"),
		"0.144.0",
		`W/"api-key-manifest"`,
	)
	if err != nil {
		t.Fatalf("FetchCodexModelsManifest returned error: %v", err)
	}
	if !manifest.NotModified {
		t.Error("expected NotModified to be true")
	}
	if manifest.ETag != `W/"api-key-manifest"` {
		t.Errorf("etag not passed through: got %q", manifest.ETag)
	}
	if gotIfNoneMatch != `W/"api-key-manifest"` {
		t.Errorf("if-none-match header: got %q", gotIfNoneMatch)
	}
}

func TestFetchCodexModelsManifestAPIKeyPreservesBaseURLQuery(t *testing.T) {
	var gotURL string
	upstream := &codexModelsHTTPUpstreamStub{do: func(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
		gotURL = req.URL.String()
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"models":[]}`)),
		}, nil
	}}

	s := newCodexModelsAPIKeyTestService(upstream)
	_, err := s.FetchCodexModelsManifest(
		context.Background(),
		newCodexModelsAPIKeyTestAccount("https://upstream.example/v1?tenant=acme"),
		"0.144.0",
		"",
	)
	if err != nil {
		t.Fatalf("FetchCodexModelsManifest returned error: %v", err)
	}
	if gotURL != "https://upstream.example/v1/models?client_version=0.144.0&tenant=acme" {
		t.Errorf("request URL: got %q", gotURL)
	}
}

func TestFetchCodexModelsManifestAPIKeyRejectsBaseURLFragment(t *testing.T) {
	called := false
	upstream := &codexModelsHTTPUpstreamStub{do: func(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
		called = true
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"models":[]}`)),
		}, nil
	}}

	s := newCodexModelsAPIKeyTestService(upstream)
	_, err := s.FetchCodexModelsManifest(
		context.Background(),
		newCodexModelsAPIKeyTestAccount("https://upstream.example/v1#models"),
		"0.144.0",
		"",
	)
	if err == nil {
		t.Fatal("expected invalid upstream base URL error, got nil")
	}
	if infraerrors.Reason(err) != "OPENAI_CODEX_MODELS_API_KEY_UPSTREAM_INVALID" {
		t.Errorf("error reason: got %q", infraerrors.Reason(err))
	}
	if called {
		t.Fatal("fragment-bearing base URL must be rejected before the upstream request")
	}
}

func TestFetchCodexModelsManifestAPIKeyUpstreamError(t *testing.T) {
	upstream := &codexModelsHTTPUpstreamStub{do: func(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Status:     "429 Too Many Requests",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"error":"rate limited"}`)),
		}, nil
	}}

	s := newCodexModelsAPIKeyTestService(upstream)
	_, err := s.FetchCodexModelsManifest(
		context.Background(),
		newCodexModelsAPIKeyTestAccount("https://upstream.example"),
		"0.144.0",
		"",
	)
	if err == nil {
		t.Fatal("expected error for upstream 429, got nil")
	}
	if infraerrors.Code(err) != http.StatusBadGateway {
		t.Errorf("error status: got %d, want %d", infraerrors.Code(err), http.StatusBadGateway)
	}
	if infraerrors.Reason(err) != "OPENAI_CODEX_MODELS_UPSTREAM_FAILED" {
		t.Errorf("error reason: got %q", infraerrors.Reason(err))
	}
}

func TestFetchCodexModelsManifestAPIKeyRejectsOfficialOpenAIBaseURL(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
	}{
		{name: "missing base URL"},
		{name: "official host", baseURL: "https://api.openai.com"},
		{name: "official versioned URL", baseURL: "https://API.OPENAI.COM:443/v1/"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newCodexModelsAPIKeyTestService(&codexModelsHTTPUpstreamStub{do: func(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
				t.Fatal("official OpenAI API key must not be used as a Codex manifest upstream")
				return nil, nil
			}})

			_, err := s.FetchCodexModelsManifest(
				context.Background(),
				newCodexModelsAPIKeyTestAccount(tt.baseURL),
				"0.144.0",
				"",
			)
			if err == nil {
				t.Fatal("expected unsupported API key upstream error, got nil")
			}
			if infraerrors.Reason(err) != "OPENAI_CODEX_MODELS_API_KEY_UPSTREAM_UNSUPPORTED" {
				t.Errorf("error reason: got %q", infraerrors.Reason(err))
			}
		})
	}
}
