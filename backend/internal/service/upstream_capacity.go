package service

import (
	"strings"

	"github.com/tidwall/gjson"
)

// IsUpstreamWAFBody reports Cloudflare / browser-integrity blocks. These are
// request-identity failures, not supplier capacity cooling. Valid JSON is
// inspected only on error fields so echoed request text cannot trip the
// matcher.
func IsUpstreamWAFBody(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	for _, path := range []string{
		"error.message",
		"response.error.message",
		"message",
		"error.code",
		"response.error.code",
		"code",
	} {
		if isUpstreamWAFMessage(gjson.GetBytes(body, path).String()) {
			return true
		}
	}
	if !gjson.ValidBytes(body) {
		return isUpstreamWAFMessage(string(body))
	}
	return false
}

func isUpstreamWAFMessage(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	if lower == "" {
		return false
	}
	for _, marker := range []string{
		"error code: 1010",
		"error 1010",
		"your request was blocked",
		"request was blocked",
		"window._cf_chl_opt",
		"just a moment",
		"challenge-platform",
		"__cf_chl_",
		"cf-mitigated",
		"attention required! | cloudflare",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// IsUpstreamCapacityCoolingBody reports request-scoped upstream capacity
// failures that should be presented as retryable service unavailability rather
// than as a credential/access failure. Several OpenAI-compatible providers use
// HTTP 403 or a FORBIDDEN code for a group whose suppliers are cooling down.
func IsUpstreamCapacityCoolingBody(body []byte) bool {
	if IsUpstreamWAFBody(body) {
		return false
	}
	if len(body) == 0 {
		return false
	}
	for _, path := range []string{
		"error.message",
		"response.error.message",
		"message",
		"error.code",
		"response.error.code",
		"code",
	} {
		if isUpstreamCapacityCoolingMessage(gjson.GetBytes(body, path).String()) {
			return true
		}
	}
	// Non-JSON providers commonly return a plain-text capacity response.
	if !gjson.ValidBytes(body) {
		return isUpstreamCapacityCoolingMessage(string(body))
	}
	return false
}

// upstreamRequestBodyBudgetExhaustedMarker is emitted verbatim by sub2api
// instances (v0.2.20+) when the gateway request-body memory budget
// (GATEWAY_REQUEST_BODY_MEMORY_BUDGET_BYTES) is full:
//
//	503 {"error":{"message":"Request body buffering capacity is exhausted; retry later",...}}
//	Retry-After: 1
//
// Only that phrase marks this condition; generic capacity/cooling wording must
// keep its existing classification.
const upstreamRequestBodyBudgetExhaustedMarker = "request body buffering capacity is exhausted"

// UpstreamRequestBodyBudgetClientMessage is the fixed downstream message for
// this condition. The upstream text names the supplier instance's internal
// memory budget, so it is replaced instead of relayed verbatim.
const UpstreamRequestBodyBudgetClientMessage = "Upstream request capacity is temporarily exhausted, please retry later"

// IsUpstreamRequestBodyBudgetExhausted reports whether an upstream response is
// the request-body memory budget rejection from a sub2api instance.
//
// This is a per-instance capacity signal, not a credential or request error:
// the instance cannot accept another buffered body until in-flight bodies are
// released, so retrying the same account immediately only deepens the deficit.
// Rotating to another account/upstream instance remains valid.
func IsUpstreamRequestBodyBudgetExhausted(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	return strings.Contains(strings.ToLower(string(body)), upstreamRequestBodyBudgetExhaustedMarker)
}

func isUpstreamCapacityCoolingMessage(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	if lower == "" {
		return false
	}
	if isOpenAICapacityShedMessage(lower) {
		return true
	}
	for _, marker := range []string{
		"cooling",
		"all candidates failed",
		"候选供应商均请求失败",
		"支持该模型的货源均",
		"货源均在冷却中",
		"稍后重试",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return strings.Contains(lower, "all providers") && strings.Contains(lower, "failed")
}
