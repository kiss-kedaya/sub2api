package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type userLoadInflightStore struct {
	rows []service.UsageInflightSnapshot
}

func (s *userLoadInflightStore) Save(context.Context, service.UsageInflightSnapshot) error {
	return nil
}
func (s *userLoadInflightStore) Delete(context.Context, int64, string) error { return nil }
func (s *userLoadInflightStore) ListUser(context.Context, int64) ([]service.UsageInflightSnapshot, error) {
	return s.rows, nil
}
func (s *userLoadInflightStore) ListAll(context.Context) ([]service.UsageInflightSnapshot, error) {
	return s.rows, nil
}

func TestUserHandlerGetLoadReturnsPerGroupInflightSplit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service.SetUsageInflightStore(&userLoadInflightStore{rows: []service.UsageInflightSnapshot{
		{RequestID: "a", UserID: 7, GroupID: 3, GroupName: "Claude", Model: "opus"},
		{RequestID: "b", UserID: 7, GroupID: 3, GroupName: "Claude", Model: "sonnet"},
		{RequestID: "c", UserID: 7, GroupID: 9, GroupName: "GPT", Model: "gpt-5"},
	}})
	t.Cleanup(func() { service.SetUsageInflightStore(nil) })

	h := &UserHandler{adminService: &stubAdminService{users: []service.User{{ID: 7, Concurrency: 10}}}}
	router := gin.New()
	router.GET("/users/:id/load", h.GetLoad)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/users/7/load", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	var body struct {
		Data UserLoadResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, int64(7), body.Data.UserID)
	require.Equal(t, 10, body.Data.MaxConcurrency)
	require.Len(t, body.Data.Groups, 2)
	require.Equal(t, int64(3), body.Data.Groups[0].GroupID)
	require.Equal(t, 2, body.Data.Groups[0].Inflight)
	require.Equal(t, []string{"opus", "sonnet"}, body.Data.Groups[0].Models)
}

func TestUserHandlerGetLoadRejectsInvalidID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &UserHandler{adminService: &stubAdminService{}}
	router := gin.New()
	router.GET("/users/:id/load", h.GetLoad)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/users/nope/load", nil))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}
