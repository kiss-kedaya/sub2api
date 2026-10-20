package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestUsageUnrestrictedIncludesWeeklyWindowStart(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/usage", nil)

	weeklyWindowStart := time.Date(2026, time.July, 13, 0, 30, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	c.Set(string(middleware.ContextKeySubscription), &service.UserSubscription{
		WeeklyWindowStart: &weeklyWindowStart,
	})

	handler := &GatewayHandler{}
	handler.usageUnrestricted(
		c,
		context.Background(),
		&service.APIKey{Group: &service.Group{
			Name:             "Weekly plan",
			SubscriptionType: service.SubscriptionTypeSubscription,
		}},
		middleware.AuthSubject{},
		nil,
		nil,
		nil,
		nil,
	)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Subscription struct {
			WeeklyWindowStart *time.Time `json:"weekly_window_start"`
		} `json:"subscription"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.NotNil(t, response.Subscription.WeeklyWindowStart)
	require.True(t, weeklyWindowStart.Equal(*response.Subscription.WeeklyWindowStart))
}

// 回归：/v1/usage 的余额分支不允许在 handler 内重新查用户。
// 该 key 的 usage_logs 可能有几十万行，统计查询会把请求拖到客户端（监控探针）
// 主动断开；此时 ctx 已取消，任何再查库都会返回 context canceled 并恒返回 500。
// 修复后用户由调用方在统计之前用 WithoutCancel 取好，这里只做 nil 校验。
func TestUsageUnrestrictedRequiresPreloadedUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/usage", nil)

	// 模拟客户端已断连：ctx 已取消。
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	handler := &GatewayHandler{}
	handler.usageUnrestricted(
		c,
		cancelled,
		&service.APIKey{Group: &service.Group{Name: "Wallet"}},
		middleware.AuthSubject{UserID: 7},
		nil, // 调用方未预加载 → 必须直接报错，不得回落到 ctx 已取消的查询
		nil,
		nil,
		nil,
	)

	require.Equal(t, http.StatusInternalServerError, recorder.Code)
	require.Contains(t, recorder.Body.String(), "Failed to get user info")
}
