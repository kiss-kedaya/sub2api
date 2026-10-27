package service

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func penaltyTestService(t *testing.T, cfg SecurityPenaltyConfig) (*SecurityPenaltyService, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return &SecurityPenaltyService{db: db, cached: cfg, expires: time.Now().Add(time.Hour)}, mock
}

func penaltyTestConfig() SecurityPenaltyConfig {
	cfg := DefaultSecurityPenaltyConfig()
	cfg.Enabled = true
	cfg.RiskControlEnabled = true
	cfg.ObserveOnly = false
	return cfg
}

func TestSecurityPenaltyScopeNeverWidensMissingSession(t *testing.T) {
	cfg := penaltyTestConfig()
	for _, policy := range []string{"session_block", "session_route_then_block"} {
		cfg.Policy = policy
		scope, key := penaltySubject(cfg, "")
		require.Equal(t, "request", scope)
		require.Empty(t, key)
		scope, key = penaltySubject(cfg, "key-scoped-conversation-hash")
		require.Equal(t, "session", scope)
		require.Equal(t, "key-scoped-conversation-hash", key)
	}
	cfg.Policy = "user_block"
	scope, key := penaltySubject(cfg, "")
	require.Equal(t, "user", scope)
	require.Equal(t, "user", key)
}

func TestSecurityPenaltyExpiryAndReleaseState(t *testing.T) {
	now := time.Now()
	future := now.Add(time.Hour)
	past := now.Add(-time.Minute)
	cfg := penaltyTestConfig()
	cfg.Policy = "session_route_then_block"
	tests := []struct {
		name  string
		state SecurityPenaltyState
		want  string
	}{
		{"below threshold", SecurityPenaltyState{Count: 2, WindowStartedAt: now}, "record"},
		{"route before block", SecurityPenaltyState{Count: 2, WindowStartedAt: now, RouteUntil: &future}, "route"},
		{"blocked", SecurityPenaltyState{Count: 3, WindowStartedAt: now, BlockedUntil: &future}, "block"},
		{"block outlives counting window", SecurityPenaltyState{Count: 3, WindowStartedAt: now.Add(-1000 * time.Hour), BlockedUntil: &future}, "block"},
		{"expired block", SecurityPenaltyState{Count: 3, WindowStartedAt: now, BlockedUntil: &past}, "none"},
		{"expired counting window", SecurityPenaltyState{Count: 2, WindowStartedAt: now.Add(-1000 * time.Hour)}, "none"},
		{"released", SecurityPenaltyState{Count: 0, WindowStartedAt: now}, "none"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { require.Equal(t, tt.want, penaltyAction(cfg, tt.state, now)) })
	}
}

func TestSecurityPenaltyAdmissionExemptDisabledAndObserveDoNotQuerySubjects(t *testing.T) {
	for _, name := range []string{"disabled", "master off", "exempt", "trusted", "observe", "missing session"} {
		t.Run(name, func(t *testing.T) {
			cfg := penaltyTestConfig()
			switch name {
			case "disabled":
				cfg.Enabled = false
			case "master off":
				cfg.RiskControlEnabled = false
			case "exempt":
				cfg.ExemptUserIDs = []int64{7}
			case "trusted":
				cfg.trustedUserIDs = []int64{7}
			case "observe":
				cfg.ObserveOnly = true
			}
			s, m := penaltyTestService(t, cfg)
			key := "session"
			if name == "missing session" {
				key = ""
			}
			admission, err := s.Admission(context.Background(), 7, key)
			require.NoError(t, err)
			require.False(t, admission.Blocked)
			require.Nil(t, admission.TargetGroupID)
			require.NoError(t, m.ExpectationsWereMet())
		})
	}
}

func TestSecurityPenaltyStrikeIsAtomicDeduplicatedAndTurnsIntoBlock(t *testing.T) {
	for _, duplicate := range []bool{false, true} {
		t.Run(map[bool]string{false: "third strike blocks", true: "retry does not add another strike"}[duplicate], func(t *testing.T) {
			cfg := penaltyTestConfig()
			s, m := penaltyTestService(t, cfg)
			now := time.Now()
			m.ExpectBegin()
			m.ExpectExec("SELECT pg_advisory_xact_lock").WithArgs(int64(7)).WillReturnResult(sqlmock.NewResult(0, 1))
			m.ExpectExec("INSERT INTO security_penalty_subjects").WithArgs(int64(7), "session", "session-hash", "session_block").WillReturnResult(sqlmock.NewResult(0, 0))
			m.ExpectQuery("SELECT .* FROM security_penalty_subjects .*FOR UPDATE").WithArgs(int64(7), "session", "session-hash", "session_block").WillReturnRows(sqlmock.NewRows([]string{"user", "scope", "key", "policy", "count", "window", "block", "route", "updated"}).AddRow(7, "session", "session-hash", "session_block", 2, now, nil, nil, now))
			rows := sqlmock.NewRows([]string{"id"})
			if !duplicate {
				rows.AddRow(12)
			}
			m.ExpectQuery("INSERT INTO security_penalty_events").WithArgs(int64(7), int64(8), nil, "req-unique", "session-hash", "content_moderation", "session", "session_block", "sexual", "", "model", "/v1/responses", 3).WillReturnRows(rows)
			if !duplicate {
				m.ExpectExec("UPDATE security_penalty_subjects SET strike_count").WithArgs(int64(7), "session", "session-hash", "session_block", 3, now, sqlmock.AnyArg(), nil, sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
			}
			m.ExpectCommit()
			err := s.RecordViolation(context.Background(), SecurityPenaltyViolation{UserID: 7, APIKeyID: 8, RequestID: "req-unique", SessionKey: "session-hash", Source: "content_moderation", Reason: "sexual", FullPrompt: "PROMPT_CANARY", Model: "model", Endpoint: "/v1/responses"})
			require.NoError(t, err)
			require.NoError(t, m.ExpectationsWereMet())
		})
	}
}

func TestSecurityPenaltyOriginalIsOwnerScopedAndNotInList(t *testing.T) {
	s, m := penaltyTestService(t, penaltyTestConfig())
	m.ExpectQuery(`SELECT .*full_prompt.* WHERE id=\$1 AND \(\$2::bigint=0 OR user_id=\$2\)`).WithArgs(int64(11), int64(7)).WillReturnError(sql.ErrNoRows)
	_, err := s.GetEvent(context.Background(), 7, 11)
	require.Error(t, err)
	m.ExpectQuery("SELECT count").WithArgs(int64(7)).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	m.ExpectQuery(`SELECT id,user_id,api_key_id,group_id,request_id,scope,policy,reason,model,endpoint,source,strike_count,created_at FROM security_penalty_events`).WithArgs(int64(7), 10, 0).WillReturnRows(sqlmock.NewRows([]string{"id", "user", "key", "group", "request", "scope", "policy", "reason", "model", "endpoint", "source", "count", "created"}).AddRow(11, 7, 8, nil, "req", "session", "session_block", "sexual", "model", "/v1/responses", "content_moderation", 1, time.Now()))
	page, err := s.ListEvents(context.Background(), 7, 1, 10)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.Empty(t, page.Items[0].FullPrompt)
	require.NoError(t, m.ExpectationsWereMet())
}

func TestSecurityPenaltyRouteRequiresTarget(t *testing.T) {
	cfg := penaltyTestConfig()
	cfg.Policy = "session_route_then_block"
	require.Error(t, validateSecurityPenaltyConfig(cfg))
	target := int64(20)
	cfg.TargetGroupID = &target
	require.NoError(t, validateSecurityPenaltyConfig(cfg))
}

func TestSecurityPenaltyIdentitySpansKeysButNotUsersAndDoesNotGuess(t *testing.T) {
	first := SecurityPenaltySessionKey(7, "client-session")
	require.NotEmpty(t, first)
	require.Equal(t, first, SecurityPenaltySessionKey(7, "client-session"), "API key is deliberately absent from the scope")
	require.NotEqual(t, first, SecurityPenaltySessionKey(8, "client-session"))
	require.Empty(t, SecurityPenaltySessionKey(7, ""))
	require.NotContains(t, first, "client-session")
}

func TestSecurityPenaltyDoesNotAcceptGrokOrUpstreamStatusAsFinding(t *testing.T) {
	s, m := penaltyTestService(t, penaltyTestConfig())
	m.ExpectBegin()
	m.ExpectRollback()
	err := s.RecordViolation(context.Background(), SecurityPenaltyViolation{UserID: 7, APIKeyID: 8, RequestID: "request-one", SessionKey: "session", Source: "grok_403", Reason: "forbidden"})
	require.Error(t, err)
	require.NoError(t, m.ExpectationsWereMet())
}

func TestSecurityPenaltyObserveAndDisabledDoNotOwnLegacyAutoBan(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		enabled, observe, want bool
	}{
		{"default off", false, true, false}, {"observing", true, true, false}, {"enforcing", true, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := penaltyTestConfig()
			cfg.Enabled = tc.enabled
			cfg.ObserveOnly = tc.observe
			s, m := penaltyTestService(t, cfg)
			require.Equal(t, tc.want, s.OwnsLegacyEnforcement(context.Background(), 7, nil))
			require.NoError(t, m.ExpectationsWereMet(), "ownership must not perform a runtime settings read")
		})
	}
}

func TestSecurityPenaltySnapshotDisabledHasNoDatabaseDependencyAndStaleEnforcementFailsClosed(t *testing.T) {
	off := NewSecurityPenaltyService(nil, nil)
	cfg, err := off.RuntimeConfig()
	require.NoError(t, err)
	require.False(t, cfg.Enabled)
	admission, err := off.Admission(context.Background(), 7, "session")
	require.NoError(t, err)
	require.False(t, admission.Blocked)
	on, _ := penaltyTestService(t, penaltyTestConfig())
	on.expires = time.Now().Add(-time.Minute)
	_, err = on.Admission(context.Background(), 7, "session")
	require.Error(t, err)
	// Keep website-login side effects disabled while execution policy is stale;
	// the gateway fails closed instead of falling back to a users.status ban.
	require.True(t, on.OwnsLegacyEnforcement(context.Background(), 7, nil))
}

func TestSecurityPenaltyOnlyExplicitEnforcementReplacesLegacyLoginBan(t *testing.T) {
	for _, observe := range []bool{true, false} {
		t.Run(map[bool]string{true: "observe preserves old autoban", false: "execution preserves login"}[observe], func(t *testing.T) {
			policy := penaltyTestConfig()
			policy.ObserveOnly = observe
			p, m := penaltyTestService(t, policy)
			users := &contentModerationTestUserRepo{user: &User{ID: 7, Role: RoleUser, Status: StatusActive}}
			svc := &ContentModerationService{securityPenalties: p, userRepo: users}
			uid := int64(7)
			log := &ContentModerationLog{UserID: &uid, RequestID: "request-fixture", Flagged: true, Action: ContentModerationActionBlock}
			m.ExpectBegin()
			m.ExpectQuery("SELECT session_key FROM security_penalty_request_context").WithArgs(int64(7), "request-fixture").WillReturnError(sql.ErrNoRows)
			m.ExpectExec("SELECT pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 1))
			m.ExpectQuery("INSERT INTO security_penalty_events").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
			m.ExpectCommit()
			svc.persistContentModerationLog(context.Background(), &ContentModerationConfig{AutoBanEnabled: true, BanThreshold: 1}, log, "", false, true)
			if observe {
				require.Equal(t, StatusDisabled, users.user.Status)
			} else {
				require.Equal(t, StatusActive, users.user.Status)
				require.Empty(t, users.updated)
			}
			require.NoError(t, m.ExpectationsWereMet())
		})
	}
}
