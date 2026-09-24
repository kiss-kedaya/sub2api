package service

import (
	"encoding/json"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// Rebuild failure envelopes from an allowlist, never replace text inside output,
// tools or usage. Internal callers retain the original event for side effects.
func redactUpstreamFinancialEvent(payload []byte, eventType string) []byte {
	errorBody := gin.H{"type": "upstream_error", "code": "upstream_error", "message": UpstreamUnavailableMessage}
	event := gin.H{"type": eventType, "error": errorBody}
	if eventType == "response.failed" {
		response := gin.H{"id": gjson.GetBytes(payload, "response.id").String(), "object": "response", "created_at": gjson.GetBytes(payload, "response.created_at").Int(), "status": "failed", "output": []any{}, "error": errorBody}
		event = gin.H{"type": "response.failed", "sequence_number": gjson.GetBytes(payload, "sequence_number").Int(), "response": response}
	}
	result, _ := json.Marshal(event)
	return result
}

// Only inspect known failure envelopes on successful HTTP responses. In
// particular, user-generated content, tool arguments and usage are not scanned.
func upstreamFinancialFailureEnvelope(body []byte) bool {
	node := gjson.ParseBytes(body)
	if node.IsArray() {
		for _, item := range node.Array() {
			if upstreamFinancialFailureEnvelope([]byte(item.Raw)) {
				return true
			}
		}
		return false
	}
	if node.Get("errors").IsArray() || node.Get("error").Exists() && node.Get("error").Type != gjson.Null || node.Get("response.error").Exists() && node.Get("response.error").Type != gjson.Null || node.Get("type").String() == "error" || node.Get("status").String() == "failed" {
		return IsUpstreamFinancialError(0, body)
	}
	return false
}
