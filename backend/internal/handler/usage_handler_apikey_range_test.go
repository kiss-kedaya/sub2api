package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// apiKeyUsageRangeRepoStub implements only the two repository calls used by
// GET /user/api-keys/:id/usage. Requests is derived from the window length so
// tests can tell the "today" aggregate apart from the requested range.
type apiKeyUsageRangeRepoStub struct {
	service.UsageLogRepository

	calls       int
	startTimes  []time.Time
	endTimes    []time.Time
	trendStart  time.Time
	trendEnd    time.Time
	trendUserID int64
	trendKeyID  int64
	items       []usagestats.TrendDataPoint
}

func (s *apiKeyUsageRangeRepoStub) GetAPIKeyStatsAggregated(
	_ context.Context, _ int64, startTime, endTime time.Time,
) (*usagestats.UsageStats, error) {
	s.calls++
	s.startTimes = append(s.startTimes, startTime)
	s.endTimes = append(s.endTimes, endTime)
	hours := int64(endTime.Sub(startTime).Hours())
	return &usagestats.UsageStats{
		TotalRequests:            hours,
		TotalInputTokens:         hours * 2,
		TotalOutputTokens:        hours * 3,
		TotalCacheTokens:         hours,
		TotalCacheCreationTokens: hours,
		TotalCacheReadTokens:     0,
		TotalTokens:              hours * 6,
		TotalCost:                float64(hours) / 10,
		TotalActualCost:          float64(hours) / 20,
	}, nil
}

func (s *apiKeyUsageRangeRepoStub) GetUsageTrendWithFilters(
	_ context.Context,
	startTime, endTime time.Time,
	_ string,
	userID, apiKeyID, _, _ int64,
	_ string,
	_ *int16,
	_ *bool,
	_ *int8,
) ([]usagestats.TrendDataPoint, error) {
	s.trendStart = startTime
	s.trendEnd = endTime
	s.trendUserID = userID
	s.trendKeyID = apiKeyID
	return s.items, nil
}

func newAPIKeyUsageTestRouter(repo *apiKeyUsageRangeRepoStub, keys map[int64]*service.APIKey, userID int64) *gin.Engine {
	gin.SetMode(gin.TestMode)
	usageSvc := service.NewUsageService(repo, nil, nil, nil)
	apiKeyRepo := &dailyUsageAPIKeyRepoStub{keys: keys}
	apiKeySvc := service.NewAPIKeyService(apiKeyRepo, nil, nil, nil, nil, nil, nil)
	h := NewUsageHandler(usageSvc, apiKeySvc, nil, nil)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: userID})
		c.Next()
	})
	router.GET("/user/api-keys/:id/usage", h.GetMyAPIKeyUsage)
	return router
}

type apiKeyUsageResponse struct {
	Code int `json:"code"`
	Data struct {
		APIKeyID  int64                              `json:"api_key_id"`
		StartDate string                             `json:"start_date"`
		EndDate   string                             `json:"end_date"`
		Today     usagestats.APIKeyUsageSummary      `json:"today"`
		Range     usagestats.APIKeyUsageSummary      `json:"range"`
		Items     []usagestats.APIKeyDailyUsagePoint `json:"items"`
	} `json:"data"`
}

func TestGetMyAPIKeyUsageRejectsCrossUserAccess(t *testing.T) {
	repo := &apiKeyUsageRangeRepoStub{}
	router := newAPIKeyUsageTestRouter(repo, map[int64]*service.APIKey{
		7: {ID: 7, UserID: 99, Status: service.StatusAPIKeyActive},
	}, 42)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/user/api-keys/7/usage?days=7", nil))

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Zero(t, repo.calls)
}

func TestGetMyAPIKeyUsageRejectsInvalidRanges(t *testing.T) {
	cases := []string{
		"/user/api-keys/7/usage?days=0",
		"/user/api-keys/7/usage?days=91",
		"/user/api-keys/7/usage?start_date=2026-02-01&end_date=2026-01-01",
		// 91 inclusive days.
		"/user/api-keys/7/usage?start_date=2026-01-01&end_date=2026-04-01",
		"/user/api-keys/7/usage?start_date=not-a-date",
	}
	for _, path := range cases {
		t.Run(path, func(t *testing.T) {
			repo := &apiKeyUsageRangeRepoStub{}
			router := newAPIKeyUsageTestRouter(repo, map[int64]*service.APIKey{
				7: {ID: 7, UserID: 42, Status: service.StatusAPIKeyActive},
			}, 42)

			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

			require.Equal(t, http.StatusBadRequest, rec.Code)
			require.Zero(t, repo.calls)
		})
	}
}

func TestGetMyAPIKeyUsageSeparatesTodayFromRange(t *testing.T) {
	repo := &apiKeyUsageRangeRepoStub{
		items: []usagestats.TrendDataPoint{{
			Date:                "2026-05-19",
			Requests:            3,
			InputTokens:         10,
			OutputTokens:        20,
			CacheReadTokens:     6,
			CacheCreationTokens: 4,
			TotalTokens:         40,
			Cost:                0.5,
			ActualCost:          0.4,
		}},
	}
	router := newAPIKeyUsageTestRouter(repo, map[int64]*service.APIKey{
		7: {ID: 7, UserID: 42, Status: service.StatusAPIKeyActive},
	}, 42)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/user/api-keys/7/usage?days=5&timezone=Asia/Shanghai", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 2, repo.calls, "one aggregate for the range, one for today")

	var got apiKeyUsageResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, int64(7), got.Data.APIKeyID)

	todayStart := timezone.StartOfDayInUserLocation(timezone.NowInUserLocation("Asia/Shanghai"), "Asia/Shanghai")
	require.Equal(t, todayStart.Format("2006-01-02"), got.Data.Today.Date)
	require.Equal(t, int64(24), got.Data.Today.Requests, "today block covers exactly one day")
	require.Equal(t, 24*5, int(got.Data.Range.Requests), "range block covers the requested five days")
	require.Equal(t, todayStart.AddDate(0, 0, -4).Format("2006-01-02"), got.Data.StartDate)
	require.Equal(t, todayStart.Format("2006-01-02"), got.Data.EndDate)

	// Trend/detail query stays user scoped.
	require.Equal(t, int64(42), repo.trendUserID)
	require.Equal(t, int64(7), repo.trendKeyID)
	require.True(t, repo.trendStart.Before(repo.trendEnd))
	require.Len(t, got.Data.Items, 1)
	require.Equal(t, "2026-05-19", got.Data.Items[0].Date)
}

func TestGetMyAPIKeyUsageDefaultsToThirtyDays(t *testing.T) {
	repo := &apiKeyUsageRangeRepoStub{}
	router := newAPIKeyUsageTestRouter(repo, map[int64]*service.APIKey{
		7: {ID: 7, UserID: 42, Status: service.StatusAPIKeyActive},
	}, 42)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/user/api-keys/7/usage", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	var got apiKeyUsageResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, int64(24*30), got.Data.Range.Requests)
}

// The payload must not leak request-level or upstream details even if the
// underlying aggregate gains new fields later.
func TestGetMyAPIKeyUsagePayloadIsSanitized(t *testing.T) {
	repo := &apiKeyUsageRangeRepoStub{}
	router := newAPIKeyUsageTestRouter(repo, map[int64]*service.APIKey{
		7: {ID: 7, UserID: 42, Status: service.StatusAPIKeyActive},
	}, 42)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/user/api-keys/7/usage?days=2", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	var raw map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
	data, ok := raw["data"].(map[string]any)
	require.True(t, ok)

	require.NotContains(t, data["today"], "account_cost")
	require.NotContains(t, data["range"], "account_cost")
	require.NotContains(t, data["range"], "endpoints")
	require.NotContains(t, data["range"], "upstream_endpoints")
	require.NotContains(t, data["range"], "endpoint_paths")
	require.NotContains(t, data, "account_cost")
	require.NotContains(t, data, "upstream_endpoints")

	for _, forbidden := range []string{"ip_address", "request_id", "account_id", "upstream_request_id"} {
		body := rec.Body.String()
		require.NotContains(t, body, forbidden)
	}
}
