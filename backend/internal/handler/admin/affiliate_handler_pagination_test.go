package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type affiliatePaginationRepository struct {
	service.AffiliateRepository
	userFilter   service.AffiliateAdminFilter
	recordFilter service.AffiliateRecordFilter
}

func (r *affiliatePaginationRepository) ListUsersWithCustomSettings(_ context.Context, filter service.AffiliateAdminFilter) ([]service.AffiliateAdminEntry, int64, error) {
	r.userFilter = filter
	return []service.AffiliateAdminEntry{{UserID: 1}}, 401, nil
}

func (r *affiliatePaginationRepository) ListAffiliateInviteRecords(_ context.Context, filter service.AffiliateRecordFilter) ([]service.AffiliateInviteRecord, int64, error) {
	r.recordFilter = filter
	return []service.AffiliateInviteRecord{}, 401, nil
}

func (r *affiliatePaginationRepository) ListAffiliateRebateRecords(_ context.Context, filter service.AffiliateRecordFilter) ([]service.AffiliateRebateRecord, int64, error) {
	r.recordFilter = filter
	return []service.AffiliateRebateRecord{}, 401, nil
}

func (r *affiliatePaginationRepository) ListAffiliateTransferRecords(_ context.Context, filter service.AffiliateRecordFilter) ([]service.AffiliateTransferRecord, int64, error) {
	r.recordFilter = filter
	return []service.AffiliateTransferRecord{}, 401, nil
}

func affiliatePaginationRequest(t *testing.T, handler gin.HandlerFunc, maxPageSize int) (int, int) {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/?page=3&page_size=500", nil)
	response.SetPaginationLimitsLoader(c, func() (int, int) { return 20, maxPageSize })
	handler(c)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var result struct {
		Data struct {
			Page     int `json:"page"`
			PageSize int `json:"page_size"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	return result.Data.Page, result.Data.PageSize
}

func TestAffiliateListUsersReportsEffectivePageSize(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tt := range []struct {
		globalMax int
		wantSize  int
	}{{1000, 200}, {50, 50}, {10, 10}} {
		t.Run(fmt.Sprint(tt.globalMax), func(t *testing.T) {
			repo := &affiliatePaginationRepository{}
			handler := NewAffiliateHandler(service.NewAffiliateService(repo, nil, nil, nil), nil)
			page, size := affiliatePaginationRequest(t, handler.ListUsers, tt.globalMax)
			require.Equal(t, 3, page)
			require.Equal(t, tt.wantSize, size)
			require.Equal(t, page, repo.userFilter.Page)
			require.Equal(t, size, repo.userFilter.PageSize)
		})
	}
}

func TestAffiliateRecordListsRespectGlobalAndDedicatedLimits(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, endpoint := range []string{"invites", "rebates", "transfers"} {
		for _, tt := range []struct {
			globalMax int
			wantSize  int
		}{{1000, 100}, {50, 50}, {10, 10}} {
			t.Run(fmt.Sprintf("%s/max%d", endpoint, tt.globalMax), func(t *testing.T) {
				repo := &affiliatePaginationRepository{}
				handler := NewAffiliateHandler(service.NewAffiliateService(repo, nil, nil, nil), nil)
				method := map[string]gin.HandlerFunc{
					"invites":   handler.ListInviteRecords,
					"rebates":   handler.ListRebateRecords,
					"transfers": handler.ListTransferRecords,
				}[endpoint]
				page, size := affiliatePaginationRequest(t, method, tt.globalMax)
				require.Equal(t, 3, page)
				require.Equal(t, tt.wantSize, size)
				require.Equal(t, page, repo.recordFilter.Page)
				require.Equal(t, size, repo.recordFilter.PageSize)
			})
		}
	}
}
