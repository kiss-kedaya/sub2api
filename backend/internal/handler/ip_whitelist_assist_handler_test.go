package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type ipWhitelistAssistServiceStub struct {
	result    service.IPWhitelistAssistResult
	err       error
	gotUserID int64
	gotIP     string
}

func (s *ipWhitelistAssistServiceStub) Get(_ context.Context, userID int64, currentIP string) (service.IPWhitelistAssistResult, error) {
	s.gotUserID = userID
	s.gotIP = currentIP
	return s.result, s.err
}

func newIPWhitelistAssistTestContext(t *testing.T, userID int64) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/user/ip-whitelist-assist", nil)
	if userID > 0 {
		c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: userID})
	}
	return c, recorder
}

func TestIPWhitelistAssistHandlerReturnsPayload(t *testing.T) {
	stub := &ipWhitelistAssistServiceStub{result: service.IPWhitelistAssistResult{
		CurrentIP:  "203.0.113.7",
		CurrentGeo: service.IPGeo{Country: "China", Region: "Guangdong", City: "Shenzhen"},
		RecentIPs: []service.IPWhitelistAssistRecentIP{
			{IP: "203.0.113.7", Count: 12, Geo: service.IPGeo{City: "Shenzhen"}},
		},
	}}
	handler := &IPWhitelistAssistHandler{service: stub}
	c, recorder := newIPWhitelistAssistTestContext(t, 42)

	handler.Get(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, int64(42), stub.gotUserID)
	var payload struct {
		Code int `json:"code"`
		Data struct {
			CurrentIP  string `json:"current_ip"`
			CurrentGeo struct {
				Country string `json:"country"`
				Region  string `json:"region"`
				City    string `json:"city"`
				ISP     string `json:"isp"`
				Hosting bool   `json:"hosting"`
			} `json:"current_geo"`
			RecentIPs []struct {
				IP    string `json:"ip"`
				Count int    `json:"count"`
			} `json:"recent_ips"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	require.Equal(t, 0, payload.Code)
	require.Equal(t, "203.0.113.7", payload.Data.CurrentIP)
	require.Equal(t, "Shenzhen", payload.Data.CurrentGeo.City)
	require.Len(t, payload.Data.RecentIPs, 1)
	require.Equal(t, 12, payload.Data.RecentIPs[0].Count)
}

func TestIPWhitelistAssistHandlerRequiresAuthentication(t *testing.T) {
	handler := &IPWhitelistAssistHandler{service: &ipWhitelistAssistServiceStub{}}
	c, recorder := newIPWhitelistAssistTestContext(t, 0)

	handler.Get(c)

	require.Equal(t, http.StatusUnauthorized, recorder.Code)
}

func TestIPWhitelistAssistHandlerRateLimitReturns429(t *testing.T) {
	handler := &IPWhitelistAssistHandler{service: &ipWhitelistAssistServiceStub{err: service.ErrIPWhitelistAssistRateLimited}}
	c, recorder := newIPWhitelistAssistTestContext(t, 42)

	handler.Get(c)

	require.Equal(t, http.StatusTooManyRequests, recorder.Code)
}

func TestIPWhitelistAssistHandlerUnexpectedError(t *testing.T) {
	handler := &IPWhitelistAssistHandler{service: &ipWhitelistAssistServiceStub{err: errors.New("boom")}}
	c, recorder := newIPWhitelistAssistTestContext(t, 42)

	handler.Get(c)

	require.NotEqual(t, http.StatusOK, recorder.Code)
}

func TestIPWhitelistAssistHandlerMarksResponseNoStore(t *testing.T) {
	handler := &IPWhitelistAssistHandler{service: &ipWhitelistAssistServiceStub{result: service.IPWhitelistAssistResult{
		CurrentIP: "203.0.113.7",
	}}}
	c, recorder := newIPWhitelistAssistTestContext(t, 42)

	handler.Get(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
}
