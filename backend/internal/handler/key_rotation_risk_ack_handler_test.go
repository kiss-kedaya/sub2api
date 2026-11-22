//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type riskAckHandlerRepoStub struct {
	mu      sync.Mutex
	created []*service.KeyRotationRiskAck
}

func (s *riskAckHandlerRepoStub) Create(_ context.Context, ack *service.KeyRotationRiskAck) (*service.KeyRotationRiskAck, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	copied := *ack
	copied.ID = int64(len(s.created) + 1)
	s.created = append(s.created, &copied)
	return &copied, nil
}

func (s *riskAckHandlerRepoStub) FindLatestValid(context.Context, int64, time.Time, time.Time) (*service.KeyRotationRiskAck, error) {
	return nil, nil
}

type riskAckHandlerSettingsStub struct {
	settings service.KeyRotationRiskAckSettings
}

func (s *riskAckHandlerSettingsStub) GetKeyRotationRiskAckSettings(context.Context) (service.KeyRotationRiskAckSettings, error) {
	return s.settings, nil
}

type riskAckHandlerAuditRepoStub struct {
	mu   sync.Mutex
	logs []*service.AuditLog
}

func (s *riskAckHandlerAuditRepoStub) BatchInsert(_ context.Context, logs []*service.AuditLog) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, entry := range logs {
		copied := *entry
		s.logs = append(s.logs, &copied)
	}
	return int64(len(logs)), nil
}

func (s *riskAckHandlerAuditRepoStub) Insert(context.Context, *service.AuditLog) error { return nil }
func (s *riskAckHandlerAuditRepoStub) List(context.Context, *service.AuditLogFilter) (*service.AuditLogList, error) {
	return &service.AuditLogList{}, nil
}
func (s *riskAckHandlerAuditRepoStub) GetByID(context.Context, int64) (*service.AuditLog, error) {
	return nil, nil
}
func (s *riskAckHandlerAuditRepoStub) Count(context.Context) (int64, error) { return 0, nil }
func (s *riskAckHandlerAuditRepoStub) TruncateAll(context.Context) error    { return nil }
func (s *riskAckHandlerAuditRepoStub) DeleteBefore(context.Context, time.Time, int) (int64, error) {
	return 0, nil
}

func (s *riskAckHandlerAuditRepoStub) snapshot() []*service.AuditLog {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*service.AuditLog(nil), s.logs...)
}

func newRiskAckTestRouter(t *testing.T, gate *service.KeyRotationGateService, riskAcks *service.KeyRotationRiskAckService, audit *service.AuditLogService) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("auth_method", service.AuditAuthMethodJWT)
		c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 42})
		c.Next()
	})
	handler := NewKeyRotationHandler(gate, riskAcks, audit)
	router.POST("/api/v1/user/security/key-rotation-risk-ack", handler.AcknowledgeRisk)
	return router
}

func newRiskAckTestGate(t *testing.T, cutoff time.Time) *service.KeyRotationGateService {
	t.Helper()
	cfg := &config.Config{Security: config.SecurityConfig{
		KeyRotationGateEnabled: true,
		KeyRotationCutoff:      cutoff.Format(time.RFC3339),
	}}
	return service.NewKeyRotationGateService(nil, cfg, nil)
}

func postRiskAck(t *testing.T, router *gin.Engine, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/user/security/key-rotation-risk-ack", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "risk-ack-handler-test")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestAcknowledgeKeyRotationRiskWritesAckAndAudit(t *testing.T) {
	cutoff := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	repo := &riskAckHandlerRepoStub{}
	settings := &riskAckHandlerSettingsStub{settings: service.KeyRotationRiskAckSettings{Enabled: true, Hours: 24}}
	riskAcks := service.NewKeyRotationRiskAckService(repo, settings)
	auditRepo := &riskAckHandlerAuditRepoStub{}
	auditService := service.NewAuditLogService(auditRepo, nil)
	auditService.Start()

	before := time.Now().UTC()
	recorder := postRiskAck(t, newRiskAckTestRouter(t, newRiskAckTestGate(t, cutoff), riskAcks, auditService), `{"acknowledge_risk":true}`)
	after := time.Now().UTC()

	auditService.Stop()

	require.Equal(t, http.StatusOK, recorder.Code)
	var body struct {
		Code int `json:"code"`
		Data struct {
			ExpiresAt time.Time `json:"expires_at"`
			Hours     int       `json:"hours"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	require.Equal(t, 24, body.Data.Hours)
	require.False(t, body.Data.ExpiresAt.Before(before.Add(24*time.Hour)))
	require.False(t, body.Data.ExpiresAt.After(after.Add(24*time.Hour)))

	repo.mu.Lock()
	created := append([]*service.KeyRotationRiskAck(nil), repo.created...)
	repo.mu.Unlock()
	require.Len(t, created, 1)
	require.EqualValues(t, 42, created[0].UserID)
	require.True(t, created[0].CutoffSnapshot.Equal(cutoff))
	require.NotEmpty(t, created[0].ClientIP)
	require.Equal(t, "risk-ack-handler-test", created[0].UserAgent)

	logs := auditRepo.snapshot()
	require.Len(t, logs, 1)
	require.Equal(t, service.AuditActionKeyRotationRiskAck, logs[0].Action)
	require.Equal(t, http.StatusOK, logs[0].StatusCode)
	require.NotNil(t, logs[0].ActorUserID)
	require.EqualValues(t, 42, *logs[0].ActorUserID)
	require.NotEmpty(t, logs[0].Extra["expires_at"])
	require.NotEmpty(t, logs[0].Extra["cutoff"])
	require.EqualValues(t, 42, logs[0].Extra["user_id"])
}

func TestAcknowledgeKeyRotationRiskRejectsMissingAckFlag(t *testing.T) {
	cutoff := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	repo := &riskAckHandlerRepoStub{}
	riskAcks := service.NewKeyRotationRiskAckService(repo, &riskAckHandlerSettingsStub{
		settings: service.KeyRotationRiskAckSettings{Enabled: true, Hours: 24},
	})
	router := newRiskAckTestRouter(t, newRiskAckTestGate(t, cutoff), riskAcks, nil)

	for _, payload := range []string{`{"acknowledge_risk":false}`, `{}`, `not-json`} {
		recorder := postRiskAck(t, router, payload)
		require.Equal(t, http.StatusBadRequest, recorder.Code, "payload %q must be rejected", payload)
	}
	repo.mu.Lock()
	require.Empty(t, repo.created)
	repo.mu.Unlock()
}

func TestAcknowledgeKeyRotationRiskDisabledFeatureReturns403(t *testing.T) {
	cutoff := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	repo := &riskAckHandlerRepoStub{}
	riskAcks := service.NewKeyRotationRiskAckService(repo, &riskAckHandlerSettingsStub{
		settings: service.KeyRotationRiskAckSettings{Enabled: false, Hours: 24},
	})
	router := newRiskAckTestRouter(t, newRiskAckTestGate(t, cutoff), riskAcks, nil)

	recorder := postRiskAck(t, router, `{"acknowledge_risk":true}`)
	require.Equal(t, http.StatusForbidden, recorder.Code)

	var body response.Response
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	require.Equal(t, service.KeyRotationRiskAckDisabledCode, body.Reason)
	require.NotEmpty(t, body.Message)

	repo.mu.Lock()
	require.Empty(t, repo.created)
	repo.mu.Unlock()
}
