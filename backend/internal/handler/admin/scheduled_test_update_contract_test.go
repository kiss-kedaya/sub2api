package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type scheduledUpdatePlans struct {
	service.ScheduledTestPlanRepository
	plan *service.ScheduledTestPlan
}

func (r *scheduledUpdatePlans) GetByID(context.Context, int64) (*service.ScheduledTestPlan, error) {
	copy := *r.plan
	return &copy, nil
}

func (r *scheduledUpdatePlans) Update(_ context.Context, plan *service.ScheduledTestPlan) (*service.ScheduledTestPlan, error) {
	r.plan = plan
	return plan, nil
}

type scheduledUpdateAdmin struct{ service.AdminService }

func (scheduledUpdateAdmin) GetAccount(context.Context, int64) (*service.Account, error) {
	owner := int64(7)
	return &service.Account{ID: 22, CreatedBy: &owner}, nil
}

func TestScheduledTestUpdateDistinguishesMissingAndExplicitZeroValues(t *testing.T) {
	gin.SetMode(gin.TestMode)
	plans := &scheduledUpdatePlans{plan: &service.ScheduledTestPlan{
		ID: 1, AccountID: 22, ModelID: "model-a", PromptText: "custom", CronExpression: "0 * * * *", MaxResults: 20,
	}}
	h := NewScheduledTestHandler(service.NewScheduledTestService(plans, nil), scheduledUpdateAdmin{})

	for _, tc := range []struct {
		name, body           string
		wantPrompt, wantCron string
		wantMax              int
	}{
		{name: "missing fields preserve", body: `{}`, wantPrompt: "custom", wantCron: "0 * * * *", wantMax: 20},
		{name: "explicit defaults normalize", body: `{"prompt_text":"","cron_expression":"","max_results":0}`, wantPrompt: service.DefaultScheduledTestPrompt, wantCron: service.DefaultScheduledTestCron, wantMax: 50},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := gin.New()
			r.Use(func(c *gin.Context) {
				c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 7})
				c.Next()
			})
			r.PUT("/plans/:id", h.Update)
			req := httptest.NewRequest(http.MethodPut, "/plans/1", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			require.Equal(t, tc.wantPrompt, plans.plan.PromptText)
			require.Equal(t, tc.wantCron, plans.plan.CronExpression)
			require.Equal(t, tc.wantMax, plans.plan.MaxResults)
		})
	}
}
