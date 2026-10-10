package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/gemini"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGeminiNativeModelsUsesAccountMappings(t *testing.T) {
	for _, tt := range []struct {
		name                     string
		forced, mixed, allowlist bool
		status                   int
	}{
		{"mixed", false, true, false, 200},
		{"mixed allowlist", false, true, true, 200},
		{"disabled mixed", false, false, false, 200},
		{"forced without mixed opt-in", true, false, false, 200},
		{"forced allowlist", true, false, true, 200},
	} {
		t.Run(tt.name, func(t *testing.T) {
			groupID := int64(45)
			repo := &geminiAllowlistAccountRepoStub{gatewayModelsAccountRepoStub: gatewayModelsAccountRepoStub{byGroup: map[int64][]service.Account{
				groupID: {{ID: 1, Platform: service.PlatformAntigravity,
					Extra:       map[string]any{"mixed_scheduling": tt.mixed},
					Credentials: map[string]any{"model_mapping": map[string]any{"gemini-synced-custom": "gemini-3.8-flash-high", "claude-custom": "claude-sonnet-4-6"}}}},
			}}}
			h := &GatewayHandler{geminiCompatService: service.NewGeminiMessagesCompatService(repo, nil, nil, nil, nil, nil, nil, nil, nil)}
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodGet, "/v1beta/models", nil)
			c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{GroupID: &groupID, Group: &service.Group{ID: groupID, Platform: service.PlatformGemini,
				ModelAllowlist: service.GroupModelAllowlist{Enabled: tt.allowlist, Models: []string{"gemini-synced-custom"}},
			}})
			if tt.forced {
				c.Set(string(middleware.ContextKeyForcePlatform), service.PlatformAntigravity)
			}
			h.GeminiV1BetaListModels(c)
			require.Equal(t, tt.status, rec.Code, rec.Body.String())
			if tt.status != 200 {
				return
			}
			var got gemini.ModelsListResponse
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
			names := []string{}
			for _, model := range got.Models {
				names = append(names, model.Name)
				require.Contains(t, model.SupportedGenerationMethods, "generateContent")
			}
			if tt.mixed || tt.forced {
				require.Contains(t, names, "models/gemini-synced-custom")
			} else {
				require.NotContains(t, names, "models/gemini-synced-custom")
				// 本地目录只来自账号真实映射；没有映射就是空目录，
				// 不再补内置全量目录里那批模型。
				require.Empty(t, names)
			}
			require.NotContains(t, names, "models/claude-custom")
			if tt.allowlist {
				require.Equal(t, []string{"models/gemini-synced-custom"}, names)
			}
		})
	}
}

func TestAppendUpstreamGeminiModelsPreservesMetadata(t *testing.T) {
	body := []byte(`{"models":[{"name":"models/gemini-native","inputTokenLimit":123,"custom":{"a":true}}],"nextPageToken":"next","other":42}`)
	extra := []gemini.Model{gemini.FallbackModel("gemini-native"), gemini.FallbackModel("gemini-synced-custom"), gemini.FallbackModel("gemini-synced-custom")}
	merged, ok := appendUpstreamGeminiModels(body, extra)
	require.True(t, ok)
	require.JSONEq(t, `{"models":[{"name":"models/gemini-native","inputTokenLimit":123,"custom":{"a":true}},{"name":"models/gemini-synced-custom","supportedGenerationMethods":["generateContent","streamGenerateContent","countTokens"]}],"nextPageToken":"next","other":42}`, string(merged))
	filtered, dropped, ok := filterUpstreamGeminiModelsBody(merged, service.GroupModelAllowlist{Enabled: true, Models: []string{"gemini-synced-*"}})
	require.True(t, ok)
	require.True(t, dropped)
	require.NotContains(t, string(filtered), "gemini-native")
	require.Contains(t, string(filtered), "gemini-synced-custom")
	for _, invalid := range []string{`null`, `not-json`, `{"error":"bad"}`, `{"models":{}}`} {
		got, ok := appendUpstreamGeminiModels([]byte(invalid), extra)
		require.False(t, ok)
		require.Equal(t, invalid, string(got))
	}
}

// Exercise the real handler's native-upstream branch, including scope fallback.
type geminiMixedModelsUpstream struct {
	service.HTTPUpstream
	status int
	body   string
}

func (u *geminiMixedModelsUpstream) Do(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return &http.Response{StatusCode: u.status, Header: http.Header{"Content-Type": {"application/json"}, "X-Request-Id": {"native-id"}}, Body: io.NopCloser(strings.NewReader(u.body))}, nil
}
func TestGeminiNativeModelsMergesNativeUpstream(t *testing.T) {
	for _, tt := range []struct {
		name string
		// expectPassthrough 为 true 时断言上游响应被原样透传（状态码 + 响应体），
		// 不再合成 200 目录，也不再出现内置全量目录条目。
		expectPassthrough bool
		status            int
		body              string
	}{
		{"native", false, 200, `{"models":[{"name":"models/gemini-native","inputTokenLimit":123}],"nextPageToken":"next"}`},
		{"scope fallback", true, 403, `{"error":"insufficient authentication scopes"}`},
		{"upstream error", true, 429, `{"error":"rate limited"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			id := int64(46)
			repo := &geminiAllowlistAccountRepoStub{gatewayModelsAccountRepoStub: gatewayModelsAccountRepoStub{byGroup: map[int64][]service.Account{id: {
				{ID: 1, Platform: service.PlatformGemini, Type: service.AccountTypeAPIKey, Credentials: map[string]any{"api_key": "test"}},
				{ID: 2, Platform: service.PlatformAntigravity, Extra: map[string]any{"mixed_scheduling": true}, Credentials: map[string]any{"model_mapping": map[string]any{"gemini-synced-custom": "gemini-3.8-flash-high"}}},
			}}}}
			upstream := &geminiMixedModelsUpstream{status: tt.status, body: tt.body}
			h := &GatewayHandler{geminiCompatService: service.NewGeminiMessagesCompatService(repo, nil, nil, nil, nil, nil, upstream, nil, &config.Config{})}
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodGet, "/v1beta/models", nil)
			c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{GroupID: &id, Group: &service.Group{ID: id, Platform: service.PlatformGemini}})
			h.GeminiV1BetaListModels(c)
			// 内置全量目录永远不再出现在响应里。
			require.NotContains(t, rec.Body.String(), "models/gemini-2.5-pro")
			if tt.expectPassthrough {
				require.Equal(t, tt.status, rec.Code, rec.Body.String())
				require.JSONEq(t, tt.body, rec.Body.String())
				require.Equal(t, "native-id", rec.Header().Get("X-Request-Id"))
				return
			}
			require.Equal(t, 200, rec.Code, rec.Body.String())
			require.Contains(t, rec.Body.String(), "models/gemini-native")
			require.Contains(t, rec.Body.String(), "models/gemini-synced-custom")
			require.Contains(t, rec.Body.String(), `"inputTokenLimit":123`)
			require.Contains(t, rec.Body.String(), `"nextPageToken":"next"`)
			require.Equal(t, "native-id", rec.Header().Get("X-Request-Id"))
		})
	}
}

type geminiMappingFailureRepo struct {
	geminiAllowlistAccountRepoStub
}

func (r *geminiMappingFailureRepo) ListSchedulableByGroupIDAndPlatforms(_ context.Context, _ int64, _ []string) ([]service.Account, error) {
	return nil, errors.New("catalog temporarily unavailable")
}

// 目录来源不可用时只回本地真实映射（这里为空），不再补内置全量目录；
// 仍然返回 200，避免 Gemini SDK 在 generateContent 前的 sync GET 直接报 "sync failed"。
func TestGeminiNativeModelsKeepsLocalFallback(t *testing.T) {
	for _, compat := range []*service.GeminiMessagesCompatService{
		nil,
		service.NewGeminiMessagesCompatService(&geminiMappingFailureRepo{}, nil, nil, nil, nil, nil, nil, nil, nil),
	} {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodGet, "/v1beta/models", nil)
		id := int64(45)
		c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{GroupID: &id, Group: &service.Group{ID: id, Platform: service.PlatformGemini}})
		(&GatewayHandler{geminiCompatService: compat}).GeminiV1BetaListModels(c)
		require.Equal(t, http.StatusOK, rec.Code)
		require.NotContains(t, rec.Body.String(), "models/gemini-2.5-pro")
		var got gemini.ModelsListResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
		require.Empty(t, got.Models)
	}
}

func TestGeminiNativeModelsIncludesSmartRoutingMappings(t *testing.T) {
	for _, openAIProtocol := range []bool{false, true} {
		repo := &geminiAllowlistAccountRepoStub{gatewayModelsAccountRepoStub: gatewayModelsAccountRepoStub{byGroup: map[int64][]service.Account{
			45: {{ID: 1, Platform: service.PlatformAntigravity, Extra: map[string]any{"mixed_scheduling": true}, Credentials: map[string]any{"model_mapping": map[string]any{"gemini-shared": "gemini-2.5-pro"}}}},
			46: {{ID: 2, Platform: service.PlatformAntigravity, Extra: map[string]any{"mixed_scheduling": true}, Credentials: map[string]any{"model_mapping": map[string]any{"gemini-shared": "gemini-2.5-pro", "gemini-secondary": "gemini-2.5-flash"}}}},
			47: {{ID: 3, Platform: service.PlatformAntigravity, Extra: map[string]any{"mixed_scheduling": true}, Credentials: map[string]any{"model_mapping": map[string]any{"gemini-unbound": "gemini-2.5-pro"}}}},
		}}}
		if openAIProtocol {
			repo.byGroup[45] = append(repo.byGroup[45], service.Account{ID: 4, Platform: service.PlatformGemini, Type: service.AccountTypeAPIKey, Credentials: map[string]any{"api_protocol": service.APIProtocolResponses}})
		}
		h := &GatewayHandler{geminiCompatService: service.NewGeminiMessagesCompatService(repo, nil, nil, nil, nil, nil, nil, nil, nil)}
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodGet, "/v1beta/models", nil)
		id := int64(45)
		c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{GroupID: &id, RouteGroupIDs: []int64{45, 46, 46}, Group: &service.Group{ID: id, Platform: service.PlatformGemini}})
		h.GeminiV1BetaListModels(c)
		require.Equal(t, http.StatusOK, rec.Code)
		var got gemini.ModelsListResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
		names := []string{}
		for _, model := range got.Models {
			names = append(names, model.Name)
		}
		require.Contains(t, names, "models/gemini-secondary")
		require.NotContains(t, names, "models/gemini-unbound")
		require.Equal(t, 1, strings.Count(rec.Body.String(), `"models/gemini-shared"`))
	}
}
