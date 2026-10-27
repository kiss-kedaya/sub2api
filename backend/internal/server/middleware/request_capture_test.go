package middleware

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/requestcapture"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type captureIdentityStore struct {
	requestcapture.Store
	mu     sync.Mutex
	task   *requestcapture.Task
	record *requestcapture.Record
}

func (s *captureIdentityStore) Tasks(context.Context, string, int, int) ([]requestcapture.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.task == nil {
		return []requestcapture.Task{}, nil
	}
	return []requestcapture.Task{*s.task}, nil
}
func (s *captureIdentityStore) SaveTask(_ context.Context, t *requestcapture.Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	copy := *t
	s.task = &copy
	return nil
}
func (s *captureIdentityStore) SaveRecord(_ context.Context, r *requestcapture.Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	copy := *r
	s.record = &copy
	return nil
}
func (s *captureIdentityStore) Task(context.Context, string, string) (*requestcapture.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	copy := *s.task
	return &copy, nil
}
func (s *captureIdentityStore) Record(context.Context, string, string) (*requestcapture.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	copy := *s.record
	return &copy, nil
}
func (s *captureIdentityStore) DeleteRecord(context.Context, string, string) error { return nil }

func TestRequestCaptureID1UsesAuthenticatedUserAndPreservesEarlyFailureBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &captureIdentityStore{}
	manager, err := requestcapture.New(store, t.TempDir(), requestcapture.Config{QuotaMiB: 16, RetentionDays: 7, ID1DiagnosticEnabled: true})
	require.NoError(t, err)
	defer manager.Close()
	for _, user := range []int64{2, 1} {
		router := gin.New()
		router.Use(func(c *gin.Context) { c.Set(string(ContextKeyAPIKey), &service.APIKey{ID: 7, UserID: user}) }, RequestCapture(manager))
		router.POST("/v1/responses", func(c *gin.Context) {
			body, e := io.ReadAll(c.Request.Body)
			require.NoError(t, e)
			require.Equal(t, `{"input":"original-input"}`, string(body))
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "missing model"}})
		})
		req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"input":"original-input"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-User-ID", "1")
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, req)
		require.Equal(t, http.StatusBadRequest, recorder.Code)
		if user == 2 {
			store.mu.Lock()
			require.Nil(t, store.record, "client-provided identity must not trigger ID1 capture")
			store.mu.Unlock()
		}
	}
	require.Eventually(t, func() bool { store.mu.Lock(); defer store.mu.Unlock(); return store.record != nil }, time.Second, 10*time.Millisecond)
	store.mu.Lock()
	record := *store.record
	store.mu.Unlock()
	require.EqualValues(t, 1, record.UserID)
	require.Equal(t, 400, record.Status)
	stages := map[string]bool{}
	for _, part := range record.Parts {
		text, _, _, e := manager.Preview(context.Background(), record.TaskID, record.ID, part.Name, 0)
		require.NoError(t, e)
		stages[part.Stage] = true
		if part.Stage == "client_request" {
			require.Contains(t, text, "original-input")
		}
	}
	require.True(t, stages["client_request"])
	require.True(t, stages["client_response"])
}
