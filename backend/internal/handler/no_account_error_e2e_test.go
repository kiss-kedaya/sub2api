//go:build unit

package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type noAccountEntry struct {
	name string
	path string
	body string
	call func(*GatewayHandler, *gin.Context)
}

func noAccountEntries(model string) []noAccountEntry {
	return []noAccountEntry{
		{
			name: "messages", path: "/v1/messages",
			body: `{"model":"` + model + `","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`,
			call: func(h *GatewayHandler, c *gin.Context) { h.Messages(c) },
		},
		{
			name: "responses", path: "/v1/responses",
			body: `{"model":"` + model + `","input":"hi"}`,
			call: func(h *GatewayHandler, c *gin.Context) { h.Responses(c) },
		},
		{
			name: "chat completions", path: "/v1/chat/completions",
			body: `{"model":"` + model + `","messages":[{"role":"user","content":"hi"}]}`,
			call: func(h *GatewayHandler, c *gin.Context) { h.ChatCompletions(c) },
		},
	}
}

// runNoAccountEntry drives a real gateway entry point against a group whose only
// account cannot serve the request, and returns the recorded response.
func runNoAccountEntry(t *testing.T, entry noAccountEntry, account *service.Account) (*httptest.ResponseRecorder, string, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	groupID := int64(7301)
	group := &service.Group{
		ID: groupID, Hydrated: true, Platform: service.PlatformAnthropic, Status: service.StatusActive,
		CrossProtocolConversionEnabled: true,
	}
	account.AccountGroups = []service.AccountGroup{{AccountID: account.ID, GroupID: groupID}}
	h, cleanup := newTestGatewayHandler(t, group, []*service.Account{account})
	defer cleanup()

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPost, entry.path, bytes.NewBufferString(entry.body))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req.WithContext(context.WithValue(req.Context(), ctxkey.Group, group))
	apiKey := &service.APIKey{
		ID: 7302, UserID: 7303, GroupID: &groupID, Group: group, Status: service.StatusActive,
		User: &service.User{ID: 7303, Concurrency: 10, Balance: 100},
	}
	c.Set(string(middleware.ContextKeyAPIKey), apiKey)
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: apiKey.UserID, Concurrency: 10})

	entry.call(h, c)

	var payload struct {
		Error struct {
			Type    string `json:"type"`
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload), rec.Body.String())
	return rec, payload.Error.Code, payload.Error.Message
}

// 端到端：真实入口选不到账号时，客户端只拿到固定文案；状态码与结构不变。
func TestGatewayEntrypoints_NoAccount503HidesSchedulerInternals(t *testing.T) {
	for _, entry := range noAccountEntries("claude-sonnet-4-5") {
		t.Run(entry.name, func(t *testing.T) {
			account := &service.Account{
				ID: 7304, Platform: service.PlatformAnthropic, Type: service.AccountTypeAPIKey,
				Status: service.StatusActive, Schedulable: false, Concurrency: 1,
			}

			rec, _, message := runNoAccountEntry(t, entry, account)

			require.Equal(t, http.StatusServiceUnavailable, rec.Code, rec.Body.String())
			require.Equal(t, noAvailableAccountsMessage, message)
			requireNoSchedulerInternals(t, rec.Body.String())
		})
	}
}

// 端到端：账号都不支持该模型时仍是 404 + MODEL_NOT_AVAILABLE，文案不带内部计数。
func TestGatewayEntrypoints_ModelNotSupported404HidesSchedulerInternals(t *testing.T) {
	for _, entry := range noAccountEntries("claude-not-mapped") {
		t.Run(entry.name, func(t *testing.T) {
			account := &service.Account{
				ID: 7305, Platform: service.PlatformAnthropic, Type: service.AccountTypeAPIKey,
				Status: service.StatusActive, Schedulable: true, Concurrency: 1,
				Credentials: map[string]any{"model_mapping": map[string]any{"claude-other": "claude-other"}},
			}

			rec, code, message := runNoAccountEntry(t, entry, account)

			require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
			require.Equal(t, modelNotAvailableCode, code)
			// 测试装配没有账号仓库，诊断走不到数据库，落在分类器的兜底 404 文案。
			require.Equal(t, "分组内没有账号支持该模型，请更换模型后重试", message)
			requireNoSchedulerInternals(t, rec.Body.String())
		})
	}
}
