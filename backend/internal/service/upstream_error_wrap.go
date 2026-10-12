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

	if IsUpstreamFinancialError(statusCode, body) {
		return http.StatusBadGateway, "upstream_error", "upstream_error", UpstreamUnavailableMessage
	}
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
	if mappedStatus := openAIDeterministicClientHTTPStatus(message, body); mappedStatus > 0 {
		if status < 400 || status >= 500 {
			status = mappedStatus
		}
		if errType == "" || errType == "api_error" || errType == "upstream_error" {
			errType = openAIDeterministicClientErrorType(status)
		}
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
	// Anthropic 官方语义：overloaded_error 用 529 表达。中转上游常把过载改写成
	// 500/502 等普通 5xx，这里按 body 语义还原，避免客户端把它当网关故障处理。
	// 503 保留：上游用 503 表达的是「暂时不可用」，不等同于过载语义。
	if errType == "overloaded_error" && status >= 500 && status != http.StatusServiceUnavailable {
		status = 529
	}
	errCode = extractUpstreamErrorCode(body)
	return
}
