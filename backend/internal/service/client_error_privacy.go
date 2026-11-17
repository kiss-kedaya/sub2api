package service

import (
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/util/clientprivacy"
	"github.com/tidwall/gjson"
)

// sanitizeClientUpstreamErrorBody is an egress-only boundary. Classifiers,
// retry decisions and billing must continue to receive the original bytes.
// Successful model text, tool results and search source URLs are untouched.
func sanitizeClientUpstreamErrorBody(status int, body []byte) []byte {
	isError := status < http.StatusOK || status >= http.StatusMultipleChoices
	if !isError && gjson.ValidBytes(body) {
		isError = clientPrivacyErrorValue(gjson.GetBytes(body, "error")) ||
			clientPrivacyErrorValue(gjson.GetBytes(body, "response.error")) ||
			gjson.GetBytes(body, "type").String() == "error" ||
			gjson.GetBytes(body, "type").String() == "response.failed"
	}
	if !isError {
		if bodyHasSSEFraming(body) {
			return []byte(clientprivacy.SSE(string(body)))
		}
		return body
	}
	if bodyHasSSEFraming(body) {
		return []byte(clientprivacy.ErrorSSEWithStatus(string(body), status))
	}
	return clientprivacy.JSONWithStatus(body, status)
}

// ClientUpstreamErrorMessage removes relay correlation suffixes only at client
// write boundaries. Internal diagnostics and rule matching keep their original.
func ClientUpstreamErrorMessage(message string) string {
	return clientprivacy.ErrorMessage(message)
}

func clientPrivacyErrorValue(value gjson.Result) bool {
	switch value.Type {
	case gjson.Null, gjson.False:
		return false
	case gjson.String:
		return value.Str != ""
	case gjson.Number:
		return value.Num != 0
	default:
		return true
	}
}
