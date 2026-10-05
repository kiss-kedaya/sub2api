package service

import (
	"net/http"
	"strings"

	"github.com/tidwall/gjson"
)

// GrokImageQualityUnsupportedReason is a provider capability mismatch, not a
// request to silently reduce quality or retry the same deterministic rejection.
const GrokImageQualityUnsupportedReason GatewayFailureReason = "grok_image_quality_unsupported"

// Match only the explicit model/quality capability response observed from an
// upstream. Generic 400s (missing prompt, malformed images, policy refusals,
// etc.) remain terminal and never fan out across accounts.
func isGrokImageQualityUnsupported(status int, model string, body []byte) bool {
	if status != http.StatusBadRequest || !isGrokImageGenerationModel(model) || !gjson.ValidBytes(body) {
		return false
	}
	message := strings.ToLower(strings.TrimSpace(gjson.GetBytes(body, "error.message").String()))
	const prefix = "this model only supports the following quality value(s):"
	if !strings.HasPrefix(message, prefix) {
		return false
	}
	values := strings.TrimSuffix(strings.TrimSpace(strings.TrimPrefix(message, prefix)), ".")
	if values == "" {
		return false
	}
	for _, value := range strings.Split(values, ",") {
		switch strings.TrimSpace(value) {
		case "low", "medium", "high", "auto", "standard", "hd":
		default:
			return false
		}
	}
	return true
}
