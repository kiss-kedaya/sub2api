//go:build unit

package handler

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// C-08 端到端：智能路由 Key 的第一个候选组因模型白名单被跳过，路由层把跳过摘要追加到
// 选号错误文本末尾；第二个候选组的真实选号计数仍决定响应是 404 / 429，而不是退化成
// 可重试的 503（客户端会无效重试，运维也会把它归因成路由容量故障）。
func TestMessagesSmartRouteSkippedGroupKeepsVerdict(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const model = "claude-x"
	cooldown := map[string]any{"model_rate_limits": map[string]any{
		model: map[string]any{"rate_limit_reset_at": time.Now().Add(2 * time.Minute).UTC().Format(time.RFC3339)},
	}}

	for _, tc := range []struct {
		name       string
		extra      map[string]any
		mapping    map[string]any
		wantStatus int
		wantType   string
		wantText   string
	}{
		{
			name:       "no account in the served group maps the model",
			mapping:    map[string]any{"claude-other": "claude-other"},
			wantStatus: http.StatusNotFound, wantType: "model_not_found",
			wantText: "分组内没有账号支持该模型，请更换模型后重试",
		},
		{
			name:       "every account in the served group cools down for the model",
			extra:      cooldown,
			wantStatus: http.StatusTooManyRequests, wantType: "rate_limit_error",
			wantText: noAvailableAccountsRateLimitedMessage,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			skippedID, servedID := int64(7501), int64(7502)
			skipped := &service.Group{
				ID: skippedID, Hydrated: true, Platform: service.PlatformAnthropic, Status: service.StatusActive,
				ModelAllowlist: service.GroupModelAllowlist{Enabled: true, Models: []string{"only-other-model"}},
			}
			served := &service.Group{
				ID: servedID, Hydrated: true, Platform: service.PlatformAnthropic, Status: service.StatusActive,
				ModelAllowlist: service.GroupModelAllowlist{Enabled: true, Models: []string{model}},
			}
			account := service.Account{
				ID: 7503, Platform: service.PlatformAnthropic, Type: service.AccountTypeAPIKey, Status: service.StatusActive,
				Schedulable: true, Concurrency: 5, Extra: tc.extra,
				AccountGroups: []service.AccountGroup{{AccountID: 7503, GroupID: servedID}}, GroupIDs: []int64{servedID},
			}
			if tc.mapping != nil {
				account.Credentials = map[string]any{"model_mapping": tc.mapping}
			}
			h, cleanup := newTestGatewayHandler(t, skipped, []*service.Account{&account})
			defer cleanup()
			// 调度快照只认请求上下文里携带的分组策略：把第二个候选组也播种进去。
			h.gatewayService.PrepareSchedulerRequestContext(context.WithValue(context.Background(), ctxkey.Group, served))

			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			req := httptest.NewRequest(http.MethodPost, "/v1/messages",
				bytes.NewBufferString(`{"model":"`+model+`","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`))
			req.Header.Set("Content-Type", "application/json")
			c.Request = req.WithContext(context.WithValue(req.Context(), ctxkey.Group, skipped))
			apiKey := &service.APIKey{
				ID: 7504, UserID: 7505, GroupID: &skippedID, Group: skipped, RouteGroupIDs: []int64{skippedID, servedID},
				Status: service.StatusActive, User: &service.User{ID: 7505, Concurrency: 10, Balance: 100},
			}
			c.Set(string(middleware.ContextKeyAPIKey), apiKey)
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: apiKey.UserID, Concurrency: 10})

			h.Messages(c)

			body := rec.Body.String()
			require.Equal(t, tc.wantStatus, rec.Code, body)
			require.Contains(t, body, `"type":"`+tc.wantType+`"`)
			require.Contains(t, body, tc.wantText)
			requireNoSchedulerInternals(t, body)
		})
	}
}
