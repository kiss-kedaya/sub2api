package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

const grokModerationRefusalBillingCost = 0.1
const grokModerationRefusalText = "i'm sorry, i can't help with that request."
const globalIPBlacklistContextKey = "grok_global_ip_blacklist_rejection"

func MarkGlobalIPBlacklistRejection(c *gin.Context) {
	if c != nil {
		c.Set(globalIPBlacklistContextKey, true)
	}
}

func IsGlobalIPBlacklistRejectionMarked(c *gin.Context) bool {
	if c == nil {
		return false
	}
	marked, _ := c.Get(globalIPBlacklistContextKey)
	rejected, _ := marked.(bool)
	return rejected
}

func IsGlobalIPBlacklistResponse(body []byte) bool {
	return strings.Contains(strings.ToLower(string(body)), "blocked by global ip blacklist")
}

func IsGlobalIPBlacklistRejection(err error) bool {
	if err == nil {
		return false
	}
	var upstreamErr *UpstreamFailoverError
	if errors.As(err, &upstreamErr) && IsGlobalIPBlacklistResponse(upstreamErr.ResponseBody) {
		return true
	}
	return IsGlobalIPBlacklistResponse([]byte(err.Error()))
}

func GlobalIPBlacklistSessionBlockedMessage() string {
	return "This conversation was blocked after an upstream refusal. Please start a new conversation."
}

func newGlobalIPBlacklistFailoverError(status int, headers http.Header, body []byte) *UpstreamFailoverError {
	return &UpstreamFailoverError{
		StatusCode:        status,
		ResponseBody:      append([]byte(nil), body...),
		ResponseHeaders:   headers.Clone(),
		Scope:             GatewayFailureScopeRequest,
		Stage:             GatewayFailureStageInference,
		NextAccountAction: NextAccountStop,
		ClientStatusCode:  http.StatusForbidden,
		ClientMessage:     GlobalIPBlacklistSessionBlockedMessage(),
	}
}

// Both refusal classes block the same authenticated conversation. IP failures
// do not receive a moderation fee; only their persistent admission gate is shared.
func (s *OpenAIGatewayService) BlockGlobalIPBlacklistSession(ctx context.Context, c *gin.Context, key *APIKey, body []byte) {
	s.BlockGrokModerationRefusedRequest(ctx, c, key, body)
}

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

func IsGrokModerationRefusalResponse(platform string, statusCode int, body []byte) bool {
	return statusCode == http.StatusForbidden && IsGrokModerationRefusal(platform, body)
}

// Transport failures must carry an actual 403 before they can trigger a fee.
// A refusal sentence alone can also occur in a provider's 429/5xx response.
func IsGrokModerationRefusalFailure(platform string, body []byte, err error) bool {
	if IsGrokPlatformContentPolicyRejection(platform, err) {
		return true
	}
	var upstreamErr *UpstreamFailoverError
	if errors.As(err, &upstreamErr) {
		return IsGrokModerationRefusalResponse(platform, upstreamErr.StatusCode, upstreamErr.ResponseBody)
	}
	if err != nil {
		return false
	}
	for _, path := range []string{"status", "status_code", "error.status_code", "response.error.status_code"} {
		if status := int(gjson.GetBytes(body, path).Int()); status != 0 {
			return IsGrokModerationRefusalResponse(platform, status, body)
		}
	}
	return false
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
	seed := explicitOpenAIHeaderSessionID(c)
	if seed == "" && c != nil {
		seed = strings.TrimSpace(c.GetHeader(grokConversationIDHeader))
	}
	if seed == "" {
		seed = grokPreviousResponseSessionSeed(body)
	}
	if seed == "" {
		seed = ClaudeCodeSessionIDFromHeader(c)
	}
	if seed == "" {
		if parsed := ParseMetadataUserID(gjson.GetBytes(body, "metadata.user_id").String()); parsed != nil {
			seed = parsed.SessionID
		}
	}
	if seed == "" {
		return ""
	}
	return DeriveSessionHashFromSeed(fmt.Sprintf("grok-moderation:%d:%s", userID, seed))
}

// Clients without an explicit conversation id are matched against the refused
// history and its continuations. Reuse the bounded transcript matcher instead
// of the first-user routing seed, which could permanently block unrelated
// conversations starting with the same greeting.
func grokModerationTranscriptKeys(apiKey *APIKey, body []byte, write bool) []string {
	uid := balancePreauthorizationAPIKeyUserID(apiKey)
	if uid <= 0 || !hasOpenAIContentSessionUserAnchor([]byte(openAIRequestPayloadView(body).Raw)) {
		return nil
	}
	transcript := deriveOpenAICyberTranscriptBlockKeys(uid, body)
	keys := transcript.lookupKeys
	if write && len(keys) > 0 {
		keys = []string{keys[len(keys)-1]}
		if transcript.preLatestUserKey != "" {
			keys = append(keys, transcript.preLatestUserKey)
		}
	}
	for i, key := range keys {
		keys[i] = "grok-moderation-transcript:" + key
	}
	return keys
}

func (s *OpenAIGatewayService) BlockGrokModerationRefusedRequest(ctx context.Context, c *gin.Context, apiKey *APIKey, body []byte) {
	if hash := GrokModerationSessionHash(c, apiKey, body); hash != "" {
		s.BlockModerationRefusedSessionForOpenAI(ctx, nil, hash, grokModerationRefusalSessionBlockTTL)
		return
	}
	if store := s.cyberSessionBlockStore(); store != nil {
		if err := store.SetCyberSessionBlocked(ctx, grokModerationTranscriptScope(apiKey), grokModerationTranscriptKeys(apiKey, body, true), 0); err != nil {
			logger.L().Warn("openai_gateway.moderation_transcript_block_failed", zap.Error(err))
		}
	}
}

func (s *OpenAIGatewayService) GrokModerationRefusedRequestBlocked(ctx context.Context, c *gin.Context, apiKey *APIKey, body []byte) bool {
	if hash := GrokModerationSessionHash(c, apiKey, body); hash != "" {
		return s.ModerationRefusedSessionBlocked(ctx, nil, hash)
	}
	if store := s.cyberSessionBlockStore(); store != nil {
		active, err := store.IsCyberSessionScopeActive(ctx, grokModerationTranscriptScope(apiKey))
		if err != nil || !active {
			return false
		}
		key, err := store.FindCyberSessionBlocked(ctx, grokModerationTranscriptKeys(apiKey, body, false))
		if err != nil {
			logger.L().Warn("openai_gateway.moderation_transcript_lookup_failed", zap.Error(err))
		}
		return err == nil && key != ""
	}
	return false
}

func grokModerationTranscriptScope(apiKey *APIKey) string {
	return fmt.Sprintf("grok-moderation-user:%d", balancePreauthorizationAPIKeyUserID(apiKey))
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
	result.NonBillableUpstreamError = billedCost <= 0
	// billedCost is the already-resolved per-refusal fee. 0 means the group
	// explicitly disabled all billing, including any reported token usage.
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
	result.NonBillableUpstreamError = billedCost <= 0
	result.ForcedBillingCost = billedCost
	result.ForceBalanceBilling = billedCost > 0
	return result
}
