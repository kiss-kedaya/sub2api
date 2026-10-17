package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/gemini"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Gemini 分组里只放 OpenAI 平台账号时，/v1beta/models 必须仍然列出这些账号
// 经 Gemini→OpenAI 兼容桥能服务的模型；此前只统计 Antigravity / 原生 Gemini
// 账号，导致这种配置下返回空目录，客户端 sync 到 0 个模型。
func TestGeminiV1BetaListModels_ListsOpenAIAccountMappings(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID := int64(4501)
	repo := &geminiAllowlistAccountRepoStub{gatewayModelsAccountRepoStub: gatewayModelsAccountRepoStub{
		byGroup: map[int64][]service.Account{
			groupID: {{
				ID:          91,
				Platform:    service.PlatformOpenAI,
				Type:        service.AccountTypeAPIKey,
				Status:      service.StatusActive,
				Schedulable: true,
				Credentials: map[string]any{"api_key": "sk-openai", "model_mapping": map[string]any{"gemini-3.8-flash": "gpt-5.6"}},
			}},
		},
	}}
	h := newGatewayModelsHandlerForTest(repo)
	h.geminiCompatService = service.NewGeminiMessagesCompatService(repo, nil, nil, nil, nil, nil, nil, nil, nil)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1beta/models", nil)
	c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{
		GroupID: &groupID,
		Group:   &service.Group{ID: groupID, Platform: service.PlatformGemini},
	})

	h.GeminiV1BetaListModels(c)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var got gemini.ModelsListResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	names := make([]string, 0, len(got.Models))
	for _, model := range got.Models {
		names = append(names, model.Name)
	}
	require.Contains(t, names, "models/gemini-3.8-flash",
		"OpenAI 平台账号的 model_mapping 必须出现在 Gemini 分组目录里")
}
