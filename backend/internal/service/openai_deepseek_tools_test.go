package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// DeepSeek Search/Fetch are raw JSON endpoints: the client body must reach the
// upstream unchanged, the Bearer key must come from the account, and a 2xx
// response bills exactly one web search call.
func TestForwardDeepSeekToolsSearchPassesThrough(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"query":"DeepSeek V4 发布","count":5,"need_content":true,"future_field":1}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/tools/search", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "X-Request-Id": []string{"ds-req-1"}},
		Body:       io.NopCloser(strings.NewReader(`{"results":[{"title":"t","url":"https://example.com"}]}`)),
	}}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	account := &Account{
		ID:       7,
		Platform: PlatformDeepseek,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "ds-key",
			"base_url": "https://api.deepseek.com",
		},
	}

	result, err := svc.ForwardDeepSeekTools(context.Background(), c, account, nil, DeepSeekToolsPathSearch, "", body)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 1, result.WebSearchCalls)
	require.Equal(t, DeepSeekToolsLabelSearch, result.Model)
	require.Equal(t, "ds-req-1", result.RequestID)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.JSONEq(t, `{"results":[{"title":"t","url":"https://example.com"}]}`, recorder.Body.String())

	require.Equal(t, "https://api.deepseek.com/v1/tools/search", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer ds-key", upstream.lastReq.Header.Get("Authorization"))
	require.Equal(t, string(body), string(upstream.lastBody))
}

// Fetch uses the same pass-through path with its own label.

// 分组可以单独指定一套搜索服务：模型账号是别家、或不想让搜索吃掉模型账号
// 额度时，用 group 上的 base_url + api_key 发搜索请求。
func TestForwardDeepSeekToolsPrefersGroupSearchProvider(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"query":"probe"}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/tools/search", bytes.NewReader(body))

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"results":[]}`)),
	}}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	account := &Account{
		ID:       8,
		Platform: PlatformDeepseek,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "model-account-key",
			"base_url": "https://api.deepseek.com",
		},
	}
	groupBase := "https://search.internal"
	groupKey := "search-only-key"
	group := &Group{ID: 36, SearchProviderBaseURL: &groupBase, SearchProviderAPIKey: &groupKey}

	_, err := svc.ForwardDeepSeekTools(context.Background(), c, account, group, DeepSeekToolsPathSearch, "", body)
	require.NoError(t, err)

	require.Equal(t, "https://search.internal/v1/tools/search", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer search-only-key", upstream.lastReq.Header.Get("Authorization"))
}

func TestForwardDeepSeekToolsFetchLabelsRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"url":"https://example.com/article"}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/tools/fetch", bytes.NewReader(body))

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"title":"Example","content":"# Example"}`)),
	}}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	account := &Account{
		ID: 8, Platform: PlatformDeepseek, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "ds-key", "base_url": "https://api.deepseek.com"},
	}

	result, err := svc.ForwardDeepSeekTools(context.Background(), c, account, nil, DeepSeekToolsPathFetch, "", body)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 1, result.WebSearchCalls)
	require.Equal(t, DeepSeekToolsLabelFetch, result.Model)
	require.Equal(t, "https://api.deepseek.com/v1/tools/fetch", upstream.lastReq.URL.String())
}

// An upstream 4xx is passed through and must not bill.
func TestForwardDeepSeekToolsUpstreamErrorIsNotBilled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/tools/search", bytes.NewReader([]byte(`{"query":"x"}`)))

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"'count' must be between 1 and 50","type":"invalid_request_error"}}`)),
	}}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	account := &Account{
		ID: 9, Platform: PlatformDeepseek, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "ds-key", "base_url": "https://api.deepseek.com"},
	}

	result, err := svc.ForwardDeepSeekTools(context.Background(), c, account, nil, DeepSeekToolsPathSearch, "", []byte(`{"query":"x"}`))

	require.NoError(t, err)
	require.Nil(t, result, "a failed upstream call must not bill")
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "between 1 and 50")
}
