package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type keyRotationGateListerStub struct {
	keys []service.APIKey
	err  error
}

func (s *keyRotationGateListerStub) ListActiveKeysByUser(context.Context, int64) ([]service.APIKey, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.keys, nil
}

func newKeyRotationGateTestMiddleware(t *testing.T, lister service.KeyRotationActiveKeyLister, audit *service.AuditLogService, cutoff time.Time) KeyRotationGateMiddleware {
	t.Helper()
	cfg := &config.Config{Security: config.SecurityConfig{
		KeyRotationGateEnabled: true,
		KeyRotationCutoff:      cutoff.Format(time.RFC3339),
	}}
	return NewKeyRotationGateMiddleware(service.NewKeyRotationGateService(lister, cfg, nil), audit)
}

func newKeyRotationGateTestRouter(t *testing.T, gate KeyRotationGateMiddleware, handler gin.HandlerFunc) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(ContextKeyUser), AuthSubject{UserID: 42})
		c.Next()
	})
	router.Use(gin.HandlerFunc(gate))
	router.POST("/api/v1/redeem", handler)
	return router
}

func decodeKeyRotationResponse(t *testing.T, recorder *httptest.ResponseRecorder) response.Response {
	t.Helper()
	var body response.Response
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	return body
}

func TestKeyRotationGateBlocksAndAudits(t *testing.T) {
	cutoff := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	repository := &auditCaptureRepository{}
	auditService := service.NewAuditLogService(repository, nil)
	auditService.Start()

	lister := &keyRotationGateListerStub{keys: []service.APIKey{
		{ID: 1, CreatedAt: cutoff.Add(-48 * time.Hour)},
		{ID: 2, CreatedAt: cutoff.Add(-time.Minute), IPWhitelist: []string{"198.51.100.9"}},
	}}
	handlerCalled := false
	router := newKeyRotationGateTestRouter(t, newKeyRotationGateTestMiddleware(t, lister, auditService, cutoff), func(c *gin.Context) {
		handlerCalled = true
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/redeem", nil)
	request.Header.Set("User-Agent", "key-rotation-gate-test")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.False(t, handlerCalled, "blocked request must not reach the redeem handler")

	body := decodeKeyRotationResponse(t, recorder)
	require.Equal(t, http.StatusForbidden, body.Code)
	require.Equal(t, service.KeyRotationRequiredCode, body.Reason)
	require.Equal(t, service.KeyRotationRequiredMessage, body.Message)
	require.Equal(t, service.KeyRotationRequiredCode, body.Metadata["error_code"])
	require.Equal(t, service.KeyRotationRemediationPath, body.Metadata["remediation_path"])
	require.Equal(t, service.KeyRotationReasonRotationRequired, body.Metadata["reason"])

	auditService.Stop()
	repository.mu.Lock()
	logs := append([]*service.AuditLog(nil), repository.logs...)
	repository.mu.Unlock()
	require.Len(t, logs, 1, "a blocked request must produce exactly one audit record")

	entry := logs[0]
	require.Equal(t, service.AuditActionRechargeBlockedKeyRotation, entry.Action)
	require.Equal(t, http.StatusForbidden, entry.StatusCode)
	require.Equal(t, http.MethodPost, entry.Method)
	require.Equal(t, "/api/v1/redeem", entry.Path)
	require.NotEmpty(t, entry.ClientIP)
	require.NotNil(t, entry.ActorUserID)
	require.EqualValues(t, 42, *entry.ActorUserID)
	require.EqualValues(t, 42, entry.Extra["user_id"])
	require.Equal(t, "/api/v1/redeem", entry.Extra["path"])
	require.Equal(t, entry.ClientIP, entry.Extra["ip"])
	require.Equal(t, service.KeyRotationRequiredCode, entry.Extra["error_code"])
	require.EqualValues(t, 2, entry.Extra["active_key_count"])
	require.EqualValues(t, 1, entry.Extra["pending_key_count"])
	require.Equal(t, "key-rotation-gate-test", entry.UserAgent)
}

func TestKeyRotationGateAllowsCompliantUser(t *testing.T) {
	cutoff := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	lister := &keyRotationGateListerStub{keys: []service.APIKey{
		{ID: 1, CreatedAt: cutoff.Add(time.Hour)},
	}}
	handlerCalled := false
	router := newKeyRotationGateTestRouter(t, newKeyRotationGateTestMiddleware(t, lister, nil, cutoff), func(c *gin.Context) {
		handlerCalled = true
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/redeem", nil))

	require.Equal(t, http.StatusOK, recorder.Code)
	require.True(t, handlerCalled)
}

func TestKeyRotationGateAllowsUserWithoutActiveKeys(t *testing.T) {
	cutoff := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	lister := &keyRotationGateListerStub{}
	handlerCalled := false
	router := newKeyRotationGateTestRouter(t, newKeyRotationGateTestMiddleware(t, lister, nil, cutoff), func(c *gin.Context) {
		handlerCalled = true
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/redeem", nil))

	require.Equal(t, http.StatusOK, recorder.Code)
	require.True(t, handlerCalled)
}

func TestKeyRotationGateFailsOpenWhenLookupFails(t *testing.T) {
	cutoff := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	lister := &keyRotationGateListerStub{err: errors.New("database unavailable")}
	handlerCalled := false
	router := newKeyRotationGateTestRouter(t, newKeyRotationGateTestMiddleware(t, lister, nil, cutoff), func(c *gin.Context) {
		handlerCalled = true
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/redeem", nil))

	require.Equal(t, http.StatusOK, recorder.Code, "a key lookup failure must not masquerade as a rotation block")
	require.True(t, handlerCalled)
}

func TestKeyRotationGateDisabledAllowsEverything(t *testing.T) {
	cutoff := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	lister := &keyRotationGateListerStub{keys: []service.APIKey{
		{ID: 1, CreatedAt: cutoff.Add(-time.Hour)},
	}}
	auditService := service.NewAuditLogService(&auditCaptureRepository{}, nil)
	cfg := &config.Config{Security: config.SecurityConfig{
		KeyRotationGateEnabled: false,
		KeyRotationCutoff:      cutoff.Format(time.RFC3339),
	}}
	gate := NewKeyRotationGateMiddleware(service.NewKeyRotationGateService(lister, cfg, nil), auditService)

	handlerCalled := false
	router := newKeyRotationGateTestRouter(t, gate, func(c *gin.Context) {
		handlerCalled = true
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/redeem", nil))

	require.Equal(t, http.StatusOK, recorder.Code)
	require.True(t, handlerCalled)
}

func TestKeyRotationGatePassesThroughWithoutAuthSubject(t *testing.T) {
	cutoff := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	lister := &keyRotationGateListerStub{keys: []service.APIKey{
		{ID: 1, CreatedAt: cutoff.Add(-time.Hour)},
	}}
	gate := newKeyRotationGateTestMiddleware(t, lister, nil, cutoff)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(gin.HandlerFunc(gate))
	router.POST("/api/v1/redeem", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/redeem", nil))
	require.Equal(t, http.StatusOK, recorder.Code)
}
