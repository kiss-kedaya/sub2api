//go:build unit

package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
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
type deepSeekHostedLiveHTTP struct {
	client       *http.Client
	mu           sync.Mutex
	observations []deepSeekHostedLiveObservation
}

type deepSeekHostedLiveObservation struct {
	Path        string   `json:"path"`
	Status      int      `json:"status"`
	ContentType string   `json:"content_type,omitempty"`
	Events      []string `json:"event_types,omitempty"`
	Keys        []string `json:"json_top_level_keys,omitempty"`
	ErrorTypes  []string `json:"error_types,omitempty"`
	ErrorCodes  []string `json:"error_codes,omitempty"`
	Truncated   bool     `json:"capture_truncated,omitempty"`
}

// Capture only for in-memory protocol classification. The bounded bytes are
// discarded on Close; only structural metadata reaches a test diagnostic.
type deepSeekHostedLiveBody struct {
	io.ReadCloser
	mu        sync.Mutex
	once      sync.Once
	body      []byte
	truncated bool
	closed    func([]byte, bool)
}

func (b *deepSeekHostedLiveBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.mu.Lock()
	remaining := (2 << 20) - len(b.body)
	if n > remaining {
		b.truncated = true
	}
	if remaining > n {
		remaining = n
	}
	if remaining > 0 {
		b.body = append(b.body, p[:remaining]...)
	}
	b.mu.Unlock()
	return n, err
}

func (b *deepSeekHostedLiveBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(func() {
		b.mu.Lock()
		body, truncated := b.body, b.truncated
		b.body = nil
		b.mu.Unlock()
		b.closed(body, truncated)
	})
	return err
}

// Protocol identifiers are small machine-readable tokens. Never emit an
// upstream message, arbitrary string, URL, query, tool argument, or header.
func deepSeekHostedLiveProtocolToken(raw string) string {
	if len(raw) == 0 || len(raw) > 100 {
		return ""
	}
	for _, r := range raw {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' && r != '-' && r != '.' {
			return ""
		}
	}
	return raw
}

func (u *deepSeekHostedLiveHTTP) protocolObservations() []deepSeekHostedLiveObservation {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]deepSeekHostedLiveObservation(nil), u.observations...)
}

func deepSeekHostedLiveClassify(observation *deepSeekHostedLiveObservation, body []byte) {
	noteError := func(raw []byte) {
		for _, path := range []string{"error.type", "response.error.type"} {
			if value := deepSeekHostedLiveProtocolToken(gjson.GetBytes(raw, path).String()); value != "" && len(observation.ErrorTypes) < 32 {
				observation.ErrorTypes = append(observation.ErrorTypes, value)
			}
		}
		for _, path := range []string{"error.code", "response.error.code"} {
			if value := deepSeekHostedLiveProtocolToken(gjson.GetBytes(raw, path).String()); value != "" && len(observation.ErrorCodes) < 32 {
				observation.ErrorCodes = append(observation.ErrorCodes, value)
			}
		}
	}
	if gjson.ValidBytes(body) {
		gjson.ParseBytes(body).ForEach(func(key, _ gjson.Result) bool {
			if value := deepSeekHostedLiveProtocolToken(key.String()); value != "" {
				observation.Keys = append(observation.Keys, value)
			}
			return len(observation.Keys) < 64
		})
		sort.Strings(observation.Keys)
		noteError(body)
		return
	}
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 4096), 2<<20)
	noteEvent := func(value string) {
		if len(observation.Events) < 256 {
			observation.Events = append(observation.Events, value)
		} else {
			// Retain the final event even after a long sequence of token deltas.
			observation.Events[len(observation.Events)-1] = "last:" + value
		}
	}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "event:") {
			if value := deepSeekHostedLiveProtocolToken(strings.TrimSpace(strings.TrimPrefix(line, "event:"))); value != "" {
				noteEvent("event:" + value)
			}
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		raw := []byte(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		if bytes.Equal(raw, []byte("[DONE]")) {
			noteEvent("[DONE]")
			continue
		}
		if !gjson.ValidBytes(raw) {
			continue
		}
		if value := deepSeekHostedLiveProtocolToken(gjson.GetBytes(raw, "type").String()); value != "" {
			noteEvent("data:" + value)
		} else if gjson.GetBytes(raw, "choices").IsArray() {
			noteEvent("data:chat_choices")
		}
		noteError(raw)
	}
}

func (u *deepSeekHostedLiveHTTP) Do(request *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	observation := deepSeekHostedLiveObservation{Path: request.URL.Path}
	response, err := u.client.Do(request)
	if response != nil {
		observation.Status = response.StatusCode
		observation.ContentType, _, _ = mime.ParseMediaType(response.Header.Get("Content-Type"))
	}
	if err != nil {
		observation.ErrorTypes = []string{"transport_error"}
	}
	u.mu.Lock()
	index := len(u.observations)
	u.observations = append(u.observations, observation)
	u.mu.Unlock()
	if response != nil && response.Body != nil {
		response.Body = &deepSeekHostedLiveBody{ReadCloser: response.Body, closed: func(body []byte, truncated bool) {
			observation.Truncated = truncated
			deepSeekHostedLiveClassify(&observation, body)
			u.mu.Lock()
			u.observations[index] = observation
			u.mu.Unlock()
		}}
	}
	return response, err
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
			upstream := &deepSeekHostedLiveHTTP{client: client}
			svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream}
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
				if observationJSON, marshalErr := json.Marshal(upstream.protocolObservations()); marshalErr == nil {
					t.Logf("safe upstream protocol observations: %s", observationJSON)
				}
				diagnostic := forwardErr.Error()
				for _, secret := range []string{modelURL, modelKey, searchURL, searchKey} {
					diagnostic = strings.ReplaceAll(diagnostic, secret, "[redacted]")
				}
				if len(diagnostic) > 300 {
					diagnostic = diagnostic[:300]
				}
				t.Fatalf("live hosted-search forwarding failed (%T): %s; response content withheld", forwardErr, diagnostic)
			}
			require.Equal(t, http.StatusOK, recorder.Code)
			require.NotNil(t, result)
			require.Greater(t, result.SearchCount, 0, "at least one real billable search must succeed")
			require.Zero(t, result.WebSearchCalls, "hosted search must retain token plus search accounting")
			require.Greater(t, result.Usage.InputTokens, 0)
			require.Greater(t, result.Usage.OutputTokens, 0)
			t.Logf("live hosted-search acceptance requests=%d searches=%d input_tokens=%d output_tokens=%d", len(upstream.protocolObservations()), result.SearchCount, result.Usage.InputTokens, result.Usage.OutputTokens)
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
