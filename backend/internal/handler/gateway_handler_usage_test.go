package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
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

// Exercise the public handler, including request cancellation during statistics.
type usageWalletUserRepo struct {
	service.UserRepository
	events      *[]string
	deadline    time.Time
	hasDeadline bool
}

func (r *usageWalletUserRepo) GetByID(ctx context.Context, id int64) (*service.User, error) {
	*r.events = append(*r.events, "user")
	r.deadline, r.hasDeadline = ctx.Deadline()
	return &service.User{ID: id, Balance: 12.5, TokenVersionResolved: true}, nil
}
func (r *usageWalletUserRepo) GetUserAvatar(context.Context, int64) (*service.UserAvatar, error) {
	return nil, nil
}

type usageWalletStatsRepo struct {
	service.UsageLogRepository
	events *[]string
	cancel context.CancelFunc
}

func (r *usageWalletStatsRepo) GetAPIKeyDashboardStats(ctx context.Context, _ int64) (*usagestats.UserDashboardStats, error) {
	*r.events = append(*r.events, "stats")
	r.cancel()
	return nil, ctx.Err()
}
func (r *usageWalletStatsRepo) GetUsageTrendWithFilters(ctx context.Context, _, _ time.Time, _ string, _, _, _, _ int64, _ string, _ *int16, _ *bool, _ *int8) ([]usagestats.TrendDataPoint, error) {
	return nil, ctx.Err()
}
func (r *usageWalletStatsRepo) GetModelStatsWithFilters(ctx context.Context, _, _ time.Time, _, _, _, _ int64, _ *int16, _ *bool, _ *int8) ([]usagestats.ModelStat, error) {
	return nil, ctx.Err()
}
func TestUsageLoadsWalletBeforeStatsWithBoundedContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	events := []string{}
	repo := &usageWalletUserRepo{events: &events}
	requestCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	statsRepo := &usageWalletStatsRepo{events: &events, cancel: cancel}
	h := &GatewayHandler{userService: service.NewUserService(repo, nil, nil, nil), usageService: service.NewUsageService(statsRepo, nil, nil, nil)}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/usage", nil).WithContext(requestCtx)
	c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 7})
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 11})
	started := time.Now()
	h.Usage(c)
	require.Equal(t, []string{"user", "stats"}, events)
	require.True(t, repo.hasDeadline)
	require.WithinDuration(t, started.Add(5*time.Second), repo.deadline, time.Second)
	require.ErrorIs(t, requestCtx.Err(), context.Canceled)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.JSONEq(t, `{"mode":"unrestricted","isValid":true,"planName":"钱包余额","remaining":12.5,"unit":"USD","balance":12.5}`, recorder.Body.String())
}
