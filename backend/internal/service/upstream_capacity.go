package service

import (
	"strings"

	"github.com/tidwall/gjson"
)

// IsUpstreamCapacityCoolingBody reports request-scoped upstream capacity
// failures that should be presented as retryable service unavailability rather
// than as a credential/access failure. Several OpenAI-compatible providers use
// HTTP 403 or a FORBIDDEN code for a group whose suppliers are cooling down.
func IsUpstreamCapacityCoolingBody(body []byte) bool {
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
