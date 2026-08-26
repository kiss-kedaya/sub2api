package handler

import (
	"context"
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
	Message string
	// ModelNotFound also acts as the existing non-capacity routing flag. A
	// plan-gated model uses it to prevent handlers from replacing the precise
	// 403 with a generic retryable capacity message.
	ModelNotFound bool
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
		return noAccountErrorClassification{
			Status:        http.StatusNotFound,
			ErrType:       "model_not_found",
			Message:       "The requested model is not supported by any currently configured upstream account.",
			ModelNotFound: true,
		}
	}
	return fallback
}

func classifySelectionFailureErrorFromGin(c *gin.Context, err error, fallback noAccountErrorClassification) noAccountErrorClassification {
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
			Status:        http.StatusNotFound,
			ErrType:       "model_not_found",
			Message:       fmt.Sprintf("Model %q is not supported by any configured account in this group", displayModel),
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
