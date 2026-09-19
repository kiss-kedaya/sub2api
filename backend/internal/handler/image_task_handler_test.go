package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type asyncImageMemoryStore struct {
	mu      sync.RWMutex
	tasks   map[string]*service.ImageTaskRecord
	saveErr error
}

func (s *asyncImageMemoryStore) Save(_ context.Context, task *service.ImageTaskRecord, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.saveErr != nil {
		return s.saveErr
	}
	copy := *task
	copy.Result = append(json.RawMessage(nil), task.Result...)
	copy.PendingResult = append(json.RawMessage(nil), task.PendingResult...)
	copy.Error = append(json.RawMessage(nil), task.Error...)
	s.tasks[task.ID] = &copy
	return nil
}

func (s *asyncImageMemoryStore) Get(_ context.Context, id string) (*service.ImageTaskRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	task := s.tasks[id]
	if task == nil {
		return nil, service.ErrImageTaskNotFound
	}
	copy := *task
	copy.Result = append(json.RawMessage(nil), task.Result...)
	copy.PendingResult = append(json.RawMessage(nil), task.PendingResult...)
	copy.Error = append(json.RawMessage(nil), task.Error...)
	return &copy, nil
}

func TestAsyncImageHandlerSubmitAndPoll(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &asyncImageMemoryStore{tasks: make(map[string]*service.ImageTaskRecord)}
	tasks := service.NewImageTaskServiceWithUploader(store, nil, time.Hour, time.Minute)
	release := make(chan struct{})
	h := &AsyncImageHandler{tasks: tasks}
	h.execute = func(_ string, c *gin.Context) {
		<-release
		c.JSON(http.StatusOK, gin.H{"created": 123, "data": []gin.H{{"url": "https://example.test/image.png"}}})
		recordMediaSettlementResult(c, nil)
	}

	router := gin.New()
	router.Use(func(c *gin.Context) {
		groupID := int64(3)
		c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
			ID:      9,
			UserID:  7,
			GroupID: &groupID,
			Group:   &service.Group{ID: groupID, Platform: service.PlatformOpenAI, AllowImageGeneration: true},
		})
		c.Next()
	})
	router.POST("/v1/images/generations/async", h.Submit)
	router.GET("/v1/images/tasks/:task_id", h.Get)

	requestCtx, cancelRequest := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations/async", strings.NewReader(`{"model":"gpt-image-1","prompt":"cat"}`)).WithContext(requestCtx)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusAccepted, w.Code)
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	require.Equal(t, "3", w.Header().Get("Retry-After"))

	var accepted struct {
		TaskID  string `json:"task_id"`
		Status  string `json:"status"`
		PollURL string `json:"poll_url"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &accepted))
	require.Equal(t, service.ImageTaskStatusProcessing, accepted.Status)
	require.Equal(t, "/v1/images/tasks/"+accepted.TaskID, accepted.PollURL)
	require.Equal(t, accepted.PollURL, w.Header().Get("Location"))

	// The detached background request must survive completion of/cancellation
	// from the short submission request.
	cancelRequest()
	close(release)
	require.Eventually(t, func() bool {
		got, err := tasks.Get(context.Background(), service.ImageTaskOwner{UserID: 7, APIKeyID: 9}, accepted.TaskID)
		return err == nil && got.Status == service.ImageTaskStatusCompleted
	}, time.Second, 10*time.Millisecond)

	pollReq := httptest.NewRequest(http.MethodGet, accepted.PollURL, nil)
	pollWriter := httptest.NewRecorder()
	router.ServeHTTP(pollWriter, pollReq)
	require.Equal(t, http.StatusOK, pollWriter.Code)
	require.Equal(t, "no-store", pollWriter.Header().Get("Cache-Control"))
	require.Empty(t, pollWriter.Header().Get("Retry-After"))
	require.Contains(t, pollWriter.Body.String(), "https://example.test/image.png")
}

func TestAsyncImageHandlerDoesNotCompleteWithoutMediaSettlement(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &asyncImageMemoryStore{tasks: make(map[string]*service.ImageTaskRecord)}
	tasks := service.NewImageTaskServiceWithUploader(store, nil, time.Hour, time.Minute)
	h := &AsyncImageHandler{tasks: tasks}
	h.execute = func(_ string, c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"data": []gin.H{{"url": "https://example.test/unsettled.png"}}})
	}

	router := gin.New()
	router.Use(func(c *gin.Context) {
		groupID := int64(3)
		c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
			ID: 9, UserID: 7, GroupID: &groupID,
			Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI, AllowImageGeneration: true},
		})
		c.Next()
	})
	router.POST("/v1/images/generations/async", h.Submit)
	router.GET("/v1/images/tasks/:task_id", h.Get)

	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations/async", strings.NewReader(`{"model":"gpt-image-1","prompt":"cat"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusAccepted, w.Code)

	var accepted struct {
		TaskID  string `json:"task_id"`
		PollURL string `json:"poll_url"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &accepted))
	require.Eventually(t, func() bool {
		record, err := store.Get(context.Background(), accepted.TaskID)
		return err == nil && record.Status == service.ImageTaskStatusFailed && len(record.Result) == 0
	}, time.Second, 10*time.Millisecond)

	pollWriter := httptest.NewRecorder()
	router.ServeHTTP(pollWriter, httptest.NewRequest(http.MethodGet, accepted.PollURL, nil))
	require.Equal(t, http.StatusOK, pollWriter.Code)
	require.NotContains(t, pollWriter.Body.String(), "https://example.test/unsettled.png")
}

func TestAsyncImageHandlerKeepsResultHiddenWhenMediaSettlementFails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &asyncImageMemoryStore{tasks: make(map[string]*service.ImageTaskRecord)}
	tasks := service.NewImageTaskServiceWithUploader(store, nil, time.Hour, time.Minute)
	h := &AsyncImageHandler{tasks: tasks}
	h.execute = func(_ string, c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"data": []gin.H{{"url": "https://example.test/unsettled.png"}}})
		recordMediaSettlementResult(c, errors.New("settlement unavailable"))
	}

	router := gin.New()
	router.Use(func(c *gin.Context) {
		groupID := int64(3)
		c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
			ID: 9, UserID: 7, GroupID: &groupID,
			Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI, AllowImageGeneration: true},
		})
		c.Next()
	})
	router.POST("/v1/images/generations/async", h.Submit)

	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations/async", strings.NewReader(`{"model":"gpt-image-1","prompt":"cat"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusAccepted, w.Code)

	var accepted struct {
		TaskID string `json:"task_id"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &accepted))
	require.Eventually(t, func() bool {
		record, err := store.Get(context.Background(), accepted.TaskID)
		return err == nil && record.Status == service.ImageTaskStatusFailed && len(record.Result) == 0
	}, time.Second, 10*time.Millisecond)
}

func TestAsyncImageHandlerStagesResultWhileDurableBillingIsPending(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &asyncImageMemoryStore{tasks: make(map[string]*service.ImageTaskRecord)}
	tasks := service.NewImageTaskServiceWithUploader(store, nil, time.Hour, time.Minute)
	h := &AsyncImageHandler{tasks: tasks}
	h.execute = func(_ string, c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"data": []gin.H{{"url": "https://example.test/pending.png"}}})
		recordMediaSettlementResult(c, errors.Join(service.ErrMediaBillingPending, errors.New("settlement retry scheduled")))
	}

	router := gin.New()
	router.Use(func(c *gin.Context) {
		groupID := int64(3)
		c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
			ID: 9, UserID: 7, GroupID: &groupID,
			Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI, AllowImageGeneration: true},
		})
		c.Next()
	})
	router.POST("/v1/images/generations/async", h.Submit)

	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations/async", strings.NewReader(`{"model":"gpt-image-1","prompt":"cat"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusAccepted, w.Code)

	var accepted struct {
		TaskID string `json:"task_id"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &accepted))
	require.Eventually(t, func() bool {
		record, err := store.Get(context.Background(), accepted.TaskID)
		return err == nil && record.Status == service.ImageTaskStatusBilling && len(record.Result) == 0 && strings.Contains(string(record.PendingResult), "pending.png")
	}, time.Second, 10*time.Millisecond)
	public, err := tasks.Get(context.Background(), service.ImageTaskOwner{UserID: 7, APIKeyID: 9}, accepted.TaskID)
	require.NoError(t, err)
	require.Equal(t, service.ImageTaskStatusBilling, public.Status)
	require.Empty(t, public.Result)
	require.Empty(t, public.ImageURL)
}

func TestAsyncImageHandlerPollPublishesOnlyAfterBillingCompletes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &asyncImageMemoryStore{tasks: make(map[string]*service.ImageTaskRecord)}
	tasks := service.NewImageTaskServiceWithUploader(store, nil, time.Hour, time.Minute)
	owner := service.ImageTaskOwner{UserID: 7, APIKeyID: 9}
	created, err := tasks.Create(context.Background(), owner)
	require.NoError(t, err)
	require.NoError(t, tasks.StageBilling(context.Background(), created.ID, http.StatusOK,
		json.RawMessage(`{"data":[{"url":"https://example.test/billed.png"}]}`)))
	billingDone := false
	h := &AsyncImageHandler{
		tasks: tasks,
		isBillingComplete: func(context.Context, string) (bool, error) {
			return billingDone, nil
		},
	}

	router := gin.New()
	router.Use(func(c *gin.Context) {
		groupID := int64(3)
		c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{ID: owner.APIKeyID, UserID: owner.UserID, GroupID: &groupID})
		c.Next()
	})
	router.GET("/v1/images/tasks/:task_id", h.Get)

	pending := httptest.NewRecorder()
	router.ServeHTTP(pending, httptest.NewRequest(http.MethodGet, "/v1/images/tasks/"+created.ID, nil))
	require.Equal(t, http.StatusOK, pending.Code)
	require.Equal(t, service.ImageTaskStatusBilling, gjson.GetBytes(pending.Body.Bytes(), "status").String())
	require.Equal(t, "3", pending.Header().Get("Retry-After"))
	require.NotContains(t, pending.Body.String(), "billed.png")

	billingDone = true
	completed := httptest.NewRecorder()
	router.ServeHTTP(completed, httptest.NewRequest(http.MethodGet, "/v1/images/tasks/"+created.ID, nil))
	require.Equal(t, http.StatusOK, completed.Code)
	require.Equal(t, service.ImageTaskStatusCompleted, gjson.GetBytes(completed.Body.Bytes(), "status").String())
	require.Contains(t, completed.Body.String(), "billed.png")
}

func TestAsyncImageHandlerPollChecksOwnerBeforeBillingStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &asyncImageMemoryStore{tasks: make(map[string]*service.ImageTaskRecord)}
	tasks := service.NewImageTaskServiceWithUploader(store, nil, time.Hour, time.Minute)
	owner := service.ImageTaskOwner{UserID: 7, APIKeyID: 9}
	created, err := tasks.Create(context.Background(), owner)
	require.NoError(t, err)
	require.NoError(t, tasks.StageBilling(context.Background(), created.ID, http.StatusOK,
		json.RawMessage(`{"data":[{"url":"https://example.test/private.png"}]}`)))
	checks := 0
	h := &AsyncImageHandler{tasks: tasks, isBillingComplete: func(context.Context, string) (bool, error) {
		checks++
		return true, nil
	}}

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{ID: 10, UserID: owner.UserID})
		c.Next()
	})
	router.GET("/v1/images/tasks/:task_id", h.Get)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/images/tasks/"+created.ID, nil))

	require.Equal(t, http.StatusNotFound, w.Code)
	require.Zero(t, checks)
	require.NotContains(t, w.Body.String(), "private.png")
}

func TestAsyncImageHandlerPollRetriesPublishAfterCacheFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &asyncImageMemoryStore{tasks: make(map[string]*service.ImageTaskRecord)}
	tasks := service.NewImageTaskServiceWithUploader(store, nil, time.Hour, time.Minute)
	owner := service.ImageTaskOwner{UserID: 7, APIKeyID: 9}
	created, err := tasks.Create(context.Background(), owner)
	require.NoError(t, err)
	require.NoError(t, tasks.StageBilling(context.Background(), created.ID, http.StatusOK,
		json.RawMessage(`{"data":[{"url":"https://example.test/retry-publish.png"}]}`)))
	h := &AsyncImageHandler{tasks: tasks, isBillingComplete: func(context.Context, string) (bool, error) { return true, nil }}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{ID: owner.APIKeyID, UserID: owner.UserID})
		c.Next()
	})
	router.GET("/v1/images/tasks/:task_id", h.Get)

	store.mu.Lock()
	store.saveErr = errors.New("redis down")
	store.mu.Unlock()
	failed := httptest.NewRecorder()
	router.ServeHTTP(failed, httptest.NewRequest(http.MethodGet, "/v1/images/tasks/"+created.ID, nil))
	require.Equal(t, http.StatusServiceUnavailable, failed.Code)
	require.NotContains(t, failed.Body.String(), "retry-publish.png")
	record, err := store.Get(context.Background(), created.ID)
	require.NoError(t, err)
	require.Equal(t, service.ImageTaskStatusBilling, record.Status)

	store.mu.Lock()
	store.saveErr = nil
	store.mu.Unlock()
	retried := httptest.NewRecorder()
	router.ServeHTTP(retried, httptest.NewRequest(http.MethodGet, "/v1/images/tasks/"+created.ID, nil))
	require.Equal(t, http.StatusOK, retried.Code)
	require.Equal(t, service.ImageTaskStatusCompleted, gjson.GetBytes(retried.Body.Bytes(), "status").String())
	require.Contains(t, retried.Body.String(), "retry-publish.png")
}

// When object storage is not configured the feature is fully disabled: the
// endpoints must return 404 without creating a task or writing to Redis.
func TestAsyncImageHandlerDisabledReturns404(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &asyncImageMemoryStore{tasks: make(map[string]*service.ImageTaskRecord)}
	tasks := service.NewImageTaskServiceWithOptions(store, time.Hour, time.Minute) // enabled == false
	h := &AsyncImageHandler{tasks: tasks}

	router := gin.New()
	router.Use(func(c *gin.Context) {
		groupID := int64(3)
		c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
			ID:      9,
			UserID:  7,
			GroupID: &groupID,
			Group:   &service.Group{ID: groupID, Platform: service.PlatformOpenAI, AllowImageGeneration: true},
		})
		c.Next()
	})
	router.POST("/v1/images/generations/async", h.Submit)
	router.GET("/v1/images/tasks/:task_id", h.Get)

	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations/async", strings.NewReader(`{"model":"gpt-image-1","prompt":"cat"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusNotFound, w.Code)
	require.Contains(t, w.Body.String(), "not enabled")

	pollReq := httptest.NewRequest(http.MethodGet, "/v1/images/tasks/imgtask_missing", nil)
	pollWriter := httptest.NewRecorder()
	router.ServeHTTP(pollWriter, pollReq)
	require.Equal(t, http.StatusNotFound, pollWriter.Code)

	// No task was created / persisted.
	require.Empty(t, store.tasks)
}
