//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// overviewUsageRepoStub 实现批量 24h 概览查询（窄接口），记录查询参数。
type overviewUsageRepoStub struct {
	service.UsageLogRepository

	stats map[int64]usagestats.APIKeyUsage24h
	since time.Time
	ids   []int64
}

func (s *overviewUsageRepoStub) GetAPIKeysUsage24h(_ context.Context, apiKeyIDs []int64, since time.Time) (map[int64]usagestats.APIKeyUsage24h, error) {
	s.ids = append([]int64(nil), apiKeyIDs...)
	s.since = since
	out := make(map[int64]usagestats.APIKeyUsage24h, len(apiKeyIDs))
	for _, id := range apiKeyIDs {
		if item, ok := s.stats[id]; ok {
			out[id] = item
		}
	}
	return out, nil
}

// overviewFallbackUsageRepoStub 不实现批量窄接口，用来验证逐 key 回退路径。
type overviewFallbackUsageRepoStub struct {
	service.UsageLogRepository

	calls []int64
}

func (s *overviewFallbackUsageRepoStub) GetAPIKeyStatsAggregated(_ context.Context, apiKeyID int64, startTime, endTime time.Time) (*usagestats.UsageStats, error) {
	s.calls = append(s.calls, apiKeyID)
	return &usagestats.UsageStats{TotalRequests: apiKeyID * 10, TotalActualCost: float64(apiKeyID) / 10}, nil
}

// overviewAPIKeyRepoStub 提供仅有的所有权校验。
type overviewAPIKeyRepoStub struct {
	service.APIKeyRepository

	owners map[int64]int64
}

func (s *overviewAPIKeyRepoStub) VerifyOwnership(_ context.Context, userID int64, ids []int64) ([]int64, error) {
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if owner, ok := s.owners[id]; ok && owner == userID {
			out = append(out, id)
		}
	}
	return out, nil
}

func newOverviewTestRouter(t *testing.T, usageRepo service.UsageLogRepository, owners map[int64]int64) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	usageSvc := service.NewUsageService(usageRepo, nil, nil, nil)
	apiKeySvc := service.NewAPIKeyService(&overviewAPIKeyRepoStub{owners: owners}, nil, nil, nil, nil, nil, nil)
	h := NewUsageHandler(usageSvc, apiKeySvc, nil, nil)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 7})
		c.Next()
	})
	router.GET("/keys/overview", h.GetAPIKeysOverview)
	return router
}

type overviewResponse struct {
	Code int `json:"code"`
	Data struct {
		Items []struct {
			ID          int64   `json:"id"`
			Requests24h int64   `json:"requests_24h"`
			Cost24h     float64 `json:"cost_24h"`
		} `json:"items"`
		WindowHours int `json:"window_hours"`
	} `json:"data"`
}

func TestGetAPIKeysOverviewReturnsBatch24hStats(t *testing.T) {
	usageRepo := &overviewUsageRepoStub{stats: map[int64]usagestats.APIKeyUsage24h{
		1: {Requests: 12, Cost: 0.25},
		2: {Requests: 3, Cost: 0.01},
	}}
	router := newOverviewTestRouter(t, usageRepo, map[int64]int64{1: 7, 2: 7})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/keys/overview?ids=2,1,2", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	var body overviewResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, 24, body.Data.WindowHours)
	require.Len(t, body.Data.Items, 2, "duplicate ids must be collapsed")
	// 保留请求顺序（前端按 id 合并回行）。
	require.Equal(t, int64(2), body.Data.Items[0].ID)
	require.Equal(t, int64(1), body.Data.Items[1].ID)
	require.EqualValues(t, 3, body.Data.Items[0].Requests24h)
	require.InDelta(t, 0.25, body.Data.Items[1].Cost24h, 1e-9)
	require.Len(t, usageRepo.ids, 2)
	// 一次查询只带一次窗口起点，且必须是 24h 前。
	require.WithinDuration(t, time.Now().Add(-24*time.Hour), usageRepo.since, 30*time.Second)
}

func TestGetAPIKeysOverviewRejectsForeignIDWith404(t *testing.T) {
	usageRepo := &overviewUsageRepoStub{stats: map[int64]usagestats.APIKeyUsage24h{1: {Requests: 1}}}
	router := newOverviewTestRouter(t, usageRepo, map[int64]int64{1: 7, 3: 99})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/keys/overview?ids=1,3", nil))
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Nil(t, usageRepo.ids, "ownership must be fully verified before the aggregate query runs")
}

func TestGetAPIKeysOverviewRejectsBadIDs(t *testing.T) {
	router := newOverviewTestRouter(t, &overviewUsageRepoStub{}, map[int64]int64{1: 7})
	cases := []string{
		"/keys/overview",
		"/keys/overview?ids=",
		"/keys/overview?ids=1,abc",
		"/keys/overview?ids=0",
		"/keys/overview?ids=" + longIDList(101),
	}
	for _, path := range cases {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusBadRequest, rec.Code, "path %q must be rejected", path)
	}
}

func longIDList(n int) string {
	ids := make([]string, 0, n)
	for i := 1; i <= n; i++ {
		ids = append(ids, strconv.Itoa(i))
	}
	return strings.Join(ids, ",")
}

func TestGetAPIKeysOverviewFallsBackToPerKeyAggregate(t *testing.T) {
	repo := &overviewFallbackUsageRepoStub{}
	router := newOverviewTestRouter(t, repo, map[int64]int64{1: 7, 2: 7})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/keys/overview?ids=1,2", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	var body overviewResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Data.Items, 2)
	require.EqualValues(t, 10, body.Data.Items[0].Requests24h)
	require.InDelta(t, 0.2, body.Data.Items[1].Cost24h, 1e-9)
	require.Equal(t, []int64{1, 2}, repo.calls)
}
