package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
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

// Both refusal classes block the same authenticated conversation. Grok's actual
// 403 IP blacklist responses share its fixed fee; other platforms do not bill.
func (s *OpenAIGatewayService) BlockGlobalIPBlacklistSession(ctx context.Context, c *gin.Context, key *APIKey, body []byte) {
	s.BlockGrokModerationRefusedRequest(ctx, c, key, body)
}

// A refused conversation stays blocked. Redis TTL 0 persists the block so
// resuming the same conversation later cannot create another provider charge.
const grokModerationRefusalSessionBlockTTL time.Duration = 0

// grokModerationSessionBlockEnabled controls the identifier-based conversation
// lock: the one keyed on a client-supplied id (x-grok-conv-id, session_id, a
// Claude Code session, metadata.user_id) or on the routing session hash. It is
// off (owner decision 2026-11-18). Those ids are not one conversation: relays
// reuse a single x-grok-conv-id for most of a customer's traffic, so one
// refusal was followed by hundreds of locally rejected requests from the same
// user, new conversations included, and the lock never expires. Locks of this
// kind written by earlier versions are ignored. Tests switch it on to keep the
// remaining code covered.
//
// What stays on is the content lock (see BlockGrokModerationRefusedRequest):
// it matches the refused transcript itself, so only resending or continuing
// that exact conversation is stopped.
var grokModerationSessionBlockEnabled atomic.Bool

// SetGrokModerationSessionBlockEnabled switches the conversation lock and
// returns a function that restores the previous setting.
func SetGrokModerationSessionBlockEnabled(enabled bool) (restore func()) {
	previous := grokModerationSessionBlockEnabled.Swap(enabled)
	return func() { grokModerationSessionBlockEnabled.Store(previous) }
}

// IsGrokModerationRefusal reports the provider-specific refusal signature that
// must be surfaced unchanged and settled as a billable request. Keep this
// narrow: it must not affect other platforms or ordinary upstream errors.
func IsGrokModerationRefusal(platform string, body []byte) bool {
	if !strings.EqualFold(strings.TrimSpace(platform), PlatformGrok) {
		return false
	}
	if isGrokAccountAccessFailure(body) {
		return false
	}
	text := strings.ToLower(string(body))
	return strings.Contains(text, grokModerationRefusalText) || isGrokContentPolicyRejection(403, body) || IsGlobalIPBlacklistResponse(body)
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
	return IsGrokModerationRefusalResponse(platform, grokModerationRefusalProtocolStatus(body), body)
}

func grokModerationRefusalProtocolStatus(body []byte) int {
	for _, path := range []string{"status", "status_code", "error.status_code", "response.error.status_code"} {
		result := gjson.GetBytes(body, path)
		if result.Type != gjson.Number {
			continue
		}
		status, err := strconv.Atoi(result.Raw)
		if err == nil && status != 0 {
			return status
		}
	}
	return 0
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
	if uid <= 0 || len(body) == 0 {
		return nil
	}
	transcript := deriveOpenAICyberTranscriptBlockKeysCore(uid, body, true, write, "grok-moderation-transcript:")
	if len(transcript.lookupKeys) == 0 {
		return nil
	}
	if write && transcript.preLatestUserKey != "" {
		return []string{transcript.lookupKeys[0], transcript.preLatestUserKey}
	}
	return transcript.lookupKeys
}

// grokModerationTranscriptBlockTTL bounds how long a refused transcript stays
// locked. A day stops a client that keeps resending the refused content from
// paying the refusal fee on every attempt, and lets a mistaken match heal.
const grokModerationTranscriptBlockTTL = 24 * time.Hour

// PrecomputeGrokModerationTranscriptKeys derives transcript block keys before
// releasing the request body mid-stream on Grok accounts.
func PrecomputeGrokModerationTranscriptKeys(apiKey *APIKey, body []byte) []string {
	return grokModerationTranscriptKeys(apiKey, body, true)
}

// BlockGrokModerationRefusedKeys locks a refused conversation using
// precomputed transcript keys when the request body was already released.
func (s *OpenAIGatewayService) BlockGrokModerationRefusedKeys(ctx context.Context, apiKey *APIKey, keys []string) {
	store := s.cyberSessionBlockStore()
	if store == nil || len(keys) == 0 {
		return
	}
	if err := store.SetCyberSessionBlocked(ctx, grokModerationTranscriptScope(apiKey), keys, grokModerationTranscriptBlockTTL); err != nil {
		logger.L().Warn("openai_gateway.moderation_transcript_block_failed", zap.Error(err))
	}
}

// BlockGrokModerationRefusedRequest locks the refused conversation by its
// content: the transcript up to and including the refused turn and, when the
// history holds model output, the history right before that turn. Client
// identifiers are deliberately not used; see grokModerationSessionBlockEnabled.
func (s *OpenAIGatewayService) BlockGrokModerationRefusedRequest(ctx context.Context, _ *gin.Context, apiKey *APIKey, body []byte) {
	keys := grokModerationTranscriptKeys(apiKey, body, true)
	s.BlockGrokModerationRefusedKeys(ctx, apiKey, keys)
}

// GrokModerationRefusedRequestBlocked reports whether this request resends or
// continues a transcript the provider refused. A request with other content is
// never blocked, whatever identifiers it carries.
func (s *OpenAIGatewayService) GrokModerationRefusedRequestBlocked(ctx context.Context, _ *gin.Context, apiKey *APIKey, body []byte) bool {
	store := s.cyberSessionBlockStore()
	if store == nil {
		return false
	}
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
