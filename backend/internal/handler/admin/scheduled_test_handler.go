package admin

import (
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// ScheduledTestHandler handles admin scheduled-test-plan management.
type ScheduledTestHandler struct {
	scheduledTestSvc *service.ScheduledTestService
	adminSvc         service.AdminService
}

// NewScheduledTestHandler creates a new ScheduledTestHandler.
func NewScheduledTestHandler(scheduledTestSvc *service.ScheduledTestService, adminSvc service.AdminService) *ScheduledTestHandler {
	return &ScheduledTestHandler{scheduledTestSvc: scheduledTestSvc, adminSvc: adminSvc}
}

func (h *ScheduledTestHandler) authorizeAccount(c *gin.Context, id int64) bool {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 || h.adminSvc == nil {
		response.Forbidden(c, "administrator account access required")
		return false
	}
	ctx := service.WithAccountOwnerScope(c.Request.Context(), subject.UserID, c.GetString(middleware.ContextKeyAuthEmail))
	if _, err := h.adminSvc.GetAccount(ctx, id); err != nil {
		response.NotFound(c, "account not found")
		return false
	}
	c.Request = c.Request.WithContext(ctx)
	return true
}

type createScheduledTestPlanRequest struct {
	AccountID           int64                   `json:"account_id" binding:"required"`
	ModelID             string                  `json:"model_id"`
	PromptText          string                  `json:"prompt_text"`
	CronExpression      string                  `json:"cron_expression" binding:"required"`
	Enabled             *bool                   `json:"enabled"`
	MaxResults          int                     `json:"max_results"`
	AutoRecover         *bool                   `json:"auto_recover"`
	QualityCheckEnabled *bool                   `json:"quality_check_enabled"`
	QualityProvider     string                  `json:"quality_provider"`
	QualityConfig       *service.ChanshuiConfig `json:"quality_config"`
}

type updateScheduledTestPlanRequest struct {
	ModelID             string                  `json:"model_id"`
	PromptText          string                  `json:"prompt_text"`
	CronExpression      string                  `json:"cron_expression"`
	Enabled             *bool                   `json:"enabled"`
	MaxResults          int                     `json:"max_results"`
	AutoRecover         *bool                   `json:"auto_recover"`
	QualityCheckEnabled *bool                   `json:"quality_check_enabled"`
	QualityProvider     *string                 `json:"quality_provider"`
	QualityConfig       *service.ChanshuiConfig `json:"quality_config"`
}

// ListByAccount GET /admin/accounts/:id/scheduled-test-plans
func (h *ScheduledTestHandler) ListByAccount(c *gin.Context) {
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "invalid account id")
		return
	}

	if !h.authorizeAccount(c, accountID) {
		return
	}
	plans, err := h.scheduledTestSvc.ListPlansByAccount(c.Request.Context(), accountID)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, plans)
}

// Create POST /admin/scheduled-test-plans
func (h *ScheduledTestHandler) Create(c *gin.Context) {
	var req createScheduledTestPlanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	if !h.authorizeAccount(c, req.AccountID) {
		return
	}
	plan := &service.ScheduledTestPlan{
		AccountID:      req.AccountID,
		ModelID:        req.ModelID,
		PromptText:     req.PromptText,
		CronExpression: req.CronExpression,
		Enabled:        true,
		MaxResults:     req.MaxResults,
	}
	if req.Enabled != nil {
		plan.Enabled = *req.Enabled
	}
	if req.AutoRecover != nil {
		plan.AutoRecover = *req.AutoRecover
	}
	if req.QualityCheckEnabled != nil {
		plan.QualityCheckEnabled = *req.QualityCheckEnabled
	}
	plan.QualityProvider = req.QualityProvider
	if req.QualityConfig != nil {
		plan.QualityConfig = *req.QualityConfig
	}

	created, err := h.scheduledTestSvc.CreatePlan(c.Request.Context(), plan)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, created)
}

// Update PUT /admin/scheduled-test-plans/:id
func (h *ScheduledTestHandler) Update(c *gin.Context) {
	planID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "invalid plan id")
		return
	}

	existing, err := h.scheduledTestSvc.GetPlan(c.Request.Context(), planID)
	if err != nil {
		response.NotFound(c, "plan not found")
		return
	}

	var req updateScheduledTestPlanRequest
	if !h.authorizeAccount(c, existing.AccountID) {
		return
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	if req.ModelID != "" {
		existing.ModelID = req.ModelID
	}
	if req.PromptText != "" {
		existing.PromptText = req.PromptText
	}
	if req.CronExpression != "" {
		existing.CronExpression = req.CronExpression
	}
	if req.Enabled != nil {
		existing.Enabled = *req.Enabled
	}
	if req.MaxResults > 0 {
		existing.MaxResults = req.MaxResults
	}
	if req.AutoRecover != nil {
		existing.AutoRecover = *req.AutoRecover
	}
	if req.QualityCheckEnabled != nil {
		existing.QualityCheckEnabled = *req.QualityCheckEnabled
	}
	if req.QualityProvider != nil {
		existing.QualityProvider = *req.QualityProvider
	}
	if req.QualityConfig != nil {
		existing.QualityConfig = *req.QualityConfig
	}

	updated, err := h.scheduledTestSvc.UpdatePlan(c.Request.Context(), existing)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, updated)
}

// Delete DELETE /admin/scheduled-test-plans/:id
func (h *ScheduledTestHandler) Delete(c *gin.Context) {
	planID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "invalid plan id")
		return
	}

	existing, err := h.scheduledTestSvc.GetPlan(c.Request.Context(), planID)
	if err != nil {
		response.NotFound(c, "plan not found")
		return
	}
	if !h.authorizeAccount(c, existing.AccountID) {
		return
	}
	if err := h.scheduledTestSvc.DeletePlan(c.Request.Context(), planID); err != nil {
		response.InternalError(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

// ListResults GET /admin/scheduled-test-plans/:id/results
func (h *ScheduledTestHandler) ListResults(c *gin.Context) {
	planID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "invalid plan id")
		return
	}

	existing, err := h.scheduledTestSvc.GetPlan(c.Request.Context(), planID)
	if err != nil {
		response.NotFound(c, "plan not found")
		return
	}
	if !h.authorizeAccount(c, existing.AccountID) {
		return
	}
	limit := 50
	if l, err := strconv.Atoi(c.Query("limit")); err == nil && l > 0 {
		limit = l
	}

	results, err := h.scheduledTestSvc.ListResults(c.Request.Context(), planID, limit)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, results)
}
