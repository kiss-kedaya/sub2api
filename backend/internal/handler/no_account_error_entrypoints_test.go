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

// 端到端：Gemini v1beta 与 OpenAI 网关入口选不到账号时，响应体里不含任何调度内部诊断，
// 状态码保持 503 / 404；404 的「可用模型」只列分组白名单可见的别名。

func TestGeminiV1BetaNoAccountResponsesHideSchedulerInternals(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID := int64(7401)
	group := &service.Group{ID: groupID, Hydrated: true, Platform: service.PlatformGemini, Status: service.StatusActive}

	for _, tc := range []struct {
		name             string
		account          service.Account
		wantStatus       int
		wantGoogleStatus string
	}{
		{
			name: "nothing schedulable is a 503",
			account: service.Account{
				ID: 7402, Platform: service.PlatformGemini, Type: service.AccountTypeAPIKey,
				Status: service.StatusActive, Schedulable: false, Concurrency: 1,
			},
			wantStatus: http.StatusServiceUnavailable, wantGoogleStatus: "UNAVAILABLE",
		},
		{
			name: "no account maps the model is a 404",
			account: service.Account{
				ID: 7403, Platform: service.PlatformGemini, Type: service.AccountTypeAPIKey,
				Status: service.StatusActive, Schedulable: true, Concurrency: 1,
				Credentials: map[string]any{"model_mapping": map[string]any{"gemini-other": "gemini-other"}},
			},
			wantStatus: http.StatusNotFound, wantGoogleStatus: "NOT_FOUND",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.account.AccountGroups = []service.AccountGroup{{AccountID: tc.account.ID, GroupID: groupID}}
			h, cleanup := newTestGatewayHandler(t, group, []*service.Account{&tc.account})
			defer cleanup()

			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			req := httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-2.5-pro:generateContent",
				bytes.NewBufferString(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`))
			req.Header.Set("Content-Type", "application/json")
			c.Request = req.WithContext(context.WithValue(req.Context(), ctxkey.Group, group))
			c.Params = gin.Params{{Key: "modelAction", Value: "/gemini-2.5-pro:generateContent"}}
			apiKey := &service.APIKey{
				ID: 7404, UserID: 7405, GroupID: &groupID, Group: group, Status: service.StatusActive,
				User: &service.User{ID: 7405, Concurrency: 10, Balance: 100},
			}
			c.Set(string(middleware.ContextKeyAPIKey), apiKey)
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: apiKey.UserID, Concurrency: 10})

			h.GeminiV1BetaModels(c)

			require.Equal(t, tc.wantStatus, rec.Code, rec.Body.String())
			var payload struct {
				Error struct {
					Code    int    `json:"code"`
					Message string `json:"message"`
					Status  string `json:"status"`
				} `json:"error"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload), rec.Body.String())
			require.Equal(t, tc.wantStatus, payload.Error.Code)
			require.Equal(t, tc.wantGoogleStatus, payload.Error.Status)
			require.NotEmpty(t, payload.Error.Message)
			if tc.wantStatus == http.StatusServiceUnavailable {
				require.Equal(t, noAvailableAccountsMessage, payload.Error.Message)
			}
			requireNoSchedulerInternals(t, rec.Body.String())
		})
	}
}

func TestOpenAIGatewayNoAccountResponsesHideSchedulerInternals(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID := int64(7411)
	entries := []struct {
		name string
		path string
		body string
		call func(*OpenAIGatewayHandler, *gin.Context)
	}{
		{"responses", "/v1/responses", `{"model":"gpt-5.1","stream":false,"input":"hello"}`,
			func(h *OpenAIGatewayHandler, c *gin.Context) { h.Responses(c) }},
		{"chat completions", "/v1/chat/completions", `{"model":"gpt-5.1","messages":[{"role":"user","content":"hello"}]}`,
			func(h *OpenAIGatewayHandler, c *gin.Context) { h.ChatCompletions(c) }},
		{"messages", "/v1/messages", `{"model":"gpt-5.1","max_tokens":16,"messages":[{"role":"user","content":"hello"}]}`,
			func(h *OpenAIGatewayHandler, c *gin.Context) { h.Messages(c) }},
	}
	unschedulable := service.Account{
		ID: 7412, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Status: service.StatusActive,
		Schedulable: false, Concurrency: 1, Credentials: map[string]any{"access_token": "t-1"},
	}
	// 账号只映射了别名 gpt-other：请求 gpt-5.1 时分组里没有账号能服务它。
	mappedOther := service.Account{
		ID: 7413, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Status: service.StatusActive,
		Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{"access_token": "t-2", "model_mapping": map[string]any{"gpt-other": "gpt-other"}},
	}

	for _, entry := range entries {
		for _, tc := range []struct {
			name      string
			account   service.Account
			allowlist service.GroupModelAllowlist
			check     func(t *testing.T, status int, body string)
		}{
			{
				name: "nothing schedulable", account: unschedulable,
				check: func(t *testing.T, status int, body string) {
					require.Equal(t, http.StatusServiceUnavailable, status, body)
				},
			},
			{
				name: "model not mapped, no allowlist: the alias is listed", account: mappedOther,
				check: func(t *testing.T, status int, body string) {
					require.Equal(t, http.StatusNotFound, status, body)
					require.Contains(t, body, `"type":"model_not_found"`)
					require.Contains(t, body, "gpt-other")
				},
			},
			{
				name: "model not mapped, allowlist hides the alias: it is not listed", account: mappedOther,
				allowlist: service.GroupModelAllowlist{Enabled: true, Models: []string{"gpt-allowed"}},
				check: func(t *testing.T, status int, body string) {
					require.Equal(t, http.StatusNotFound, status, body)
					require.Contains(t, body, `"type":"model_not_found"`)
					require.NotContains(t, body, "gpt-other", "an alias the allowlist hides must not leak through the 404")
				},
			},
		} {
			t.Run(entry.name+"/"+tc.name, func(t *testing.T) {
				h, _ := newOpenAIImagesSizeRoutingTestHandler(t, []service.Account{tc.account}, nil)
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				req := httptest.NewRequest(http.MethodPost, entry.path, bytes.NewBufferString(entry.body))
				req.Header.Set("Content-Type", "application/json")
				c.Request = req
				c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{
					ID: 7414, GroupID: &groupID, User: &service.User{ID: 7415},
					Group: &service.Group{
						ID: groupID, Platform: service.PlatformOpenAI, Status: service.StatusActive, Hydrated: true,
						AllowMessagesDispatch: true, ModelAllowlist: tc.allowlist,
					},
				})
				c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 7415, Concurrency: 0})

				entry.call(h, c)

				body := rec.Body.String()
				tc.check(t, rec.Code, body)
				requireNoSchedulerInternals(t, body)
			})
		}
	}
}
