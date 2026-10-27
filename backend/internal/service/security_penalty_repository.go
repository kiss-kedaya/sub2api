// Adapted from ranxi2001/sub2api @ 9620d315, LGPL-3.0-or-later.
package service

import (
	"context"
	"database/sql"
	"errors"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const penaltyStateColumns = "user_id,scope,subject_key,policy,strike_count,window_started_at,blocked_until,route_until,updated_at"

func scanPenaltyState(row interface{ Scan(...any) error }) (SecurityPenaltyState, error) {
	var state SecurityPenaltyState
	err := row.Scan(&state.UserID, &state.Scope, &state.SubjectKey, &state.Policy, &state.Count, &state.WindowStartedAt, &state.BlockedUntil, &state.RouteUntil, &state.UpdatedAt)
	return state, err
}

func (s *SecurityPenaltyService) RecordViolation(ctx context.Context, v SecurityPenaltyViolation) error {
	if s == nil {
		return nil
	}
	cfg, err := s.RuntimeConfig()
	if err != nil {
		return err
	}
	if !cfg.Applies(v.UserID) {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = s.RecordViolationTx(ctx, tx, v, cfg); err != nil {
		return err
	}
	return tx.Commit()
}

// RecordViolationTx lets an asynchronous Guard finding and its strike commit
// together under the worker's existing lease-fenced transaction.
func (s *SecurityPenaltyService) RecordViolationTx(ctx context.Context, tx *sql.Tx, v SecurityPenaltyViolation, cfg SecurityPenaltyConfig) error {
	var err error
	if !cfg.Applies(v.UserID) {
		return nil
	}
	if !cfg.AllGroups {
		included := false
		for _, id := range cfg.GroupIDs {
			if v.GroupID != nil && *v.GroupID == id {
				included = true
				break
			}
		}
		if !included {
			return nil
		}
	}
	if v.RequestID == "" || (v.Source != "content_moderation" && v.Source != "prompt_audit") {
		return errors.New("invalid security penalty violation identity")
	}
	if v.SessionKey == "" {
		err = tx.QueryRowContext(ctx, `SELECT session_key FROM security_penalty_request_context WHERE user_id=$1 AND request_id=$2 AND expires_at>NOW()`, v.UserID, v.RequestID).Scan(&v.SessionKey)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtextextended('security-penalty-user:' || $1::text,0))", v.UserID); err != nil {
		return err
	}
	scope, key := penaltySubject(cfg, v.SessionKey)
	now := time.Now().UTC()
	state := SecurityPenaltyState{UserID: v.UserID, Scope: scope, SubjectKey: key, Policy: cfg.Policy, WindowStartedAt: now}
	if scope != "request" {
		_, err = tx.ExecContext(ctx, `INSERT INTO security_penalty_subjects(user_id,scope,subject_key,policy) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, v.UserID, scope, key, cfg.Policy)
		if err != nil {
			return err
		}
		state, err = scanPenaltyState(tx.QueryRowContext(ctx, "SELECT "+penaltyStateColumns+" FROM security_penalty_subjects WHERE user_id=$1 AND scope=$2 AND subject_key=$3 AND policy=$4 FOR UPDATE", v.UserID, scope, key, cfg.Policy))
		if err != nil {
			return err
		}
		if penaltyAction(cfg, state, now) == "none" {
			state.Count = 0
			state.WindowStartedAt = now
			state.BlockedUntil = nil
			state.RouteUntil = nil
		}
		if state.Count < 1000000 {
			state.Count++
		}
	}
	var eventID int64
	// Keep identifiers and bounded reason only. The existing audit owns prompt
	// evidence; do not duplicate private conversations into a second ledger.
	prompt := ""
	err = tx.QueryRowContext(ctx, `INSERT INTO security_penalty_events(user_id,api_key_id,group_id,request_id,session_key,source,scope,policy,reason,full_prompt,model,endpoint,strike_count)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) ON CONFLICT(user_id,request_id) DO NOTHING RETURNING id`, v.UserID, v.APIKeyID, v.GroupID, v.RequestID, v.SessionKey, v.Source, scope, cfg.Policy, securityPenaltyReason(v.Reason), prompt, v.Model, v.Endpoint, state.Count).Scan(&eventID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	} // Same request/WS turn is never counted twice.
	if err != nil {
		return err
	}
	if scope == "request" {
		return nil
	} // Missing identity never widens to a whole-user block.
	until := now.Add(time.Duration(cfg.DurationHours) * time.Hour)
	if state.Count >= cfg.Threshold && state.BlockedUntil == nil {
		state.BlockedUntil = &until
		state.RouteUntil = nil
	}
	if state.Count < cfg.Threshold && cfg.Policy == "session_route_then_block" && (state.RouteUntil == nil || !now.Before(*state.RouteUntil)) {
		state.RouteUntil = &until
	}
	_, err = tx.ExecContext(ctx, `UPDATE security_penalty_subjects SET strike_count=$5,window_started_at=$6,blocked_until=$7,route_until=$8,updated_at=$9 WHERE user_id=$1 AND scope=$2 AND subject_key=$3 AND policy=$4`, v.UserID, scope, key, cfg.Policy, state.Count, state.WindowStartedAt, state.BlockedUntil, state.RouteUntil, now)
	return err
}

// BindRequestContext retains only a one-way identity until asynchronous audit
// completes. No body/header/secret enters this table. Server request IDs dedupe.
func (s *SecurityPenaltyService) BindRequestContext(ctx context.Context, userID int64, requestID, sessionKey string) error {
	if requestID == "" || len(requestID) > 256 || len(sessionKey) > 64 || userID <= 0 {
		return errors.New("invalid penalty request identity")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO security_penalty_request_context(user_id,request_id,session_key,expires_at) VALUES($1,$2,$3,NOW()+INTERVAL '7 days') ON CONFLICT(user_id,request_id) DO NOTHING`, userID, requestID, sessionKey)
	return err
}

type SecurityPenaltyEvent struct {
	ID         int64     `json:"id"`
	UserID     int64     `json:"user_id"`
	APIKeyID   int64     `json:"api_key_id"`
	GroupID    *int64    `json:"group_id"`
	RequestID  string    `json:"request_id"`
	Scope      string    `json:"scope"`
	Policy     string    `json:"policy"`
	Reason     string    `json:"reason"`
	FullPrompt string    `json:"full_prompt,omitempty"`
	Model      string    `json:"model"`
	Endpoint   string    `json:"endpoint"`
	Source     string    `json:"source"`
	Count      int       `json:"count"`
	CreatedAt  time.Time `json:"created_at"`
}

type SecurityPenaltyEventPage struct {
	Items    []SecurityPenaltyEvent `json:"items"`
	Total    int64                  `json:"total"`
	Page     int                    `json:"page"`
	PageSize int                    `json:"page_size"`
}

type SecurityPenaltySummary struct {
	Enabled     bool                   `json:"enabled"`
	ObserveOnly bool                   `json:"observe_only"`
	Policy      string                 `json:"policy"`
	Threshold   int                    `json:"threshold"`
	Restricted  bool                   `json:"restricted"`
	States      []SecurityPenaltyState `json:"states"`
}

func (s *SecurityPenaltyService) Summary(ctx context.Context, userID int64) (SecurityPenaltySummary, error) {
	cfg, err := s.GetConfig(ctx)
	if err != nil {
		return SecurityPenaltySummary{}, err
	}
	result := SecurityPenaltySummary{Enabled: cfg.Applies(userID), ObserveOnly: cfg.ObserveOnly, Policy: cfg.Policy, Threshold: cfg.Threshold, States: []SecurityPenaltyState{}}
	rows, err := s.db.QueryContext(ctx, "SELECT "+penaltyStateColumns+" FROM security_penalty_subjects WHERE user_id=$1 AND policy=$2 ORDER BY updated_at DESC LIMIT 100", userID, cfg.Policy)
	if err != nil {
		return result, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		state, err := scanPenaltyState(rows)
		if err != nil {
			return result, err
		}
		state.Action = penaltyAction(cfg, state, time.Now())
		if state.Action == "none" {
			continue
		}
		if !cfg.Applies(userID) || cfg.ObserveOnly {
			state.Action = "observe"
		}
		if state.Action == "block" || state.Action == "route" {
			result.Restricted = true
		}
		result.States = append(result.States, state)
	}
	if err = rows.Err(); err != nil {
		return result, err
	}
	// State rows are bounded; query reasons after closing the cursor so a
	// single-connection database pool remains usable.
	if err = rows.Close(); err != nil {
		return result, err
	}
	for i := range result.States {
		state := &result.States[i]
		err = s.db.QueryRowContext(ctx, `SELECT reason FROM security_penalty_events WHERE user_id=$1 AND scope=$2 AND policy=$3 AND ($2='user' OR session_key=$4) ORDER BY id DESC LIMIT 1`, userID, state.Scope, state.Policy, state.SubjectKey).Scan(&state.Reason)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return result, err
		}
	}
	if result.Enabled && !cfg.ObserveOnly {
		err = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM security_penalty_subjects WHERE user_id=$1 AND policy=$2 AND strike_count>0 AND (blocked_until>NOW() OR ($2='session_route_then_block' AND blocked_until IS NULL AND route_until>NOW() AND window_started_at>NOW()-make_interval(hours => $3::int))))`, userID, cfg.Policy, cfg.WindowHours).Scan(&result.Restricted)
		if err != nil {
			return result, err
		}
	}
	return result, nil
}

func (s *SecurityPenaltyService) ListEvents(ctx context.Context, userID int64, page, size int) (SecurityPenaltyEventPage, error) {
	if page < 1 || page > 100000 || size < 1 || size > 100 {
		return SecurityPenaltyEventPage{}, infraerrors.BadRequest("security_penalty_invalid_page", "分页参数无效")
	}
	result := SecurityPenaltyEventPage{Items: []SecurityPenaltyEvent{}, Page: page, PageSize: size}
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM security_penalty_events WHERE ($1::bigint=0 OR user_id=$1)", userID).Scan(&result.Total); err != nil {
		return result, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,user_id,api_key_id,group_id,request_id,scope,policy,reason,model,endpoint,source,strike_count,created_at FROM security_penalty_events WHERE ($1::bigint=0 OR user_id=$1) ORDER BY id DESC LIMIT $2 OFFSET $3`, userID, size, (page-1)*size)
	if err != nil {
		return result, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var item SecurityPenaltyEvent
		if err = rows.Scan(&item.ID, &item.UserID, &item.APIKeyID, &item.GroupID, &item.RequestID, &item.Scope, &item.Policy, &item.Reason, &item.Model, &item.Endpoint, &item.Source, &item.Count, &item.CreatedAt); err != nil {
			return result, err
		}
		result.Items = append(result.Items, item)
	}
	return result, rows.Err()
}

// Ownership is in the SQL predicate, never supplied by a user query parameter.
func (s *SecurityPenaltyService) GetEvent(ctx context.Context, userID, id int64) (SecurityPenaltyEvent, error) {
	var item SecurityPenaltyEvent
	err := s.db.QueryRowContext(ctx, `SELECT id,user_id,api_key_id,group_id,request_id,scope,policy,reason,full_prompt,model,endpoint,source,strike_count,created_at FROM security_penalty_events WHERE id=$1 AND ($2::bigint=0 OR user_id=$2)`, id, userID).Scan(&item.ID, &item.UserID, &item.APIKeyID, &item.GroupID, &item.RequestID, &item.Scope, &item.Policy, &item.Reason, &item.FullPrompt, &item.Model, &item.Endpoint, &item.Source, &item.Count, &item.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return item, infraerrors.NotFound("security_penalty_event_not_found", "安全记录不存在")
	}
	return item, err
}

func (s *SecurityPenaltyService) Release(ctx context.Context, userID, actorID int64, reason string) error {
	reason = securityPenaltyReason(reason)
	if userID <= 0 || actorID <= 0 || reason == "" {
		return infraerrors.BadRequest("security_penalty_release_reason_required", "请填写解除原因")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtextextended('security-penalty-user:' || $1::text,0))", userID); err != nil {
		return err
	}
	// Serialize resets with new strikes, including previously unseen sessions.
	if _, err = tx.ExecContext(ctx, `UPDATE security_penalty_subjects SET strike_count=0,window_started_at=NOW(),blocked_until=NULL,route_until=NULL,updated_at=NOW() WHERE user_id=$1`, userID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO security_penalty_releases(user_id,actor_id,reason) VALUES($1,$2,$3)", userID, actorID, reason); err != nil {
		return err
	}
	return tx.Commit()
}
