package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type ownerScopeGuardAdminService struct {
	service.AdminService
	accounts map[int64]*service.Account
}

func (s *ownerScopeGuardAdminService) GetAccount(ctx context.Context, id int64) (*service.Account, error) {
	a := s.accounts[id]
	adminID, all, _ := service.AccountOwnerScopeDetail(ctx)
	if a == nil || !service.AccountVisibleToOwner(a.CreatedBy, adminID, all) {
		return nil, service.ErrAccountNotFound
	}
	return a, nil
}

func (s *ownerScopeGuardAdminService) GetAccountsByIDs(ctx context.Context, ids []int64) ([]*service.Account, error) {
	var out []*service.Account
	for _, id := range ids {
		if a, err := s.GetAccount(ctx, id); err == nil {
			out = append(out, a)
		}
	}
	return out, nil
}

func newOwnerScopeGuardHandler() *AccountHandler {
	owner, other := int64(7), int64(8)
	return &AccountHandler{adminService: &ownerScopeGuardAdminService{accounts: map[int64]*service.Account{
		11: {ID: 11, CreatedBy: &owner}, 12: {ID: 12, CreatedBy: &other}, 13: {ID: 13},
	}}}
}

func TestAccountOwnerGuardBlocksRawWritesAndCacheReads(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := newOwnerScopeGuardHandler()
	for _, tc := range []struct {
		method, path string
		handler      gin.HandlerFunc
	}{
		{"POST", "/:id/clear-rate-limit", h.ClearRateLimit},
		{"DELETE", "/:id/temp-unschedulable", h.ClearTempUnschedulable},
		{"GET", "/:id/temp-unschedulable", h.GetTempUnschedulable},
		{"GET", "/:id/today-stats", h.GetTodayStats},
		{"GET", "/:id/stats", h.GetStats},
		{"GET", "/:id/usage", h.GetUsage},
	} {
		for _, id := range []int64{12, 13} {
			t.Run(tc.method+tc.path+strconv.FormatInt(id, 10), func(t *testing.T) {
				router := gin.New()
				router.Use(func(c *gin.Context) {
					c.Request = c.Request.WithContext(service.WithAccountOwnerScope(c.Request.Context(), 7, 0))
					c.Next()
				})
				router.Use(h.RequireAccountOwnerAccess)
				router.Handle(tc.method, tc.path, tc.handler)
				r := httptest.NewRequest(tc.method, strings.ReplaceAll(tc.path, ":id", strconv.FormatInt(id, 10)), nil)
				w := httptest.NewRecorder()
				// Services behind the guard are nil, so bypassing the guard also
				// panics rather than silently returning a false-positive response.
				require.NotPanics(t, func() { router.ServeHTTP(w, r) })
				require.Equal(t, http.StatusNotFound, w.Code)
			})
		}
	}
}

func TestAccountOwnerGuardAllowsConfiguredOwnerAndOwnUploads(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name               string
		ownerID, accountID int64
	}{
		{"own", 0, 11}, {"pool owner legacy", 7, 13}, {"pool owner other upload", 7, 12},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router := gin.New()
			router.Use(func(c *gin.Context) {
				c.Request = c.Request.WithContext(service.WithAccountOwnerScope(c.Request.Context(), 7, tc.ownerID))
				c.Next()
			})
			router.Use(newOwnerScopeGuardHandler().RequireAccountOwnerAccess)
			router.GET("/:id", func(c *gin.Context) { c.Status(http.StatusNoContent) })
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest("GET", "/"+strconv.FormatInt(tc.accountID, 10), nil))
			require.Equal(t, http.StatusNoContent, w.Code)
		})
	}
}

func TestAccountOwnerBatchUsageChecksVisibilityBeforeCache(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := newOwnerScopeGuardHandler()
	for _, handler := range []gin.HandlerFunc{h.GetBatchTodayStats, h.GetBatchUsage} {
		t.Run("batch", func(t *testing.T) {
			router := gin.New()
			router.Use(func(c *gin.Context) {
				c.Request = c.Request.WithContext(service.WithAccountOwnerScope(c.Request.Context(), 7, 0))
				c.Next()
			})
			router.POST("/batch", handler)
			accountTodayStatsBatchCache.Set(buildAccountTodayStatsBatchCacheKey([]int64{11, 12}), gin.H{"stats": gin.H{"12": "private"}})
			r := httptest.NewRequest("POST", "/batch", strings.NewReader(`{"account_ids":[11,12]}`))
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			require.NotPanics(t, func() { router.ServeHTTP(w, r) })
			require.Equal(t, http.StatusNotFound, w.Code)
			require.NotContains(t, w.Body.String(), "private")
		})
	}
}
