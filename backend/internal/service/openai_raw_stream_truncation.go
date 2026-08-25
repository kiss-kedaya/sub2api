package service

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const openAIRawStreamTruncatedUpstreamMessage = "Upstream Chat Completions stream ended before any terminal chunk"

// openAIRawStreamTerminalState tracks the terminal signals understood by the
// Chat Completions SSE protocol. Compatible providers differ on whether they
// emit [DONE] and/or usage, so any of the three signals is sufficient.
type openAIRawStreamTerminalState struct {
	sawDataLine     bool
	sawDone         bool
	sawUsage        bool
	sawFinishReason bool
}

func (t *openAIRawStreamTerminalState) ObserveDataLine(payload string) {
	if t == nil {
		return
	}
	t.sawDataLine = true
	if payload == "[DONE]" {
		t.sawDone = true
		return
	}
	if usage := gjson.Get(payload, "usage"); usage.Exists() && usage.IsObject() {
		t.sawUsage = true
	}
	if t.sawFinishReason {
		return
	}
	for _, choice := range gjson.Get(payload, "choices").Array() {
		if strings.TrimSpace(choice.Get("finish_reason").String()) != "" {
			t.sawFinishReason = true
			return
		}
	}
}

func (t *openAIRawStreamTerminalState) Terminated() bool {
	return t != nil && (t.sawDone || t.sawUsage || t.sawFinishReason)
}

// IsTruncated keeps the old behavior for a non-SSE body that was already
// forwarded verbatim, while treating an empty 200 response as a truncation.
func (t *openAIRawStreamTerminalState) IsTruncated(clientOutputStarted bool) bool {
	if t == nil || t.Terminated() {
		return false
	}
	return t.sawDataLine || !clientOutputStarted
}

func openAIRawStreamTruncatedMessage(cause error) string {
	if cause == nil || errors.Is(cause, ErrOpenAIUpstreamStreamTruncated) {
		return openAIRawStreamTruncatedUpstreamMessage
	}
	return openAIRawStreamTruncatedUpstreamMessage + ": " + cause.Error()
}

func openAIRawStreamTruncatedErrorBody(cause error) []byte {
	code, message := classifyOpenAIUpstreamStreamReadError(cause)
	body, err := json.Marshal(map[string]any{
		"error": map[string]any{
			"type":    "upstream_error",
			"code":    code,
			"message": message,
		},
	})
	if err != nil {
		return []byte(`{"error":{"type":"upstream_error","code":"upstream_stream_truncated","message":"Upstream response stream ended before completion"}}`)
	}
	return body
}

func recordOpenAIRawStreamTruncation(c *gin.Context, account *Account, requestID string, cause error, kind string) {
	if c == nil {
		return
	}
	message := openAIRawStreamTruncatedMessage(cause)
	event := OpsUpstreamErrorEvent{
		Platform:           PlatformOpenAI,
		UpstreamStatusCode: http.StatusBadGateway,
		UpstreamRequestID:  strings.TrimSpace(requestID),
		Kind:               kind,
		Message:            message,
	}
	if account != nil {
		event.Platform = account.Platform
		event.AccountID = account.ID
		event.AccountName = account.Name
	}
	setOpsUpstreamError(c, http.StatusBadGateway, message, "")
	appendOpsUpstreamError(c, event)
}

func newOpenAIRawStreamTruncatedFailoverError(c *gin.Context, account *Account, requestID string, cause error) *UpstreamFailoverError {
	recordOpenAIRawStreamTruncation(c, account, requestID, cause, "failover")
	headers := make(http.Header)
	if strings.TrimSpace(requestID) != "" {
		headers.Set("x-request-id", strings.TrimSpace(requestID))
	}
	return &UpstreamFailoverError{
		StatusCode:      http.StatusBadGateway,
		ResponseBody:    openAIRawStreamTruncatedErrorBody(cause),
		ResponseHeaders: headers,
	}
}
