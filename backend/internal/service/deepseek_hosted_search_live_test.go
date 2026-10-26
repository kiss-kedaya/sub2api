//go:build unit

package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// This acceptance test never creates users, starts an application, signs a
// transaction, or writes to a database. It runs only when explicitly enabled
// with credentials supplied by the operator's environment, and logs no bodies.
type deepSeekHostedLiveHTTP struct{ client *http.Client }

func (u *deepSeekHostedLiveHTTP) Do(request *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return u.client.Do(request)
}

func (u *deepSeekHostedLiveHTTP) DoWithTLS(request *http.Request, proxy string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(request, proxy, accountID, concurrency)
}

func TestDeepSeekHostedSearchLive(t *testing.T) {
	if os.Getenv("SUB2API_DEEPSEEK_HOSTED_SEARCH_LIVE") != "1" {
		t.Skip("set SUB2API_DEEPSEEK_HOSTED_SEARCH_LIVE=1 to enable paid upstream acceptance")
	}
	get := func(name string) string {
		value := strings.TrimSpace(os.Getenv(name))
		if value == "" {
			t.Fatalf("required live-test environment variable is unset: %s", name)
		}
		return value
	}
	modelURL := get("SUB2API_DEEPSEEK_LIVE_MODEL_URL")
	modelKey := get("SUB2API_DEEPSEEK_LIVE_MODEL_KEY")
	searchURL := get("SUB2API_DEEPSEEK_LIVE_SEARCH_URL")
	searchKey := get("SUB2API_DEEPSEEK_LIVE_SEARCH_KEY")
	model := get("SUB2API_DEEPSEEK_LIVE_MODEL")
	modes := []bool{false, true}
	if selected := strings.TrimSpace(os.Getenv("SUB2API_DEEPSEEK_LIVE_STREAM")); selected != "" {
		stream, err := strconv.ParseBool(selected)
		if err != nil {
			t.Fatal("SUB2API_DEEPSEEK_LIVE_STREAM must be true or false")
		}
		modes = []bool{stream}
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport, Timeout: 120 * time.Second}
	for _, stream := range modes {
		t.Run("stream_"+strconv.FormatBool(stream), func(t *testing.T) {
			cfg := &config.Config{}
			cfg.Gateway.UpstreamResponseReadMaxBytes = 16 << 20
			cfg.Gateway.MaxLineSize = 16 << 20
			svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: &deepSeekHostedLiveHTTP{client: client}}
			groupID, price := int64(36), 5.0
			group := &Group{ID: groupID, Platform: PlatformDeepseek, Status: StatusActive, Hydrated: true,
				SearchPricePer1k: &price, SearchProviderBaseURL: &searchURL, SearchProviderAPIKey: &searchKey}
			key := &APIKey{ID: 100001, UserID: 100002, GroupID: &groupID, Group: group,
				Status: StatusActive, User: &User{ID: 100002, Status: StatusActive}}
			account := &Account{ID: 100003, Platform: PlatformDeepseek, Type: AccountTypeAPIKey, Status: StatusActive,
				Credentials: map[string]any{"base_url": modelURL, "api_key": modelKey, "api_protocol": APIProtocolResponses}}
			body, err := json.Marshal(map[string]any{
				"model": model, "stream": stream, "max_output_tokens": 1024,
				"reasoning":   map[string]string{"effort": "none"},
				"input":       "Search the web for the official DeepSeek Search API guide. Reply in at most three short sentences, include its real documentation URL, and explain that Search is a separate API. Do not call any other tools.",
				"tools":       []any{map[string]string{"type": "web_search"}},
				"tool_choice": map[string]string{"type": "web_search"},
			})
			require.NoError(t, err)
			ctx, cancel := context.WithTimeout(t.Context(), 150*time.Second)
			defer cancel()
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body)).WithContext(ctx)
			c.Request.Header.Set("Content-Type", "application/json")
			c.Set("api_key", key)
			result, forwardErr := svc.Forward(ctx, c, account, body)
			if forwardErr != nil {
				t.Fatalf("live hosted-search forwarding failed (%T); response content withheld", forwardErr)
			}
			require.Equal(t, http.StatusOK, recorder.Code)
			require.NotNil(t, result)
			require.Greater(t, result.SearchCount, 0, "at least one real billable search must succeed")
			require.Zero(t, result.WebSearchCalls, "hosted search must retain token plus search accounting")
			require.Greater(t, result.Usage.InputTokens, 0)
			require.Greater(t, result.Usage.OutputTokens, 0)
			final := recorder.Body.Bytes()
			if stream {
				scanner := bufio.NewScanner(bytes.NewReader(final))
				scanner.Buffer(make([]byte, 4096), 16<<20)
				final = nil
				for scanner.Scan() {
					line := scanner.Text()
					if !strings.HasPrefix(line, "data:") {
						continue
					}
					raw := []byte(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
					if gjson.GetBytes(raw, "type").String() == "response.completed" {
						final = []byte(gjson.GetBytes(raw, "response").Raw)
					}
				}
				require.NoError(t, scanner.Err())
			}
			require.True(t, gjson.ValidBytes(final), "final response must be standard JSON")
			foundSearch, foundSource, foundMessage := false, false, false
			for _, item := range gjson.GetBytes(final, "output").Array() {
				if item.Get("type").String() == "web_search_call" {
					foundSearch = true
					for _, source := range item.Get("action.sources").Array() {
						foundSource = foundSource || deepSeekHostedSearchPublicURL(source.Get("url").String())
					}
				}
				if item.Get("type").String() == "message" {
					for _, part := range item.Get("content").Array() {
						text := part.Get("text").String()
						foundMessage = foundMessage || strings.Contains(text, "https://api-docs.deepseek.com/")
					}
				}
			}
			require.True(t, foundSearch, "standard hosted-search output item must be visible")
			require.True(t, foundSource, "search output must contain a real public source URL")
			require.True(t, foundMessage, "the final assistant answer must contain the real documentation URL")
		})
	}
}
