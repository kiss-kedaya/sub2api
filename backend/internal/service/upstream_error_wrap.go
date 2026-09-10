package service

import (
	"net/http"
	"strings"

	"github.com/tidwall/gjson"
)

// WrapUpstreamErrorForClient keeps the upstream HTTP status and message.
// The gateway only wraps them into the inbound error envelope; it does not
// rewrite 503 overloaded into a generic 502 "Upstream request failed".
func WrapUpstreamErrorForClient(statusCode int, body []byte) (status int, errType, errCode, message string) {
	status = statusCode
	if status < 400 || status > 599 {
		status = http.StatusBadGateway
	}

	message = strings.TrimSpace(sanitizeUpstreamErrorMessage(extractUpstreamErrorMessage(body)))
	if message == "" {
		plain := strings.TrimSpace(string(body))
		if plain != "" && !strings.HasPrefix(plain, "{") && !strings.HasPrefix(plain, "<") {
			message = strings.TrimSpace(sanitizeUpstreamErrorMessage(plain))
		}
	}
	if message == "" {
		if text := http.StatusText(status); text != "" {
			message = text
		} else {
			message = "upstream error"
		}
	}

	errType = strings.TrimSpace(gjson.GetBytes(body, "error.type").String())
	if errType == "" {
		errType = strings.TrimSpace(gjson.GetBytes(body, "error.status").String())
	}
	if errType == "" {
		switch status {
		case http.StatusTooManyRequests:
			errType = "rate_limit_error"
		case 529:
			errType = "overloaded_error"
		case http.StatusBadRequest:
			errType = "invalid_request_error"
		case http.StatusNotFound:
			errType = "not_found_error"
		default:
			errType = "api_error"
		}
	}
	errCode = extractUpstreamErrorCode(body)
	return
}
