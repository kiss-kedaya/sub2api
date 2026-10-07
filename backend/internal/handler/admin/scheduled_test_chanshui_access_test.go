package admin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type scheduledAccessPlans struct {
	service.ScheduledTestPlanRepository
	updates int
}

func (r *scheduledAccessPlans) GetByID(context.Context, int64) (*service.ScheduledTestPlan, error) {
	return &service.ScheduledTestPlan{ID: 1, AccountID: 22}, nil
}
func (r *scheduledAccessPlans) Update(context.Context, *service.ScheduledTestPlan) (*service.ScheduledTestPlan, error) {
	r.updates++
	return nil, nil
}

type scheduledAccessAdmin struct {
	service.AdminService
	owner int64
}

func (s *scheduledAccessAdmin) GetAccount(ctx context.Context, id int64) (*service.Account, error) {
	s.owner, _ = service.AccountOwnerScopeFromContext(ctx)
	return nil, errors.New("account belongs to another administrator")
}

func TestScheduledChanshuiConfigurationAndResultsRequireAccountOwnership(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodPost, "/plans", `{"account_id":22,"model_id":"gpt-test","cron_expression":"*/5 * * * *","quality_provider":"chanshui"}`},
		{http.MethodPut, "/plans/1", `{"quality_provider":"chanshui","quality_config":{"base_url":"https://audit.example"}}`},
		{http.MethodDelete, "/plans/1", ``}, {http.MethodGet, "/plans/1/results", ``}, {http.MethodGet, "/accounts/22/plans", ``},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			plans := &scheduledAccessPlans{}
			admin := &scheduledAccessAdmin{}
			h := NewScheduledTestHandler(service.NewScheduledTestService(plans, nil), admin)
			r := gin.New()
			r.Use(func(c *gin.Context) {
				c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 7})
				c.Set(middleware.ContextKeyAuthEmail, "other-admin@example.test")
				// Restricted means an owner is configured and it is not this admin.
				c.Request = c.Request.WithContext(service.WithAccountOwnerScope(c.Request.Context(), 7, 9))
				c.Next()
			})
			r.POST("/plans", h.Create)
			r.PUT("/plans/:id", h.Update)
			r.DELETE("/plans/:id", h.Delete)
			r.GET("/plans/:id/results", h.ListResults)
			r.GET("/accounts/:id/plans", h.ListByAccount)
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
			require.Equal(t, int64(7), admin.owner)
			require.Zero(t, plans.updates)
		})
	}
}
