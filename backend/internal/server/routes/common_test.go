package routes

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCommonRoutesReadiness(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterCommonRoutes(router, func(_ context.Context) error { return nil })

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.JSONEq(t, `{"status":"ready"}`, recorder.Body.String())
}

func TestCommonRoutesReadinessFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterCommonRoutes(router, func(_ context.Context) error { return errors.New("dependency unavailable") })

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.JSONEq(t, `{"status":"not_ready"}`, recorder.Body.String())
}

func TestCommonRoutesHealthRemainsLivenessOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterCommonRoutes(router, func(_ context.Context) error { return errors.New("dependency unavailable") })

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.JSONEq(t, `{"status":"ok"}`, recorder.Body.String())
}
