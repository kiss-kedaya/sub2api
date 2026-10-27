package securityaudit

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type penaltyFixtureSettings struct {
	service.SettingRepository
	cfg service.SecurityPenaltyConfig
}

func (s penaltyFixtureSettings) GetMultiple(context.Context, []string) (map[string]string, error) {
	raw, _ := json.Marshal(s.cfg)
	return map[string]string{service.SettingKeySecurityPenalty: string(raw), service.SettingKeyRiskControlEnabled: "true"}, nil
}
func penaltyFixtureService(t *testing.T, policy string) (*service.SecurityPenaltyService, sqlmock.Sqlmock) {
	t.Helper()
	db, m, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	cfg := service.DefaultSecurityPenaltyConfig()
	cfg.Enabled = true
	cfg.ObserveOnly = false
	cfg.Policy = policy
	target := int64(20)
	cfg.TargetGroupID = &target
	s := service.NewSecurityPenaltyService(db, penaltyFixtureSettings{cfg: cfg})
	_, err = s.GetConfig(context.Background())
	require.NoError(t, err)
	return s, m
}
func TestSecurityPenaltyCoordinatorCarriesRestrictionWithoutChangingEntryGroup(t *testing.T) {
	for _, policy := range []string{"session_block", "session_route_then_block"} {
		t.Run(policy, func(t *testing.T) {
			p, m := penaltyFixtureService(t, policy)
			future := time.Now().Add(time.Hour)
			var block, route any
			if policy == "session_block" {
				block = future
			} else {
				route = future
			}
			session := service.SecurityPenaltySessionKey(7, "conversation")
			m.ExpectQuery("SELECT .* FROM security_penalty_subjects").WithArgs(int64(7), "session", session, policy).WillReturnRows(sqlmock.NewRows([]string{"user", "scope", "key", "policy", "count", "window", "block", "route", "updated"}).AddRow(7, "session", session, policy, 2, time.Now(), block, route, time.Now()))
			if policy == "session_route_then_block" {
				m.ExpectExec("INSERT INTO security_penalty_request_context").WithArgs(int64(7), "server-request", session).WillReturnResult(sqlmock.NewResult(0, 1))
			}
			legacy := &fakeLegacyEngine{}
			coordinator := ProvidePenaltyCoordinator(legacy, nil, p)
			group := int64(10)
			decision := coordinator.Check(context.Background(), Request{UserID: 7, APIKeyID: 8, GroupID: &group, RequestID: "server-request", PenaltySessionIdentity: "conversation"})
			if policy == "session_block" {
				require.Equal(t, DecisionBlock, decision.Kind)
				require.False(t, decision.AllowNextStage)
				require.Equal(t, http.StatusForbidden, decision.HTTPStatus)
				require.Zero(t, legacy.calls.Load(), "blocked requests are not scanned or billed again")
			} else {
				require.Equal(t, DecisionAllow, decision.Kind)
				require.True(t, decision.AllowNextStage)
				require.NotNil(t, decision.RestrictedGroupID)
				require.Equal(t, int64(20), *decision.RestrictedGroupID)
				require.Equal(t, int64(1), legacy.calls.Load(), "restricted conversations still use the existing audit")
			}
			require.Equal(t, int64(10), group, "do not silently pin an unauthorized group")
			require.NoError(t, m.ExpectationsWereMet())
		})
	}
}
func TestSecurityPenaltyPersonalRecordsCannotReadAnotherUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	p, m := penaltyFixtureService(t, "session_block")
	h := ProvidePenaltyAdminHandler(nil, p)
	m.ExpectQuery("SELECT count").WithArgs(int64(7)).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	m.ExpectQuery("SELECT id,user_id,api_key_id").WithArgs(int64(7), 20, 0).WillReturnRows(sqlmock.NewRows([]string{"id", "user", "key", "group", "request", "scope", "policy", "reason", "model", "endpoint", "source", "count", "created"}))
	r := gin.New()
	r.GET("/mine", func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 7})
		h.MyPenaltyEvents(c)
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/mine?user_id=999", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	require.NoError(t, m.ExpectationsWereMet())
}
func TestSecurityPenaltyAdminHandlersDenyObserversBeforeStateAccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &PromptAdminHandler{}
	for _, fn := range []gin.HandlerFunc{h.GetPenaltyConfig, h.SavePenaltyConfig, h.AdminPenaltyEvents, h.ReleasePenalty} {
		r := gin.New()
		r.POST("/test", func(c *gin.Context) {
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 8})
			c.Set(string(middleware.ContextKeyUserRole), "admin")
			fn(c)
		})
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/test", nil))
		require.Equal(t, http.StatusForbidden, w.Code)
	}
}

func TestPromptPenaltyFindingCommitsOnlyUnderLeaseTransaction(t *testing.T) {
	db, m, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	repo := NewPostgreSQLRepository(db)
	called := false
	repo.prepareFinding = func(context.Context) (func(*sql.Tx, PromptSnapshot, *NormalizedResult) error, error) {
		return func(*sql.Tx, PromptSnapshot, *NormalizedResult) error { called = true; return nil }, nil
	}
	m.ExpectBegin()
	m.ExpectExec("UPDATE prompt_audit_jobs SET status='done'").WillReturnResult(sqlmock.NewResult(0, 0))
	m.ExpectRollback()
	_, err = repo.Complete(context.Background(), &Job{ID: 9, ClaimVersion: 2}, &NormalizedResult{Decision: EventCritical, Action: ActionBlock}, false)
	require.ErrorIs(t, err, ErrLeaseLost)
	require.False(t, called, "a stale worker must never add a strike")
	require.NoError(t, m.ExpectationsWereMet())
}
