package service

import (
	"context"
	"log/slog"
)

// SetSecurityPenalties only connects already-confirmed local moderation hits.
// Grok protocol refusals, fixed fees and upstream errors never enter this path.
func (s *ContentModerationService) SetSecurityPenalties(penalties *SecurityPenaltyService) {
	s.securityPenalties = penalties
}

func (s *ContentModerationService) recordSecurityPenaltyFromModeration(ctx context.Context, log *ContentModerationLog) {
	if s == nil || s.securityPenalties == nil || log == nil || !log.Flagged || log.UserID == nil || log.RequestID == "" || log.Action == ContentModerationActionCyberPolicy {
		return
	}
	keyID := int64(0)
	if log.APIKeyID != nil {
		keyID = *log.APIKeyID
	}
	if err := s.securityPenalties.RecordViolation(ctx, SecurityPenaltyViolation{UserID: *log.UserID, APIKeyID: keyID, GroupID: log.GroupID, RequestID: log.RequestID, Source: "content_moderation", Reason: "content_policy_violation", Model: log.Model, Endpoint: log.Endpoint}); err != nil {
		slog.Warn("security_penalty.record_failed", "user_id", *log.UserID, "request_id", log.RequestID)
	}
}
