package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func TestObserverRejectsRequestIdentityFiltersBeforeAnyQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, filter := range []string{"private-request", "prefix:private", "contains:private", "  private  "} {
		for _, suffix := range []string{"", "&inflight_only=true"} {
			t.Run(filter+suffix, func(t *testing.T) {
				w := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(w)
				c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/usage?request_id="+url.QueryEscape(filter)+suffix, nil)
				c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 2})
				// No service is wired: rejection must happen before any storage or live lookup.
				(&UsageHandler{}).List(c)
				if w.Code != http.StatusForbidden {
					t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
				}
			})
		}
	}
}

func TestUsagePaginationRedactsOnlyObserverAndNeverMutatesOwnerRecords(t *testing.T) {
	gin.SetMode(gin.TestMode)
	private := "fixture-secret-identity"
	record := dto.AdminUsageLog{UsageLog: dto.UsageLog{ID: 8, UserID: 2, AccountID: 3, ActualCost: 2.5,
		User: &dto.User{ID: 2, Email: private}, APIKey: &dto.APIKey{Name: private, Key: private}, IPAddress: &private, RequestID: private},
		Account: &dto.AccountSummary{ID: 3, Name: private}, IPAddress: &private}
	for _, id := range []int64{2, 1, 2} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/usage", nil)
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: id})
		writeAdminUsagePage(c, []dto.AdminUsageLog{record}, 1, 1, 20)
		if strings.Contains(w.Body.String(), private) != (id == 1) {
			t.Fatalf("id=%d response=%s", id, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), `"actual_cost":2.5`) {
			t.Fatal("billing metric was changed")
		}
	}
	if record.User.Email != private || record.APIKey.Key != private {
		t.Fatal("shared source changed")
	}
}

func TestIndependentInflightUsageResponseIsAnonymized(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// The same writer handles persisted, merged-live, and inflight-only pages.
	record := dto.AdminUsageLogFromInflight(service.UsageInflightSnapshot{UserID: 2, AccountID: 3, APIKeyID: 4, RequestID: "private-request-fixture", StartedAt: time.Now()}, time.Now())
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/usage?inflight_only=true", nil)
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 2})
	writeAdminUsagePage(c, []dto.AdminUsageLog{record}, 1, 1, 20)
	var response map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(w.Body.String(), "private-request-fixture") {
		t.Fatal("inflight correlation leaked")
	}
}

func TestObserverCannotReadCachedOwnerUserSeries(t *testing.T) {
	t.Cleanup(resetDashboardReadCachesForTest)
	resetDashboardReadCachesForTest()
	repo := &dashboardUsageRepoCacheProbe{}
	h := NewDashboardHandler(service.NewDashboardService(repo, nil, nil, nil), nil)
	ownerETag := ""
	for _, id := range []int64{1, 2, 1} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: id})
		c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/dashboard/snapshot-v2?start_date=2026-03-01&end_date=2026-03-07&include_stats=false&include_trend=false&include_model_stats=false&include_group_stats=false&include_users_trend=true", nil)
		h.GetSnapshotV2(c)
		if w.Code != 200 {
			t.Fatalf("id=%d status=%d body=%s", id, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "cache@test.dev") != (id == 1) {
			t.Fatalf("user series crossed observer boundary: %s", w.Body.String())
		}
		if id == 1 {
			if ownerETag != "" && ownerETag != w.Header().Get("ETag") {
				t.Error("owner cache mutated")
			}
			ownerETag = w.Header().Get("ETag")
		}
		if id == 2 && ownerETag == w.Header().Get("ETag") {
			t.Error("owner-only response reused by observer")
		}
	}
	if repo.usersTrendCalls.Load() != 1 {
		t.Fatal("observer loaded a private user series")
	}
}
