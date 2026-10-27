// Admission/strike separation adapted from ranxi2001/sub2api @ 9620d315,
// LGPL-3.0-or-later. Local audit decisions are reused; no additional scanner.
package securityaudit

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Wire this provider instead of NewCoordinator. The repository callback joins
// the existing lease-fenced prompt result transaction, including async jobs.
func ProvidePenaltyCoordinator(legacy LegacyEngine, prompt PromptEngine, penalties *service.SecurityPenaltyService) *Coordinator {
	c := NewCoordinator(legacy, prompt)
	c.penalties = penalties
	if p, ok := prompt.(*PromptService); ok && p.repo != nil {
		p.repo.prepareFinding = func(ctx context.Context) (func(*sql.Tx, PromptSnapshot, *NormalizedResult) error, error) {
			cfg, err := penalties.RuntimeConfig()
			if err != nil {
				return nil, err
			}
			return func(tx *sql.Tx, snapshot PromptSnapshot, result *NormalizedResult) error {
				if result == nil || result.Action != ActionBlock {
					return nil
				}
				return penalties.RecordViolationTx(ctx, tx, service.SecurityPenaltyViolation{UserID: snapshot.UserID, APIKeyID: snapshot.APIKeyID, GroupID: snapshot.GroupID, RequestID: snapshot.RequestID, Source: "prompt_audit", Reason: "prompt_audit_blocked", Model: snapshot.Model, Endpoint: snapshot.Endpoint}, cfg)
			}, nil
		}
	}
	return c
}

func (c *Coordinator) Check(ctx context.Context, req Request) Decision {
	if c == nil || c.penalties == nil {
		return c.checkWithoutPenalty(ctx, req)
	}
	cfg, err := c.penalties.RuntimeConfig()
	if err != nil {
		return penaltyUnavailableDecision()
	}
	if !cfg.Applies(req.UserID) {
		return c.checkWithoutPenalty(ctx, req)
	}
	session := service.SecurityPenaltySessionKey(req.UserID, penaltyRequestSessionIdentity(req))
	admission, err := c.penalties.Admission(ctx, req.UserID, session)
	if err != nil {
		return penaltyUnavailableDecision()
	}
	if admission.Blocked {
		return penaltyBlockedDecision()
	}
	// Carry the restriction to the actual selected group. The entry group may
	// differ on a smart-routing key; the handler must validate the original ACL.
	finish := func() Decision {
		decision := c.checkWithoutPenalty(ctx, req)
		if admission.TargetGroupID != nil {
			id := *admission.TargetGroupID
			decision.RestrictedGroupID = &id
		}
		return decision
	}
	if !cfg.IncludesGroup(req.GroupID) {
		return finish()
	}
	if err = c.penalties.BindRequestContext(ctx, req.UserID, req.RequestID, session); err != nil {
		return penaltyUnavailableDecision()
	}
	return finish()
}

func penaltyBlockedDecision() Decision {
	return Decision{Kind: DecisionBlock, HTTPStatus: http.StatusForbidden, ErrorCode: "security_penalty_blocked", ClientMessage: "API requests are temporarily restricted; sign in to view your security records and expiry", AllowNextStage: false}
}
func penaltyUnavailableDecision() Decision {
	return Decision{Kind: DecisionUnavailable, HTTPStatus: http.StatusServiceUnavailable, ErrorCode: "security_penalty_unavailable", ClientMessage: "Request permission checks are temporarily unavailable", AllowNextStage: false}
}

func (r *PostgreSQLRepository) preparePenaltyFinding(ctx context.Context) (func(*sql.Tx, PromptSnapshot, *NormalizedResult) error, error) {
	if r.prepareFinding == nil {
		return nil, nil
	}
	return r.prepareFinding(ctx)
}

// Exported for the root composition's startup validation; no runtime I/O.
func ValidatePenaltyWiring(c *Coordinator) error {
	if c == nil || c.penalties == nil {
		return errors.New("security penalty coordinator is not configured")
	}
	return nil
}
