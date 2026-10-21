package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"go.uber.org/zap"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

const grokModerationRefusalBillingCost = 0.1
const grokModerationRefusalText = "i'm sorry, i can't help with that request."

// A refused conversation stays blocked. Redis TTL 0 persists the block so
// resuming the same conversation later cannot create another provider charge.
const grokModerationRefusalSessionBlockTTL time.Duration = 0

// IsGrokModerationRefusal reports the provider-specific refusal signature that
// must be surfaced unchanged and settled as a billable request. Keep this
// narrow: it must not affect other platforms or ordinary upstream errors.
func IsGrokModerationRefusal(platform string, body []byte) bool {
	if !strings.EqualFold(strings.TrimSpace(platform), PlatformGrok) {
		return false
	}
	text := strings.ToLower(string(body))
	return strings.Contains(text, grokModerationRefusalText) || isGrokContentPolicyRejection(403, body)
}

func GrokModerationRefusalBillingCost() float64 { return grokModerationRefusalBillingCost }

// GrokModerationRefusalBilledCost resolves the per-refusal fee for a group.
//
// The fee is deliberately NOT multiplied by the group rate multiplier: ops
// prices this penalty per occurrence. A nil group / nil override falls back to
// the built-in 0.1; an explicit 0 disables the charge.
func GrokModerationRefusalBilledCost(group *Group) float64 {
	if group != nil && group.GrokModerationRefusalPrice != nil {
		price := *group.GrokModerationRefusalPrice
		if price < 0 {
			return 0
		}
		return price
	}
	return grokModerationRefusalBillingCost
}

// grokContentPolicyRejectionPrefix is the error prefix emitted by
// handleCompatErrorResponse for platform-level Grok content-policy refusals.
// That branch returns a plain error (not UpstreamFailoverError), so handlers
// recognise it from the message.
const grokContentPolicyRejectionPrefix = "grok content policy rejection:"

// IsGrokPlatformContentPolicyRejection reports whether err came from the
// platform-level Grok content-policy branch (as opposed to other 403s).
func IsGrokPlatformContentPolicyRejection(platform string, err error) bool {
	if !strings.EqualFold(strings.TrimSpace(platform), PlatformGrok) || err == nil {
		return false
	}
	return strings.HasPrefix(
		strings.ToLower(strings.TrimSpace(err.Error())),
		grokContentPolicyRejectionPrefix,
	)
}

// GrokModerationRefusalMessage is the text returned to a client whose
// conversation was blocked after the provider refused it on safety grounds.
func GrokModerationRefusalMessage() string {
	return "I'm sorry, I can't help with that request. Failed check: SAFETY_CHECK_TYPE"
}

// GrokModerationRefusalSessionBlockTTL exposes the block duration so handlers
// can apply it through the gateway cache.
func GrokModerationRefusalSessionBlockTTL() time.Duration {
	return grokModerationRefusalSessionBlockTTL
}

// BlockModerationRefusedSessionForOpenAI is the OpenAIGatewayService twin of
// GatewayService.BlockModerationRefusedSession: the two services hold separate
// GatewayCache handles.
func (s *OpenAIGatewayService) BlockModerationRefusedSessionForOpenAI(
	ctx context.Context,
	groupID *int64,
	sessionHash string,
	ttl time.Duration,
) {
	if s == nil || s.cache == nil || sessionHash == "" || ttl < 0 {
		return
	}
	gid := int64(0)
	if groupID != nil {
		gid = *groupID
	}
	if err := s.cache.BlockSession(ctx, gid, sessionHash, ttl); err != nil {
		logger.L().Warn("openai_gateway.block_session_failed",
			zap.Int64("group_id", gid),
			zap.Error(err),
		)
	}
}

// Use the conversation identity, not the model-scoped routing affinity. A
// refused conversation stays blocked if the client changes model or protocol.
// Scope explicit client identifiers to the authenticated user.
func GrokModerationSessionHash(c *gin.Context, apiKey *APIKey, body []byte) string {
	userID := balancePreauthorizationAPIKeyUserID(apiKey)
	if userID <= 0 {
		return ""
	}
	seed := explicitOpenAIRequestSessionID(c, body)
	if seed == "" {
		seed = ClaudeCodeSessionIDFromHeader(c)
	}
	if seed == "" {
		if parsed := ParseMetadataUserID(gjson.GetBytes(body, "metadata.user_id").String()); parsed != nil {
			seed = parsed.SessionID
		}
	}
	if seed == "" {
		// Routing includes the model; moderation identity must not. Require a
		// real user anchor to avoid blocking every model-only request.
		view := openAIRequestPayloadView(body)
		withoutModel, err := sjson.DeleteBytes([]byte(view.Raw), "model")
		if err == nil {
			seed = deriveOpenAIAnchoredContentSessionSeed(withoutModel)
		}
	}
	if seed == "" {
		return ""
	}
	return DeriveSessionHashFromSeed(fmt.Sprintf("grok-moderation:%d:%s", userID, seed))
}

func (s *OpenAIGatewayService) ModerationRefusedSessionBlocked(ctx context.Context, groupID *int64, sessionHash string) bool {
	if s == nil || s.cache == nil || sessionHash == "" {
		return false
	}
	gid := int64(0)
	if groupID != nil {
		gid = *groupID
	}
	blocked, err := s.cache.IsSessionBlocked(ctx, gid, sessionHash)
	if err != nil {
		logger.L().Warn("openai_gateway.session_block_check_failed", zap.Int64("group_id", gid), zap.Error(err))
		return false
	}
	return blocked
}

// PrepareGrokModerationRefusalResult creates the minimal usage record needed
// when the upstream rejected before producing a normal forward result.
func PrepareGrokModerationRefusalResult(result *ForwardResult, requestedModel string, billedCost float64) *ForwardResult {
	if result == nil {
		result = &ForwardResult{}
	}
	if strings.TrimSpace(result.Model) == "" {
		result.Model = strings.TrimSpace(requestedModel)
	}
	if strings.TrimSpace(result.UpstreamModel) == "" {
		result.UpstreamModel = result.Model
	}
	result.NonBillableUpstreamError = false
	// billedCost is the already-resolved per-refusal fee. 0 means the group
	// explicitly disabled the charge, so leave the forced cost unset.
	result.ForcedBillingCost = billedCost
	result.ForceBalanceBilling = billedCost > 0
	return result
}

// PrepareGrokModerationRefusalOpenAIResult is the OpenAI-gateway twin of
// PrepareGrokModerationRefusalResult.
func PrepareGrokModerationRefusalOpenAIResult(
	result *OpenAIForwardResult,
	requestedModel string,
	billedCost float64,
) *OpenAIForwardResult {
	if result == nil {
		result = &OpenAIForwardResult{}
	}
	if strings.TrimSpace(result.Model) == "" {
		result.Model = strings.TrimSpace(requestedModel)
	}
	if strings.TrimSpace(result.UpstreamModel) == "" {
		result.UpstreamModel = result.Model
	}
	result.NonBillableUpstreamError = false
	result.ForcedBillingCost = billedCost
	result.ForceBalanceBilling = billedCost > 0
	return result
}
