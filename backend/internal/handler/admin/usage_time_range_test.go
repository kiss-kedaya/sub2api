package admin

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestUsageDateBoundarySecondsAndLegacyDates(t *testing.T) {
	for _, tc := range []struct {
		value, tz, want string
		end             bool
	}{
		{"2026-10-03T04:43:28", "Asia/Shanghai", "2026-10-02T20:43:28Z", false},
		{"2026-10-03T05:02:08", "Asia/Shanghai", "2026-10-02T21:02:09Z", true},
		{"2026-10-03T05:02:08+08:00", "UTC", "2026-10-02T21:02:09Z", true},
		{"2026-10-03", "Asia/Shanghai", "2026-10-02T16:00:00Z", false},
		{"2026-10-03", "Asia/Shanghai", "2026-10-03T16:00:00Z", true},
		{"2026-03-08", "America/New_York", "2026-03-09T04:00:00Z", true},
	} {
		t.Run(tc.value+tc.want, func(t *testing.T) {
			got, err := parseUsageDateBoundary(tc.value, tc.tz, tc.end)
			require.NoError(t, err)
			require.Equal(t, tc.want, got.UTC().Format(time.RFC3339))
		})
	}
}

func TestAdminUsageSecondPrecisionReachesRepository(t *testing.T) {
	for _, endpoint := range []string{"/admin/usage", "/admin/usage/stats"} {
		t.Run(endpoint, func(t *testing.T) {
			usageStatsCache = newSnapshotCache(time.Second)
			repo := &adminUsageRepoCapture{}
			router := newAdminUsageRequestTypeTestRouter(repo)
			q := url.Values{"start_date": {"2026-10-03T04:43:28"}, "end_date": {"2026-10-03T05:02:08"}, "timezone": {"Asia/Shanghai"}}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, endpoint+"?"+q.Encode(), nil))
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			filters := repo.listFilters
			if endpoint == "/admin/usage/stats" {
				filters = repo.statsFilters
			}
			require.NotNil(t, filters.StartTime)
			require.NotNil(t, filters.EndTime)
			require.Equal(t, "2026-10-02T20:43:28Z", filters.StartTime.UTC().Format(time.RFC3339))
			require.Equal(t, "2026-10-02T21:02:09Z", filters.EndTime.UTC().Format(time.RFC3339))
		})
	}
}

func TestAdminUsageRejectsInvalidTimeRanges(t *testing.T) {
	for _, endpoint := range []string{"/admin/usage", "/admin/usage/stats"} {
		for _, start := range []string{"2026-10-03T25:00:00", "2026-10-03T05:02:09", "not-a-date"} {
			repo := &adminUsageRepoCapture{}
			router := newAdminUsageRequestTypeTestRouter(repo)
			q := url.Values{"start_date": {start}, "end_date": {"2026-10-03T05:02:08"}}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, endpoint+"?"+q.Encode(), nil))
			require.Equal(t, http.StatusBadRequest, w.Code, endpoint+start)
		}
	}
}

func TestDashboardTimeRangeKeepsSeconds(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/?start_date=2026-10-03T04:43:28&end_date=2026-10-03T05:02:08&timezone=Asia%2FShanghai", nil)
	start, end := parseTimeRange(c)
	require.Equal(t, "2026-10-02T20:43:28Z", start.UTC().Format(time.RFC3339))
	require.Equal(t, "2026-10-02T21:02:09Z", end.UTC().Format(time.RFC3339))
}

func TestUsageCleanupKeepsSelectedSecondsWithoutWideningToDays(t *testing.T) {
	repo := &cleanupRepoStub{}
	cfg := &config.Config{UsageCleanup: config.UsageCleanupConfig{Enabled: true, MaxRangeDays: 31}}
	router := setupCleanupRouter(service.NewUsageCleanupService(repo, nil, nil, cfg), 99)
	body := []byte(`{"start_date":"2026-10-03T04:43:28","end_date":"2026-10-03T05:02:08","timezone":"Asia/Shanghai"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/usage/cleanup-tasks", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Len(t, repo.created, 1)
	require.Equal(t, "2026-10-02T20:43:28Z", repo.created[0].Filters.StartTime.UTC().Format(time.RFC3339))
	require.Equal(t, "2026-10-02T21:02:08.999999999Z", repo.created[0].Filters.EndTime.UTC().Format(time.RFC3339Nano))
}
