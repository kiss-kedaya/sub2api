package handler

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/repository" //nolint:depguard // 端到端夹具：要真实的仓库（聚合缓存 + 超预算映射）才能证明 handler 层看到的是 429 而不是 500。
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/lib/pq"
	"github.com/redis/go-redis/v9" //nolint:depguard // 端到端夹具：miniredis 客户端，交给真实仓库做聚合缓存。
	"github.com/stretchr/testify/require"
)

// GET /api/v1/usage/dashboard/stats, end to end through the real repository and its cache: when the database cannot
// answer the usage aggregate in its budget (the disk is saturated and the statement is cancelled), the page is told
// to come back in a moment, the answer the usage concurrency gate already gives (429 with Retry-After), and the log
// has no [ERROR] line for it. Production answered 500 and logged every one of them as an error. A statement that
// fails for any other reason is still a 500.
func TestUserDashboardStatsOverBudgetIsBusyNotAServerError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name       string
		dbErr      error
		wantStatus int
		wantLogged bool
	}{
		{"cancelled when our own load budget ran out", &pq.Error{Code: "57014", Message: "canceling statement due to user request"}, http.StatusTooManyRequests, false},
		{"the server's statement timeout", &pq.Error{Code: "57014", Message: "canceling statement due to statement timeout"}, http.StatusTooManyRequests, false},
		{"a deadline", context.DeadlineExceeded, http.StatusTooManyRequests, false},
		{"a broken connection", sql.ErrConnDone, http.StatusInternalServerError, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := miniredis.RunT(t)
			rdb := redis.NewClient(&redis.Options{Addr: server.Addr()})
			t.Cleanup(func() { _ = rdb.Close() })
			db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })

			cfg := &config.Config{UsageStatsCache: config.UsageStatsCacheConfig{Enabled: true, TTL: time.Minute}}
			h := NewUsageHandler(service.NewUsageService(repository.NewUsageLogRepository(nil, db, rdb, cfg), nil, nil, nil), nil, nil, nil)

			// The live key counts, then the first read of the usage aggregate (of the whole answer in the first hour of
			// the local day, of the days that are over after it) fails.
			mock.ExpectQuery(`SELECT COUNT\(\*\) FROM api_keys WHERE user_id = \$1 AND deleted_at IS NULL`).
				WithArgs(int64(42)).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(2)))
			mock.ExpectQuery(`SELECT COUNT\(\*\) FROM api_keys WHERE user_id = \$1 AND status = \$2 AND deleted_at IS NULL`).
				WithArgs(int64(42), service.StatusActive).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(1)))
			mock.ExpectQuery(`FROM usage_dashboard_hourly_users`).WillReturnError(tc.dbErr)

			previous := log.Writer()
			t.Cleanup(func() { log.SetOutput(previous) })
			var logged bytes.Buffer
			log.SetOutput(&logged)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/usage/dashboard/stats", nil)
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
			h.DashboardStats(c)

			require.NoError(t, mock.ExpectationsWereMet())
			require.Equal(t, tc.wantStatus, w.Code)
			var body response.Response
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			if tc.wantStatus == http.StatusTooManyRequests {
				require.Equal(t, "2", w.Header().Get("Retry-After"))
				require.Equal(t, "USAGE_STATS_BUSY", body.Reason)
				require.Equal(t, map[string]string{"retry_after": "2"}, body.Metadata)
			}
			if tc.wantLogged {
				require.Contains(t, logged.String(), "[ERROR] GET /api/v1/usage/dashboard/stats")
			} else {
				require.NotContains(t, logged.String(), "[ERROR]")
			}
		})
	}
}
