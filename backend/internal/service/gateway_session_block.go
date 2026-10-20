package service

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

// BlockModerationRefusedSession records that a conversation was refused by the
// provider on safety grounds. Later requests carrying the same session hash are
// rejected before account selection.
//
// Failures are logged and swallowed: a cache problem must never turn a billable
// refusal into a 5xx for the client.
func (s *GatewayService) BlockModerationRefusedSession(ctx context.Context, groupID *int64, sessionHash string, ttl time.Duration) {
	if s == nil || s.cache == nil || sessionHash == "" || ttl <= 0 {
		return
	}
	gid := int64(0)
	if groupID != nil {
		gid = *groupID
	}
	if err := s.cache.BlockSession(ctx, gid, sessionHash, ttl); err != nil {
		logger.L().Warn("gateway.block_session_failed",
			zap.Int64("group_id", gid),
			zap.Error(err),
		)
	}
}

// ModerationRefusedSessionBlocked reports whether this conversation was already
// refused on safety grounds.
func (s *GatewayService) ModerationRefusedSessionBlocked(ctx context.Context, groupID *int64, sessionHash string) bool {
	if s == nil || s.cache == nil || sessionHash == "" {
		return false
	}
	gid := int64(0)
	if groupID != nil {
		gid = *groupID
	}
	blocked, err := s.cache.IsSessionBlocked(ctx, gid, sessionHash)
	if err != nil {
		logger.L().Warn("gateway.session_block_check_failed",
			zap.Int64("group_id", gid),
			zap.Error(err),
		)
		return false
	}
	return blocked
}
