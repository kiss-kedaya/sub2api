package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestUsageStatsCacheRangesKeepExplicitDatesAndBoundRollingDefaults(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		name := "rolling"
		query := "?period=week&timezone=UTC"
		if explicit {
			name = "explicit"
			query = "?start_date=2026-11-01&end_date=2026-11-03&timezone=UTC"
		}
		t.Run(name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodGet, "/usage"+query, nil)
			c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 42})
			before := time.Now()
			parsed, ok := (&UsageHandler{}).parseUserUsageFilters(c, true)
			require.True(t, ok)
			gatewayStart, gatewayEnd := (&GatewayHandler{}).parseUsageDateRange(c)
			if explicit {
				require.Equal(t, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), parsed.StartTime)
				require.Equal(t, time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC), parsed.EndTime)
				expectedStart, err := timezone.ParseInLocation("2006-01-02", "2026-11-01")
				require.NoError(t, err)
				expectedEnd, err := timezone.ParseInLocation("2006-01-02", "2026-11-04")
				require.NoError(t, err)
				require.True(t, gatewayStart.Equal(expectedStart))
				require.True(t, gatewayEnd.Equal(expectedEnd))
			} else {
				for _, end := range []time.Time{parsed.EndTime, gatewayEnd} {
					require.True(t, end.Equal(end.Truncate(time.Minute)))
					require.False(t, end.Before(before.Add(-time.Minute)))
					require.False(t, end.After(time.Now()))
				}
				require.True(t, parsed.StartTime.Equal(parsed.EndTime.AddDate(0, 0, -7)))
				require.True(t, gatewayStart.Equal(gatewayEnd.AddDate(0, 0, -30)))
			}
		})
	}
}
