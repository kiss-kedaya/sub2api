package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// noAccountErrorClassification describes the HTTP response to emit when
// account selection failed with ErrNoAvailableAccounts. Handlers obtain it
// via classifyNoAccountError and choose between:
//
//   - 404 model_not_found — the group has accounts, but none of them are
//     configured to serve the requested model (config / typo / unsupported
//     model). Returning 503 here misleads operators and trips reverse-proxy
//     health checks; 404 lets the client surface the real problem.
//
//   - 503 api_error — accounts that could serve the model exist but are
//     temporarily exhausted (rate limit, quota auto-pause, runtime block) OR
//     the group has no accounts at all. Both stay on 503 because retrying
//     after a backoff can plausibly succeed (or, in the empty-pool case, the
//     operator may be in the middle of adding accounts).
type noAccountErrorClassification struct {
	Status  int
	ErrType string
	// Code is the protocol-level machine code surfaced to clients. It stays
	// empty for generic capacity errors and is set to MODEL_NOT_AVAILABLE when
	// the pool exists but no account can serve the requested model, so clients
	// and operators can tell a configuration mistake from transient capacity.
	Code    string
	Message string
	// ModelNotFound also acts as the existing non-capacity routing flag. A
	// plan-gated model uses it to prevent handlers from replacing the precise
	// 403 with a generic retryable capacity message.
	ModelNotFound bool
}

// modelNotAvailableCode is the stable machine code for "the group has accounts
// but none of them supports the requested model".
const modelNotAvailableCode = "MODEL_NOT_AVAILABLE"

// maxListedAvailableModels caps how many aliases a MODEL_NOT_AVAILABLE message
// quotes, so the error body stays readable when a group advertises hundreds.
const maxListedAvailableModels = 20

// noAvailableAccountsMessage is the only text an API caller sees for a
// capacity-type "no account" failure (503). The "No available accounts" prefix
// is kept on purpose: the ops error filter, the ops phase classifier and the
// channel-monitor taxonomy all key off it.
const noAvailableAccountsMessage = "No available accounts for this request. Please retry later."

// clientNoAccountMessage renders the caller-facing text for a classified
// "no account" failure. Scheduler internals (group id, pool counters, profit
// gate, protocol and cross-protocol switches, the raw selection error) must
// never be echoed to the client; handlers log them under the request id.
// Verdicts the classifier owns (404 / 403 model verdicts, 429 rate limit,
// security-penalty 403) already carry a client-safe Message and are kept as is.
func clientNoAccountMessage(cls noAccountErrorClassification) string {
	if cls.Status == http.StatusServiceUnavailable && !cls.ModelNotFound {
		return noAvailableAccountsMessage
	}
	return cls.Message
}

// modelNotAvailableMessage renders the user-facing MODEL_NOT_AVAILABLE text.
// availableModels comes from ModelAvailabilityDiagnosis, already narrowed to
// what the caller's group exposes (see groupVisibleModels), so a client asking
// for a model no account maps gets an actionable message instead of a
// retryable 503.
func modelNotAvailableMessage(displayModel string, availableModels []string) string {
	displayModel = strings.TrimSpace(displayModel)
	if displayModel == "" {
		displayModel = "unknown"
	}
	quoted := make([]string, 0, len(availableModels))
	for _, model := range availableModels {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		if len(quoted) == maxListedAvailableModels {
			break
		}
		quoted = append(quoted, fmt.Sprintf("%q", model))
	}
	if len(quoted) == 0 {
		return fmt.Sprintf("分组内没有账号支持模型 %q（当前分组没有可用的模型配置）", displayModel)
	}
	return fmt.Sprintf("分组内没有账号支持模型 %q（当前可用：%s）", displayModel, strings.Join(quoted, ", "))
}

// groupVisibleModels narrows the account model_mapping aliases a 404 may quote
// to what the key's group exposes. The diagnosis collects the mapping keys of
// every eligible account in the pool, which is wider than the group model
// allowlist: quoting it verbatim would reveal aliases the allowlist hides. It
// applies the same FilterForListing rule as /v1/models. When the group the
// diagnosis ran against cannot be established, no list is shown.
func groupVisibleModels(apiKey *service.APIKey, models []string) []string {
	if len(models) == 0 || apiKey == nil || apiKey.GroupID == nil ||
		apiKey.Group == nil || apiKey.Group.ID != *apiKey.GroupID {
		return nil
	}
	return apiKey.Group.ModelAllowlist.FilterForListing(models)
}

var selectionModelRateLimitedPattern = regexp.MustCompile(`(?:model_rate_limited|rate_limited)=(\d+)`)
var selectionPoolPattern = regexp.MustCompile(`pool=(\d+)`)
var selectionModelNotSupportedPattern = regexp.MustCompile(`model_not_supported=(\d+)`)
var selectionModelPlanGatedPattern = regexp.MustCompile(`model_plan_gated=(\d+)`)

func selectionFailureCount(pattern *regexp.Regexp, message string) int {
	match := pattern.FindStringSubmatch(strings.ToLower(message))
	if len(match) != 2 {
		return 0
	}
	count, err := strconv.Atoi(match[1])
	if err != nil || count <= 0 {
		return 0
	}
	return count
}

func selectionFailureDetails(message string) string {
	message = strings.TrimSpace(message)
	if !strings.HasSuffix(message, ")") {
		return ""
	}
	start := strings.LastIndex(message, "(")
	if start < 0 || start+1 >= len(message)-1 {
		return ""
	}
	return message[start+1 : len(message)-1]
}

// classifySelectionFailureError uses the scheduler-owned diagnostic suffix to
// distinguish real model rate limits from deterministic upstream model or plan
// rejection. It never parses the user-controlled model portion of the error.
func classifySelectionFailureError(err error, fallback noAccountErrorClassification) noAccountErrorClassification {
	if err == nil {
		return fallback
	}
	// A 404 model_not_found fallback is authoritative and must not be downgraded
	// to a rate-limit verdict. classifyNoAccountError only reaches it through
	// DiagnoseModelAvailabilityForPlatform, a dedicated database query over
	// persistent eligibility (active + schedulable + model_mapping) that already
	// established no account in the group can serve this model at all. A transient
	// per-model cooldown on one of the remaining candidates does not make "all
	// available accounts are rate-limited" true.
	//
	// Reporting 429 here is actively harmful: retrying can never succeed, and
	// clients that treat 429 as a rate limit retry hard and swallow the body
	// (Codex surfaces only "exceeded retry limit, last status: 429"), losing the
	// one message that names the real problem. It also flips the ops attribution
	// from a local model-configuration issue to routing capacity, because call
	// sites gate markOpsRoutingCapacityLimitedIfNoAvailable on ModelNotFound.
	if fallback.ModelNotFound {
		return fallback
	}
	details := selectionFailureDetails(err.Error())
	if details == "" {
		return fallback
	}
	if selectionFailureCount(selectionModelRateLimitedPattern, details) > 0 {
		return noAccountErrorClassification{
			Status:  http.StatusTooManyRequests,
			ErrType: "rate_limit_error",
			Message: "All available accounts are currently rate-limited. Please retry later.",
		}
	}

	pool := selectionFailureCount(selectionPoolPattern, details)
	unsupported := selectionFailureCount(selectionModelNotSupportedPattern, details)
	planGated := selectionFailureCount(selectionModelPlanGatedPattern, details)
	if pool <= 0 || unsupported+planGated < pool {
		return fallback
	}
	if planGated == pool {
		return noAccountErrorClassification{
			Status:        http.StatusForbidden,
			ErrType:       "permission_error",
			Message:       "The requested model is not available under the configured upstream plans.",
			ModelNotFound: true,
		}
	}
	if unsupported == pool {
		message := fallback.Message
		if strings.TrimSpace(message) == "" || message == "Service temporarily unavailable" {
			// 正常路径下 classifyNoAccountError 已经用数据库诊断给出带可用模型
			// 列表的精确文案（此时 fallback.ModelNotFound=true 会在上面提前返回）。
			// 走到这里说明调度快照与持久化配置不一致，给一句不含内部细节的兜底。
			message = "分组内没有账号支持该模型，请更换模型后重试"
		}
		return noAccountErrorClassification{
			Status:        http.StatusNotFound,
			ErrType:       "model_not_found",
			Code:          modelNotAvailableCode,
			Message:       message,
			ModelNotFound: true,
		}
	}
	return fallback
}

func classifySelectionFailureErrorFromGin(c *gin.Context, err error, fallback noAccountErrorClassification) noAccountErrorClassification {
	if errors.Is(err, service.ErrSecurityPenaltyGroupRestricted) {
		return noAccountErrorClassification{Status: http.StatusForbidden, ErrType: "security_penalty_group_restricted",
			Message: "This conversation is restricted to the configured group; review your security records"}
	}
	classification := classifySelectionFailureError(err, fallback)
	if classification.ModelNotFound && !fallback.ModelNotFound {
		service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalModelConfiguration)
	}
	return classification
}

// classifyNoAccountError decides between 404 model_not_found and 503
// api_error for "no available accounts" failures.
//
// The classifier intentionally does not consume the original error: the
// selection layer never tells us *why* the pool came up empty (rate-limited
// vs. unsupported model are both wrapped as ErrNoAvailableAccounts). Instead
// we re-check pool composition through DiagnoseModelAvailabilityForPlatform.
// Its dedicated database query considers only persistent eligibility
// (active status + schedulable setting) and model_mapping, bypassing scheduler
// snapshots and transient filters. That guarantees a 404 is only returned
// when persistent account/group/model configuration must change before the
// request can succeed.
//
// routingModel is the model name that account selection actually compared
// against (i.e. after group-level dispatch mapping). displayModel is the
// raw model the caller asked for; it is used only in the user-facing error
// message so that internal mapping details don't leak. Most callers pass
// the same value for both.
//
// platform is the platform the request was routed to (use
// service.PlatformOpenAI / PlatformAnthropic / PlatformGemini). It is
// required because Anthropic/Gemini routes additionally surface
// mixed-scheduled Antigravity accounts; passing the wrong platform would
// flip a legitimate 503 to a misleading 404 (or vice versa).
func classifyNoAccountError(
	ctx context.Context,
	diag service.ModelAvailabilityDiagnoser,
	apiKey *service.APIKey,
	routingModel string,
	displayModel string,
	platform string,
) noAccountErrorClassification {
	fallback := noAccountErrorClassification{
		Status:  http.StatusServiceUnavailable,
		ErrType: "api_error",
		Message: "Service temporarily unavailable",
	}

	routingModel = strings.TrimSpace(routingModel)
	displayModel = strings.TrimSpace(displayModel)
	if displayModel == "" {
		displayModel = routingModel
	}
	if diag == nil || apiKey == nil || apiKey.GroupID == nil || routingModel == "" {
		return fallback
	}

	result := diag.DiagnoseModelAvailabilityForPlatform(ctx, apiKey.GroupID, routingModel, platform)
	if result.HasAccountsInPool && !result.HasModelSupport {
		return noAccountErrorClassification{
			Status:  http.StatusNotFound,
			ErrType: "model_not_found",
			Code:    modelNotAvailableCode,
			Message: modelNotAvailableMessage(displayModel, groupVisibleModels(apiKey, result.AvailableModels)),
			// Keep the 404 model_not_found classification: callers gate the
			// retryable 503 path on ModelNotFound, and a model that no
			// account can serve must not be retried.
			ModelNotFound: true,
		}
	}
	return fallback
}

// classifyNoAccountErrorFromGin is a thin wrapper that forwards the gin
// context's underlying request context. Most call sites already have a
// *gin.Context handy, so this keeps the call sites uncluttered.
func classifyNoAccountErrorFromGin(
	c *gin.Context,
	diag service.ModelAvailabilityDiagnoser,
	apiKey *service.APIKey,
	routingModel string,
	displayModel string,
	platform string,
) noAccountErrorClassification {
	ctx := context.Background()
	if c != nil && c.Request != nil {
		ctx = c.Request.Context()
	}
	classification := classifyNoAccountError(ctx, diag, apiKey, routingModel, displayModel, platform)
	if classification.ModelNotFound {
		service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalModelConfiguration)
	}
	return classification
}

func classifyOpenAICompatibleNoAccountErrorFromGin(
	c *gin.Context,
	diag service.ModelAvailabilityDiagnoser,
	apiKey *service.APIKey,
	routingModel string,
	displayModel string,
) noAccountErrorClassification {
	ctx := context.Background()
	if c != nil && c.Request != nil {
		ctx = c.Request.Context()
	}
	return classifyNoAccountErrorFromGin(
		c,
		diag,
		apiKey,
		routingModel,
		displayModel,
		openAICompatibleRequestPlatform(ctx, apiKey),
	)
}

func openAICompatibleSelectionErrorForLog(err error, platform string) error {
	if err == nil || platform != service.PlatformGrok {
		return err
	}
	message := strings.ReplaceAll(err.Error(), "OpenAI accounts", "Grok accounts")
	if message == err.Error() {
		return err
	}
	return fmt.Errorf("%s", message)
}
