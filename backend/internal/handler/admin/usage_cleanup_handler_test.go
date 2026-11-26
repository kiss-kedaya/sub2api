package admin

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type cleanupRepoStub struct {
	mu           sync.Mutex
	created      []*service.UsageCleanupTask
	listTasks    []service.UsageCleanupTask
	listResult   *pagination.PaginationResult
	listErr      error
	statusByID   map[int64]string
	countResult  int64
	countTrunc   bool
	countErr     error
	lastCountFlt service.UsageCleanupFilters
}

func (s *cleanupRepoStub) CreateTask(ctx context.Context, task *service.UsageCleanupTask) error {
	if task == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if task.ID == 0 {
		task.ID = int64(len(s.created) + 1)
	}
	if task.CreatedAt.IsZero() {
		task.CreatedAt = time.Now().UTC()
	}
	task.UpdatedAt = task.CreatedAt
	clone := *task
	s.created = append(s.created, &clone)
	return nil
}

func (s *cleanupRepoStub) ListTasks(ctx context.Context, params pagination.PaginationParams) ([]service.UsageCleanupTask, *pagination.PaginationResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listTasks, s.listResult, s.listErr
}

func (s *cleanupRepoStub) ClaimNextPendingTask(ctx context.Context, staleRunningAfterSeconds int64) (*service.UsageCleanupTask, error) {
	return nil, nil
}

func (s *cleanupRepoStub) GetTaskStatus(ctx context.Context, taskID int64) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.statusByID == nil {
		return "", sql.ErrNoRows
	}
	status, ok := s.statusByID[taskID]
	if !ok {
		return "", sql.ErrNoRows
	}
	return status, nil
}

func (s *cleanupRepoStub) UpdateTaskProgress(ctx context.Context, taskID int64, deletedRows int64) error {
	return nil
}

func (s *cleanupRepoStub) CancelTask(ctx context.Context, taskID int64, canceledBy int64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.statusByID == nil {
		s.statusByID = map[int64]string{}
	}
	status := s.statusByID[taskID]
	if status != service.UsageCleanupStatusPending && status != service.UsageCleanupStatusRunning {
		return false, nil
	}
	s.statusByID[taskID] = service.UsageCleanupStatusCanceled
	return true, nil
}

func (s *cleanupRepoStub) MarkTaskSucceeded(ctx context.Context, taskID int64, deletedRows int64) error {
	return nil
}

func (s *cleanupRepoStub) MarkTaskFailed(ctx context.Context, taskID int64, deletedRows int64, errorMsg string) error {
	return nil
}

func (s *cleanupRepoStub) CountUsageLogs(ctx context.Context, filters service.UsageCleanupFilters, limit int) (int64, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastCountFlt = filters
	if s.countErr != nil {
		return 0, false, s.countErr
	}
	return s.countResult, s.countTrunc, nil
}

func (s *cleanupRepoStub) DeleteUsageLogsBatch(ctx context.Context, filters service.UsageCleanupFilters, limit int) (int64, error) {
	return 0, nil
}

var _ service.UsageCleanupRepository = (*cleanupRepoStub)(nil)

func setupCleanupRouter(cleanupService *service.UsageCleanupService, userID int64) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	if userID > 0 {
		router.Use(func(c *gin.Context) {
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: userID})
			c.Next()
		})
	}

	handler := NewUsageHandler(nil, nil, nil, cleanupService)
	router.POST("/api/v1/admin/usage/cleanup-tasks/preview", handler.PreviewCleanupTask)
	router.POST("/api/v1/admin/usage/cleanup-tasks", handler.CreateCleanupTask)
	router.GET("/api/v1/admin/usage/cleanup-tasks", handler.ListCleanupTasks)
	router.POST("/api/v1/admin/usage/cleanup-tasks/:id/cancel", handler.CancelCleanupTask)
	return router
}

func TestUsageHandlerCreateCleanupTaskUnauthorized(t *testing.T) {
	repo := &cleanupRepoStub{}
	cfg := &config.Config{UsageCleanup: config.UsageCleanupConfig{Enabled: true, MaxRangeDays: 31}}
	cleanupService := service.NewUsageCleanupService(repo, nil, nil, cfg)
	router := setupCleanupRouter(cleanupService, 0)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/usage/cleanup-tasks", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusUnauthorized, recorder.Code)
}

func TestUsageHandlerCreateCleanupTaskUnavailable(t *testing.T) {
	router := setupCleanupRouter(nil, 1)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/usage/cleanup-tasks", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
}

func TestUsageHandlerCreateCleanupTaskBindError(t *testing.T) {
	repo := &cleanupRepoStub{}
	cfg := &config.Config{UsageCleanup: config.UsageCleanupConfig{Enabled: true, MaxRangeDays: 31}}
	cleanupService := service.NewUsageCleanupService(repo, nil, nil, cfg)
	router := setupCleanupRouter(cleanupService, 88)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/usage/cleanup-tasks", bytes.NewBufferString("{bad-json"))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestUsageHandlerCreateCleanupTaskMissingRange(t *testing.T) {
	repo := &cleanupRepoStub{}
	cfg := &config.Config{UsageCleanup: config.UsageCleanupConfig{Enabled: true, MaxRangeDays: 31}}
	cleanupService := service.NewUsageCleanupService(repo, nil, nil, cfg)
	router := setupCleanupRouter(cleanupService, 88)

	payload := map[string]any{
		"start_date": "2024-01-01",
		"timezone":   "UTC",
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/usage/cleanup-tasks", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestUsageHandlerCreateCleanupTaskInvalidDate(t *testing.T) {
	repo := &cleanupRepoStub{}
	cfg := &config.Config{UsageCleanup: config.UsageCleanupConfig{Enabled: true, MaxRangeDays: 31}}
	cleanupService := service.NewUsageCleanupService(repo, nil, nil, cfg)
	router := setupCleanupRouter(cleanupService, 88)

	payload := map[string]any{
		"start_date": "2024-13-01",
		"end_date":   "2024-01-02",
		"timezone":   "UTC",
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/usage/cleanup-tasks", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestUsageHandlerCreateCleanupTaskInvalidEndDate(t *testing.T) {
	repo := &cleanupRepoStub{}
	cfg := &config.Config{UsageCleanup: config.UsageCleanupConfig{Enabled: true, MaxRangeDays: 31}}
	cleanupService := service.NewUsageCleanupService(repo, nil, nil, cfg)
	router := setupCleanupRouter(cleanupService, 88)

	payload := map[string]any{
		"start_date": "2024-01-01",
		"end_date":   "2024-02-40",
		"timezone":   "UTC",
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/usage/cleanup-tasks", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestUsageHandlerCreateCleanupTaskInvalidRequestType(t *testing.T) {
	repo := &cleanupRepoStub{}
	cfg := &config.Config{UsageCleanup: config.UsageCleanupConfig{Enabled: true, MaxRangeDays: 31}}
	cleanupService := service.NewUsageCleanupService(repo, nil, nil, cfg)
	router := setupCleanupRouter(cleanupService, 88)

	payload := map[string]any{
		"start_date":   "2024-01-01",
		"end_date":     "2024-01-02",
		"timezone":     "UTC",
		"request_type": "invalid",
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/usage/cleanup-tasks", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestUsageHandlerCreateCleanupTaskRequestTypePriority(t *testing.T) {
	repo := &cleanupRepoStub{}
	cfg := &config.Config{UsageCleanup: config.UsageCleanupConfig{Enabled: true, MaxRangeDays: 31}}
	cleanupService := service.NewUsageCleanupService(repo, nil, nil, cfg)
	router := setupCleanupRouter(cleanupService, 99)

	payload := map[string]any{
		"start_date":   "2024-01-01",
		"end_date":     "2024-01-02",
		"timezone":     "UTC",
		"request_type": "ws_v2",
		"stream":       false,
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/usage/cleanup-tasks", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusOK, recorder.Code)

	repo.mu.Lock()
	defer repo.mu.Unlock()
	require.Len(t, repo.created, 1)
	created := repo.created[0]
	require.NotNil(t, created.Filters.RequestType)
	require.Equal(t, int16(service.RequestTypeWSV2), *created.Filters.RequestType)
	require.Nil(t, created.Filters.Stream)
}

func TestUsageHandlerCreateCleanupTaskWithLegacyStream(t *testing.T) {
	repo := &cleanupRepoStub{}
	cfg := &config.Config{UsageCleanup: config.UsageCleanupConfig{Enabled: true, MaxRangeDays: 31}}
	cleanupService := service.NewUsageCleanupService(repo, nil, nil, cfg)
	router := setupCleanupRouter(cleanupService, 99)

	payload := map[string]any{
		"start_date": "2024-01-01",
		"end_date":   "2024-01-02",
		"timezone":   "UTC",
		"stream":     true,
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/usage/cleanup-tasks", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusOK, recorder.Code)

	repo.mu.Lock()
	defer repo.mu.Unlock()
	require.Len(t, repo.created, 1)
	created := repo.created[0]
	require.Nil(t, created.Filters.RequestType)
	require.NotNil(t, created.Filters.Stream)
	require.True(t, *created.Filters.Stream)
}

func TestUsageHandlerCreateCleanupTaskSuccess(t *testing.T) {
	repo := &cleanupRepoStub{}
	cfg := &config.Config{UsageCleanup: config.UsageCleanupConfig{Enabled: true, MaxRangeDays: 31}}
	cleanupService := service.NewUsageCleanupService(repo, nil, nil, cfg)
	router := setupCleanupRouter(cleanupService, 99)

	payload := map[string]any{
		"start_date": " 2024-01-01 ",
		"end_date":   "2024-01-02",
		"timezone":   "UTC",
		"model":      "gpt-4",
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/usage/cleanup-tasks", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusOK, recorder.Code)

	var resp response.Response
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Equal(t, 0, resp.Code)

	repo.mu.Lock()
	defer repo.mu.Unlock()
	require.Len(t, repo.created, 1)
	created := repo.created[0]
	require.Equal(t, int64(99), created.CreatedBy)
	require.NotNil(t, created.Filters.Model)
	require.Equal(t, "gpt-4", *created.Filters.Model)

	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC).Add(24*time.Hour - time.Nanosecond)
	require.True(t, created.Filters.StartTime.Equal(start))
	require.True(t, created.Filters.EndTime.Equal(end))
}

func TestUsageHandlerListCleanupTasksUnavailable(t *testing.T) {
	router := setupCleanupRouter(nil, 0)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/usage/cleanup-tasks", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
}

func TestUsageHandlerListCleanupTasksSuccess(t *testing.T) {
	repo := &cleanupRepoStub{}
	repo.listTasks = []service.UsageCleanupTask{
		{
			ID:        7,
			Status:    service.UsageCleanupStatusSucceeded,
			CreatedBy: 4,
		},
	}
	repo.listResult = &pagination.PaginationResult{Total: 1, Page: 1, PageSize: 20, Pages: 1}
	cfg := &config.Config{UsageCleanup: config.UsageCleanupConfig{Enabled: true, MaxRangeDays: 31}}
	cleanupService := service.NewUsageCleanupService(repo, nil, nil, cfg)
	router := setupCleanupRouter(cleanupService, 1)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/usage/cleanup-tasks", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusOK, recorder.Code)

	var resp struct {
		Code int `json:"code"`
		Data struct {
			Items []dto.UsageCleanupTask `json:"items"`
			Total int64                  `json:"total"`
			Page  int                    `json:"page"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Equal(t, 0, resp.Code)
	require.Len(t, resp.Data.Items, 1)
	require.Equal(t, int64(7), resp.Data.Items[0].ID)
	require.Equal(t, int64(1), resp.Data.Total)
	require.Equal(t, 1, resp.Data.Page)
}

func TestUsageHandlerListCleanupTasksError(t *testing.T) {
	repo := &cleanupRepoStub{listErr: errors.New("boom")}
	cfg := &config.Config{UsageCleanup: config.UsageCleanupConfig{Enabled: true, MaxRangeDays: 31}}
	cleanupService := service.NewUsageCleanupService(repo, nil, nil, cfg)
	router := setupCleanupRouter(cleanupService, 1)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/usage/cleanup-tasks", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusInternalServerError, recorder.Code)
}

func TestUsageHandlerCancelCleanupTaskUnauthorized(t *testing.T) {
	repo := &cleanupRepoStub{}
	cfg := &config.Config{UsageCleanup: config.UsageCleanupConfig{Enabled: true}}
	cleanupService := service.NewUsageCleanupService(repo, nil, nil, cfg)
	router := setupCleanupRouter(cleanupService, 0)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/usage/cleanup-tasks/1/cancel", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestUsageHandlerCancelCleanupTaskNotFound(t *testing.T) {
	repo := &cleanupRepoStub{}
	cfg := &config.Config{UsageCleanup: config.UsageCleanupConfig{Enabled: true}}
	cleanupService := service.NewUsageCleanupService(repo, nil, nil, cfg)
	router := setupCleanupRouter(cleanupService, 1)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/usage/cleanup-tasks/999/cancel", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestUsageHandlerCancelCleanupTaskConflict(t *testing.T) {
	repo := &cleanupRepoStub{statusByID: map[int64]string{2: service.UsageCleanupStatusSucceeded}}
	cfg := &config.Config{UsageCleanup: config.UsageCleanupConfig{Enabled: true}}
	cleanupService := service.NewUsageCleanupService(repo, nil, nil, cfg)
	router := setupCleanupRouter(cleanupService, 1)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/usage/cleanup-tasks/2/cancel", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusConflict, rec.Code)
}

func TestUsageHandlerCancelCleanupTaskSuccess(t *testing.T) {
	repo := &cleanupRepoStub{statusByID: map[int64]string{3: service.UsageCleanupStatusPending}}
	cfg := &config.Config{UsageCleanup: config.UsageCleanupConfig{Enabled: true}}
	cleanupService := service.NewUsageCleanupService(repo, nil, nil, cfg)
	router := setupCleanupRouter(cleanupService, 1)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/usage/cleanup-tasks/3/cancel", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
}

func TestUsageHandlerPreviewCleanupTaskSuccessWithExtendedFilters(t *testing.T) {
	repo := &cleanupRepoStub{countResult: 256, countTrunc: false}
	cfg := &config.Config{UsageCleanup: config.UsageCleanupConfig{Enabled: true, MaxRangeDays: 31}}
	cleanupService := service.NewUsageCleanupService(repo, nil, nil, cfg)
	router := setupCleanupRouter(cleanupService, 99)

	payload := map[string]any{
		"start_date":              "2026-05-01T00:00:00",
		"end_date":                "2026-05-01T23:59:59",
		"timezone":                "UTC",
		"request_id":              "=req_exact",
		"native_compaction_v2":    true,
		"billing_mode":            "image",
		"upstream_model_mismatch": true,
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/usage/cleanup-tasks/preview", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		Code int                     `json:"code"`
		Data dto.UsageCleanupPreview `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, 0, resp.Code)
	require.Equal(t, int64(256), resp.Data.MatchedRows)
	require.False(t, resp.Data.Truncated)
	require.Equal(t, 31, resp.Data.MaxRangeDays)

	repo.mu.Lock()
	defer repo.mu.Unlock()
	require.NotNil(t, repo.lastCountFlt.RequestID)
	require.Equal(t, "=req_exact", *repo.lastCountFlt.RequestID)
	require.NotNil(t, repo.lastCountFlt.NativeCompactionV2)
	require.True(t, *repo.lastCountFlt.NativeCompactionV2)
	require.NotNil(t, repo.lastCountFlt.BillingMode)
	require.Equal(t, "image", *repo.lastCountFlt.BillingMode)
	require.NotNil(t, repo.lastCountFlt.UpstreamModelMismatch)
	require.True(t, *repo.lastCountFlt.UpstreamModelMismatch)
}

func TestUsageHandlerPreviewCleanupTaskAuthAndValidation(t *testing.T) {
	// Unavailable
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/usage/cleanup-tasks/preview", bytes.NewBufferString(`{}`))
	rec := httptest.NewRecorder()
	setupCleanupRouter(nil, 1).ServeHTTP(rec, req)
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)

	// Unauthorized
	repo := &cleanupRepoStub{}
	cfg := &config.Config{UsageCleanup: config.UsageCleanupConfig{Enabled: true, MaxRangeDays: 31}}
	cleanupService := service.NewUsageCleanupService(repo, nil, nil, cfg)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/admin/usage/cleanup-tasks/preview", bytes.NewBufferString(`{}`))
	rec = httptest.NewRecorder()
	setupCleanupRouter(cleanupService, 0).ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)

	// Invalid billing mode
	router := setupCleanupRouter(cleanupService, 1)
	body, err := json.Marshal(map[string]any{
		"start_date":   "2026-05-01",
		"end_date":     "2026-05-02",
		"timezone":     "UTC",
		"billing_mode": "invalid_mode",
	})
	require.NoError(t, err)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/admin/usage/cleanup-tasks/preview", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUsageHandlerCreateCleanupTaskWithExtendedFilters(t *testing.T) {
	repo := &cleanupRepoStub{}
	cfg := &config.Config{UsageCleanup: config.UsageCleanupConfig{Enabled: true, MaxRangeDays: 31}}
	cleanupService := service.NewUsageCleanupService(repo, nil, nil, cfg)
	router := setupCleanupRouter(cleanupService, 99)

	payload := map[string]any{
		"start_date":              "2026-05-01",
		"end_date":                "2026-05-01",
		"timezone":                "UTC",
		"request_id":              "req_xyz99",
		"native_compaction_v2":    true,
		"billing_mode":            "video",
		"upstream_model_mismatch": false,
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/usage/cleanup-tasks", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	repo.mu.Lock()
	defer repo.mu.Unlock()
	require.Len(t, repo.created, 1)
	flt := repo.created[0].Filters
	require.NotNil(t, flt.RequestID)
	require.Equal(t, "req_xyz99", *flt.RequestID)
	require.NotNil(t, flt.NativeCompactionV2)
	require.True(t, *flt.NativeCompactionV2)
	require.NotNil(t, flt.BillingMode)
	require.Equal(t, "video", *flt.BillingMode)
	require.NotNil(t, flt.UpstreamModelMismatch)
	require.False(t, *flt.UpstreamModelMismatch)
}

func TestUsageHandlerPreviewCleanupTaskSkipsAuditAndHandlesTimeoutAndCancel(t *testing.T) {
	cfg := &config.Config{UsageCleanup: config.UsageCleanupConfig{Enabled: true, MaxRangeDays: 31}}

	t.Run("sets audit_skip on read-only preview", func(t *testing.T) {
		repo := &cleanupRepoStub{countResult: 12}
		cleanupService := service.NewUsageCleanupService(repo, nil, nil, cfg)
		gin.SetMode(gin.TestMode)
		router := gin.New()
		var skipped bool
		router.Use(func(c *gin.Context) {
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1})
			c.Next()
			skipped = c.GetBool("audit_skip")
		})
		handler := NewUsageHandler(nil, nil, nil, cleanupService)
		router.POST("/api/v1/admin/usage/cleanup-tasks/preview", handler.PreviewCleanupTask)

		body := []byte(`{"start_date":"2026-05-01","end_date":"2026-05-01","timezone":"UTC"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/usage/cleanup-tasks/preview", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
		require.True(t, skipped, "preview endpoint must skip admin audit log writes")
	})

	t.Run("returns 504 with USAGE_CLEANUP_PREVIEW_TIMEOUT on deadline exceeded", func(t *testing.T) {
		repo := &cleanupRepoStub{countErr: context.DeadlineExceeded}
		cleanupService := service.NewUsageCleanupService(repo, nil, nil, cfg)
		router := setupCleanupRouter(cleanupService, 1)

		body := []byte(`{"start_date":"2026-05-01","end_date":"2026-05-01","timezone":"UTC"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/usage/cleanup-tasks/preview", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		require.Equal(t, http.StatusGatewayTimeout, rec.Code)
		var resp response.Response
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		require.Equal(t, "USAGE_CLEANUP_PREVIEW_TIMEOUT", resp.Reason)
	})

	t.Run("returns cleanly without 500 when client cancels request", func(t *testing.T) {
		repo := &cleanupRepoStub{countErr: context.Canceled}
		cleanupService := service.NewUsageCleanupService(repo, nil, nil, cfg)
		router := setupCleanupRouter(cleanupService, 1)

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		body := []byte(`{"start_date":"2026-05-01","end_date":"2026-05-01","timezone":"UTC"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/usage/cleanup-tasks/preview", bytes.NewReader(body)).WithContext(ctx)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		require.NotEqual(t, http.StatusInternalServerError, rec.Code)
		require.Empty(t, rec.Body.String())
	})
}

type searchAPIKeyRepoStub struct {
	service.APIKeyRepository
	byID map[int64]*service.APIKey
}

func (s *searchAPIKeyRepoStub) GetByID(_ context.Context, id int64) (*service.APIKey, error) {
	key, ok := s.byID[id]
	if !ok {
		return nil, service.ErrAPIKeyNotFound
	}
	return key, nil
}

func TestUsageHandlerSearchAPIKeysByExactID(t *testing.T) {
	repo := &searchAPIKeyRepoStub{
		byID: map[int64]*service.APIKey{
			8523: {ID: 8523, Name: "deep-key", UserID: 77, Key: "test-key-must-not-leak"},
		},
	}
	apiKeySvc := service.NewAPIKeyService(repo, nil, nil, nil, nil, nil, &config.Config{})
	handler := NewUsageHandler(nil, apiKeySvc, nil, nil)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/v1/admin/usage/search-api-keys", handler.SearchAPIKeys)

	// Found without user_id, never leaks secret key
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/usage/search-api-keys?api_key_id=8523", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotContains(t, rec.Body.String(), "test-key-must-not-leak")
	var resp struct {
		Data []struct {
			ID     int64  `json:"id"`
			Name   string `json:"name"`
			UserID int64  `json:"user_id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp.Data, 1)
	require.Equal(t, int64(8523), resp.Data[0].ID)
	require.Equal(t, "deep-key", resp.Data[0].Name)
	require.Equal(t, int64(77), resp.Data[0].UserID)

	// User mismatch returns empty list
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/usage/search-api-keys?user_id=99&api_key_id=8523", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	resp.Data = nil
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Empty(t, resp.Data)

	// Missing ID returns empty list
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/usage/search-api-keys?api_key_id=999999", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	resp.Data = nil
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Empty(t, resp.Data)

	// Invalid ID returns 400
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/usage/search-api-keys?api_key_id=0", nil))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}
