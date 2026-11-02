package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// batchQuotaRepoForList 记录批量查询调用，并禁止回退到逐用户查询。
type batchQuotaRepoForList struct {
	service.UserPlatformQuotaRepository
	calls   int
	gotIDs  []int64
	records map[int64][]service.UserPlatformQuotaRecord
	err     error
}

func (r *batchQuotaRepoForList) ListByUsers(_ context.Context, userIDs []int64) (map[int64][]service.UserPlatformQuotaRecord, error) {
	r.calls++
	r.gotIDs = append([]int64(nil), userIDs...)
	if r.err != nil {
		return nil, r.err
	}
	return r.records, nil
}

func (r *batchQuotaRepoForList) ListByUser(_ context.Context, _ int64) ([]service.UserPlatformQuotaRecord, error) {
	panic("list page must batch quotas instead of per-user queries")
}

type listQuotaResponse struct {
	Data struct {
		Items []struct {
			ID             int64            `json:"id"`
			PlatformQuotas []map[string]any `json:"platform_quotas"`
		} `json:"items"`
	} `json:"data"`
}

func newListQuotaHandler(t *testing.T, repo *batchQuotaRepoForList) (*gin.Engine, *stubAdminService) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	stub := newStubAdminService()
	now := time.Now().UTC()
	stub.users = []service.User{
		{ID: 7, Email: "seven@example.com", Role: service.RoleUser, Status: service.StatusActive, CreatedAt: now, UpdatedAt: now},
		{ID: 8, Email: "eight@example.com", Role: service.RoleUser, Status: service.StatusActive, CreatedAt: now, UpdatedAt: now},
	}
	h := NewUserHandler(stub, nil, repo, nil, nil, nil, nil)
	r := gin.New()
	r.GET("/admin/users", h.List)
	return r, stub
}

func doListRequest(t *testing.T, r *gin.Engine, query string) listQuotaResponse {
	t.Helper()
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/admin/users"+query, nil)
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var body listQuotaResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	return body
}

func TestAdminUserList_InlinePlatformQuotasUsesSingleBatchQuery(t *testing.T) {
	start := time.Now().UTC().Add(-time.Hour)
	repo := &batchQuotaRepoForList{records: map[int64][]service.UserPlatformQuotaRecord{
		7: {{UserID: 7, Platform: "anthropic", DailyUsageUSD: 1.5, DailyWindowStart: &start}},
	}}
	router, _ := newListQuotaHandler(t, repo)
	body := doListRequest(t, router, "?include_platform_quotas=true")

	require.Equal(t, 1, repo.calls, "must hit the batch endpoint exactly once per page")
	require.ElementsMatch(t, []int64{7, 8}, repo.gotIDs)
	require.Len(t, body.Data.Items, 2)

	// 7 有配额，8 没有 -> 省略字段而不是返回空数组，避免前端误判。
	require.NotEmpty(t, body.Data.Items[0].PlatformQuotas)
	require.Equal(t, "anthropic", body.Data.Items[0].PlatformQuotas[0]["platform"])
	require.Contains(t, body.Data.Items[0].PlatformQuotas[0], "daily_window_resets_at")
	require.Nil(t, body.Data.Items[1].PlatformQuotas)
}

func TestAdminUserList_PlatformQuotasOffByDefault(t *testing.T) {
	repo := &batchQuotaRepoForList{records: map[int64][]service.UserPlatformQuotaRecord{
		7: {{UserID: 7, Platform: "anthropic"}},
	}}
	router, _ := newListQuotaHandler(t, repo)
	body := doListRequest(t, router, "")

	require.Zero(t, repo.calls, "default list must not pay an extra quota query")
	for _, item := range body.Data.Items {
		require.Nil(t, item.PlatformQuotas)
	}
}

func TestAdminUserList_PlatformQuotaFailureDoesNotBreakList(t *testing.T) {
	repo := &batchQuotaRepoForList{err: context.DeadlineExceeded}
	router, _ := newListQuotaHandler(t, repo)
	body := doListRequest(t, router, "?include_platform_quotas=1")

	require.Len(t, body.Data.Items, 2)
	for _, item := range body.Data.Items {
		require.Nil(t, item.PlatformQuotas)
	}
}

func TestAdminUserList_ForwardsPageSizes20_50_100(t *testing.T) {
	for _, pageSize := range []int{20, 50, 100} {
		t.Run(fmt.Sprintf("page_size_%d", pageSize), func(t *testing.T) {
			router, stub := newListQuotaHandler(t, nil)
			_ = doListRequest(t, router, fmt.Sprintf("?page=2&page_size=%d", pageSize))
			require.Equal(t, 2, stub.lastListUsers.page)
			require.Equal(t, pageSize, stub.lastListUsers.pageSize)
		})
	}
}
