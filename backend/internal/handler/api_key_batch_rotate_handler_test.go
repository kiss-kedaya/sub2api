//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// rotationAuditRepoStub 捕获审计中间件写入的记录。
type rotationAuditRepoStub struct {
	mu   sync.Mutex
	logs []*service.AuditLog
}

func (s *rotationAuditRepoStub) BatchInsert(_ context.Context, logs []*service.AuditLog) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, entry := range logs {
		copied := *entry
		s.logs = append(s.logs, &copied)
	}
	return int64(len(logs)), nil
}
func (s *rotationAuditRepoStub) Insert(context.Context, *service.AuditLog) error { return nil }
func (s *rotationAuditRepoStub) List(context.Context, *service.AuditLogFilter) (*service.AuditLogList, error) {
	return &service.AuditLogList{}, nil
}
func (s *rotationAuditRepoStub) GetByID(context.Context, int64) (*service.AuditLog, error) {
	return nil, nil
}
func (s *rotationAuditRepoStub) Count(context.Context) (int64, error) { return 0, nil }
func (s *rotationAuditRepoStub) TruncateAll(context.Context) error    { return nil }
func (s *rotationAuditRepoStub) DeleteBefore(context.Context, time.Time, int) (int64, error) {
	return 0, nil
}
func (s *rotationAuditRepoStub) snapshot() []*service.AuditLog {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*service.AuditLog(nil), s.logs...)
}

// rotationAPIKeyRepoStub 是批量轮换 handler 测试用的 APIKeyRepository。
type rotationAPIKeyRepoStub struct {
	service.APIKeyRepository

	keys      map[int64]*service.APIKey
	owners    map[int64]int64
	updateErr map[int64]error
}

func (s *rotationAPIKeyRepoStub) GetByID(_ context.Context, id int64) (*service.APIKey, error) {
	key, ok := s.keys[id]
	if !ok {
		return nil, service.ErrAPIKeyNotFound
	}
	clone := *key
	return &clone, nil
}

func (s *rotationAPIKeyRepoStub) ExistsByKey(context.Context, string) (bool, error) {
	return false, nil
}

func (s *rotationAPIKeyRepoStub) VerifyOwnership(_ context.Context, userID int64, ids []int64) ([]int64, error) {
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if owner, ok := s.owners[id]; ok && owner == userID {
			out = append(out, id)
		}
	}
	return out, nil
}

func (s *rotationAPIKeyRepoStub) Update(_ context.Context, key *service.APIKey, fields service.APIKeyUpdateFields) error {
	if err := s.updateErr[key.ID]; err != nil {
		return err
	}
	if fields.Key {
		key.KeyHash = service.HashAPIKey(key.Key)
		key.KeyPrefix = service.APIKeyPrefixFromPlaintext(key.Key)
	}
	return nil
}

func newRotationTestRouter(t *testing.T, repo *rotationAPIKeyRepoStub, auditRepo *rotationAuditRepoStub) (*gin.Engine, *service.AuditLogService) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	apiKeySvc := service.NewAPIKeyService(repo, nil, nil, nil, nil, nil, &config.Config{})
	h := NewAPIKeyHandler(apiKeySvc, nil)

	auditService := service.NewAuditLogService(auditRepo, nil)
	auditService.Start()

	router := gin.New()
	// 生产顺序：认证中间件先写主体，审计中间件再包住 handler。
	router.Use(func(c *gin.Context) {
		c.Set("auth_method", service.AuditAuthMethodJWT)
		c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 7})
		c.Next()
	})
	router.Use(gin.HandlerFunc(middleware2.NewAuditLogMiddleware(auditService)))
	router.POST("/api/v1/keys/batch-rotate", h.BatchRotate)
	router.PUT("/api/v1/keys/:id", h.Update)
	return router, auditService
}

func newRotationRepoFixture() *rotationAPIKeyRepoStub {
	return &rotationAPIKeyRepoStub{
		keys: map[int64]*service.APIKey{
			1: {ID: 1, UserID: 7, Name: "one", KeyHash: service.HashAPIKey("sk-old-one"), KeyPrefix: "sk-old-o", Status: service.StatusActive},
			2: {ID: 2, UserID: 7, Name: "two", KeyHash: service.HashAPIKey("sk-old-two"), KeyPrefix: "sk-old-t", Status: service.StatusActive},
			3: {ID: 3, UserID: 99, Name: "other", KeyHash: service.HashAPIKey("sk-other"), KeyPrefix: "sk-other", Status: service.StatusActive},
		},
		owners:    map[int64]int64{1: 7, 2: 7, 3: 99},
		updateErr: map[int64]error{},
	}
}

type batchRotateResponse struct {
	Code int `json:"code"`
	Data struct {
		Results []struct {
			ID        int64  `json:"id"`
			Status    string `json:"status"`
			Key       string `json:"key"`
			ErrorCode string `json:"error_code"`
		} `json:"results"`
		RequestedCount int `json:"requested_count"`
		SucceededCount int `json:"succeeded_count"`
		FailedCount    int `json:"failed_count"`
	} `json:"data"`
}

func doRotationRequest(t *testing.T, router *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestBatchRotateHandlerRotatesAndWritesBatchAudit(t *testing.T) {
	auditRepo := &rotationAuditRepoStub{}
	router, auditStop := newRotationTestRouter(t, newRotationRepoFixture(), auditRepo)

	// 故意重复 id，验证去重后仍只轮换一次。
	rec := doRotationRequest(t, router, http.MethodPost, "/api/v1/keys/batch-rotate", `{"ids":[1,2,1]}`)
	require.Equal(t, http.StatusOK, rec.Code)

	var body batchRotateResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Data.Results, 2)
	require.Equal(t, 2, body.Data.RequestedCount)
	require.Equal(t, 2, body.Data.SucceededCount)
	require.Zero(t, body.Data.FailedCount)
	require.NotEmpty(t, body.Data.Results[0].Key)
	require.NotEqual(t, body.Data.Results[0].Key, body.Data.Results[1].Key)
	require.Equal(t, "ok", body.Data.Results[0].Status)

	auditStop.Stop()
	logs := auditRepo.snapshot()
	require.Len(t, logs, 1)
	require.Equal(t, service.AuditActionAPIKeyRotate, logs[0].Action)
	require.Equal(t, http.StatusOK, logs[0].StatusCode)
	require.EqualValues(t, true, logs[0].Extra["batch"])
	require.EqualValues(t, true, logs[0].Extra["rotate"])
	require.EqualValues(t, 2, logs[0].Extra["requested_count"])
	require.EqualValues(t, 2, logs[0].Extra["succeeded_count"])
	require.EqualValues(t, 0, logs[0].Extra["failed_count"])
	require.NotContains(t, logs[0].RequestBody, "sk-", "no credential material may enter the audit row")
}

func TestBatchRotateHandlerRejectsForeignIDWith404(t *testing.T) {
	auditRepo := &rotationAuditRepoStub{}
	router, auditStop := newRotationTestRouter(t, newRotationRepoFixture(), auditRepo)

	rec := doRotationRequest(t, router, http.MethodPost, "/api/v1/keys/batch-rotate", `{"ids":[1,3]}`)
	require.Equal(t, http.StatusNotFound, rec.Code)

	var body struct {
		Code   int    `json:"code"`
		Reason string `json:"reason"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.NotEqual(t, 0, body.Code)

	auditStop.Stop()
	logs := auditRepo.snapshot()
	require.Len(t, logs, 1)
	require.Equal(t, http.StatusNotFound, logs[0].StatusCode)
	require.NotEqual(t, service.AuditActionAPIKeyRotate, logs[0].Action,
		"a rejected batch must not be recorded as a completed rotation")
}

func TestBatchRotateHandlerReportsPartialFailure(t *testing.T) {
	repo := newRotationRepoFixture()
	repo.updateErr[2] = errors.New("database is on fire")
	auditRepo := &rotationAuditRepoStub{}
	router, auditStop := newRotationTestRouter(t, repo, auditRepo)

	rec := doRotationRequest(t, router, http.MethodPost, "/api/v1/keys/batch-rotate", `{"ids":[1,2]}`)
	require.Equal(t, http.StatusOK, rec.Code)

	var body batchRotateResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Data.Results, 2)
	require.Equal(t, "ok", body.Data.Results[0].Status)
	require.Equal(t, "failed", body.Data.Results[1].Status)
	require.NotEmpty(t, body.Data.Results[1].ErrorCode)
	require.Empty(t, body.Data.Results[1].Key)
	require.Equal(t, 1, body.Data.SucceededCount)
	require.Equal(t, 1, body.Data.FailedCount)

	auditStop.Stop()
	logs := auditRepo.snapshot()
	require.Len(t, logs, 1)
	require.EqualValues(t, 1, logs[0].Extra["succeeded_count"])
	require.EqualValues(t, 1, logs[0].Extra["failed_count"])
}

func TestBatchRotateHandlerRejectsTooManyIDs(t *testing.T) {
	router, auditStop := newRotationTestRouter(t, newRotationRepoFixture(), &rotationAuditRepoStub{})

	ids := make([]string, 0, service.MaxBatchRotateAPIKeys+1)
	for i := 1; i <= service.MaxBatchRotateAPIKeys+1; i++ {
		ids = append(ids, strconv.Itoa(i))
	}
	payload := `{"ids":[` + strings.Join(ids, ",") + `]}`
	rec := doRotationRequest(t, router, http.MethodPost, "/api/v1/keys/batch-rotate", payload)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	auditStop.Stop()
}

func TestSingleRotationWritesSameAuditAction(t *testing.T) {
	auditRepo := &rotationAuditRepoStub{}
	router, auditStop := newRotationTestRouter(t, newRotationRepoFixture(), auditRepo)

	rec := doRotationRequest(t, router, http.MethodPut, "/api/v1/keys/1", `{"regenerate_key":true}`)
	require.Equal(t, http.StatusOK, rec.Code)

	var body struct {
		Data struct {
			ID  int64  `json:"id"`
			Key string `json:"key"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, int64(1), body.Data.ID)
	require.NotEmpty(t, body.Data.Key)

	auditStop.Stop()
	logs := auditRepo.snapshot()
	require.Len(t, logs, 1)
	require.Equal(t, service.AuditActionAPIKeyRotate, logs[0].Action)
	require.EqualValues(t, false, logs[0].Extra["batch"])
	require.EqualValues(t, true, logs[0].Extra["rotate"])
	require.EqualValues(t, 1, logs[0].Extra["api_key_id"])
	require.NotContains(t, logs[0].RequestBody, "sk-", "no credential material may enter the audit row")
}
