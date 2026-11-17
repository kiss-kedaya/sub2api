package clientprivacy

import (
	"net/http"
	"regexp"
)

// Only explicit relay-owned types are normalized; official api_error and
// invalid_request_error (and unknown provider types) must retain their meaning.
const (
	relayErrorTypeNewAPI = "new_api_error"
	relayErrorTypeOneAPI = "one_api_error"
)

var relayRequestIDSuffix = regexp.MustCompile(`[ \t]*\(request id: [A-Za-z0-9]+\)$`)

// ErrorMessage removes a relay's trailing diagnostic correlation suffix only.
// It is egress-only: Text remains suitable for internal diagnostics and rules.
func ErrorMessage(message string) string {
	return relayRequestIDSuffix.ReplaceAllString(message, "")
}

// ErrorType replaces only known relay error types with a standard status type.
func ErrorType(status int, upstreamType string) string {
	switch upstreamType {
	case relayErrorTypeNewAPI, relayErrorTypeOneAPI:
		return ErrorTypeForStatus(status)
	default:
		return upstreamType
	}
}

// ErrorTypeForStatus is the existing OpenAI Images status-to-type mapping,
// shared by client-facing error normalization without changing HTTP statuses.
func ErrorTypeForStatus(status int) string {
	switch {
	case status == http.StatusBadRequest:
		return "invalid_request_error"
	case status == http.StatusUnauthorized:
		return "authentication_error"
	case status == http.StatusForbidden:
		return "permission_error"
	case status == http.StatusNotFound:
		return "not_found_error"
	case status == http.StatusTooManyRequests:
		return "rate_limit_error"
	case status >= http.StatusInternalServerError:
		return "api_error"
	default:
		return "upstream_error"
	}
}
