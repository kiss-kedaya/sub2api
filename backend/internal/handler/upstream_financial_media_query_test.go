package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestUpstreamFinancialPersistedImageTaskPoll(t *testing.T) {
	for _, status := range []int{402, 403, 429, 500} {
		for _, path := range []string{"/v1/images/tasks/task_financial", "/images/tasks/task_financial"} {
			raw := json.RawMessage(`{"type":"new_api_error","code":"insufficient_user_quota","details":{"message":"当前额度: 0.1, 需要额度: 1.64 vendor-A host.internal sk-secret"}}`)
			stored := &service.ImageTaskRecord{ID: "task_financial", UserID: 7, APIKeyID: 9, Status: service.ImageTaskStatusFailed, HTTPStatus: status, Error: raw}
			store := &asyncImageMemoryStore{tasks: map[string]*service.ImageTaskRecord{stored.ID: stored}}
			tasks := service.NewImageTaskServiceWithOptions(store, time.Hour, time.Minute)
			handler := &AsyncImageHandler{tasks: tasks}
			router := gin.New()
			router.Use(func(ctx *gin.Context) {
				ctx.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{ID: 9, UserID: 7})
				ctx.Next()
			})
			router.GET("/v1/images/tasks/:task_id", handler.Get)
			router.GET("/images/tasks/:task_id", handler.Get)
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
			require.Equal(t, 200, recorder.Code)
			require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
			require.Equal(t, int64(502), gjson.GetBytes(recorder.Body.Bytes(), "http_status").Int())
			require.Equal(t, "failed", gjson.GetBytes(recorder.Body.Bytes(), "status").String())
			require.Equal(t, stored.ID, gjson.GetBytes(recorder.Body.Bytes(), "id").String())
			require.Contains(t, recorder.Body.String(), service.UpstreamUnavailableMessage)
			for _, secret := range []string{"sk-secret", "host.internal", "vendor-A", "1.64", "insufficient_user_quota"} {
				require.NotContains(t, recorder.Body.String(), secret)
			}
			actual, err := store.Get(context.Background(), stored.ID)
			require.NoError(t, err)
			require.Equal(t, raw, actual.Error)
			require.Equal(t, status, actual.HTTPStatus)
		}
	}
}

func TestUpstreamFinancialPersistedImageLocalBillingAndSuccess(t *testing.T) {
	for _, local := range []error{service.ErrInsufficientBalance, service.ErrBalanceWithholdingFailed, service.ErrDailyLimitExceeded, service.ErrBillingServiceUnavailable, service.ErrUserPlatformDailyQuotaExhausted} {
		status, code, message, _ := billingErrorDetails(local)
		raw, _ := json.Marshal(gin.H{"type": code, "message": message})
		store := &asyncImageMemoryStore{tasks: map[string]*service.ImageTaskRecord{"task_local": {ID: "task_local", UserID: 7, APIKeyID: 9, Status: service.ImageTaskStatusFailed, HTTPStatus: status, Error: raw}}}
		tasks := service.NewImageTaskServiceWithOptions(store, time.Hour, time.Minute)
		public, err := tasks.Get(context.Background(), service.ImageTaskOwner{UserID: 7, APIKeyID: 9}, "task_local")
		require.NoError(t, err)
		require.Equal(t, status, public.HTTPStatus)
		require.JSONEq(t, string(raw), string(public.Error))
	}
	for _, test := range []struct {
		status     string
		httpStatus int
		body       string
	}{
		{service.ImageTaskStatusCompleted, 200, `{"data":[{"url":"https://example.test/image.png"}],"usage":{"total_tokens":8},"text":"usage cost: $1.64 sk-secret"}`},
		{service.ImageTaskStatusFailed, 400, `{"type":"invalid_request_error","message":"Invalid usage field"}`},
		{service.ImageTaskStatusFailed, 403, `{"type":"model_not_found","message":"Model billing-model does not exist"}`},
	} {
		stored := &service.ImageTaskRecord{ID: "task_test", UserID: 7, APIKeyID: 9, Status: test.status, HTTPStatus: test.httpStatus}
		if test.status == service.ImageTaskStatusCompleted {
			stored.Result = json.RawMessage(test.body)
		} else {
			stored.Error = json.RawMessage(test.body)
		}
		store := &asyncImageMemoryStore{tasks: map[string]*service.ImageTaskRecord{stored.ID: stored}}
		public, err := service.NewImageTaskServiceWithOptions(store, time.Hour, time.Minute).Get(context.Background(), service.ImageTaskOwner{UserID: 7, APIKeyID: 9}, stored.ID)
		require.NoError(t, err)
		require.Equal(t, test.httpStatus, public.HTTPStatus)
		require.Equal(t, stored.Error, public.Error)
		require.Equal(t, stored.Result, public.Result)
	}
}
