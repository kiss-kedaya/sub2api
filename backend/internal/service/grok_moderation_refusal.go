package service

import (
	"context"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

const grokModerationRefusalBillingCost = 0.1
const grokModerationRefusalText = "i'm sorry, i can't help with that request."

// grokModerationRefusalSessionBlockTTL is how long a conversation stays blocked
// after the provider refused it on safety grounds. The block exists so a client
// hammering the same prompt stops burning upstream quota; it is not a permanent
// ban, so it expires.
const grokModerationRefusalSessionBlockTTL = 24 * time.Hour

// IsGrokModerationRefusal reports the provider-specific refusal signature that
// must be surfaced unchanged and settled as a billable request. Keep this
// narrow: it must not affect other platforms or ordinary upstream errors.
func IsGrokModerationRefusal(platform string, body []byte) bool {
	if !strings.EqualFold(strings.TrimSpace(platform), PlatformGrok) {
		return false
	}
	text := strings.ToLower(string(body))
	return strings.Contains(text, grokModerationRefusalText)
}

func GrokModerationRefusalBillingCost() float64 { return grokModerationRefusalBillingCost }

// GrokModerationRefusalBilledCost applies the resolved group/user rate
// multiplier to the nominal refusal fee, mirroring token billing (0.07x pays
// 0.007, 1.0x pays the full 0.1).
func GrokModerationRefusalBilledCost(multiplier float64) float64 {
	if multiplier < 0 {
		multiplier = 0
	}
	return grokModerationRefusalBillingCost * multiplier
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
	if s == nil || s.cache == nil || sessionHash == "" || ttl <= 0 {
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
	if billedCost > 0 {
		result.ForcedBillingCost = billedCost
	} else {
		result.ForcedBillingCost = GrokModerationRefusalBillingCost()
	}
	result.ForceBalanceBilling = true
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
	if billedCost > 0 {
		result.ForcedBillingCost = billedCost
	} else {
		result.ForcedBillingCost = GrokModerationRefusalBillingCost()
	}
	result.ForceBalanceBilling = true
	return result
}
