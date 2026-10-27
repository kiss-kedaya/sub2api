package service

import "fmt"

// AnthropicPassthroughStreamError is a failed upstream stream whose original
// error event has already been handled by the Anthropic API-key passthrough.
// It is deliberately not an UpstreamFailoverError: replaying an accepted stream
// could duplicate output and billing. Observed usage may still accompany it.
type AnthropicPassthroughStreamError struct {
	StatusCode      int
	ErrorType       string
	Message         string
	RawData         string
	ResponseWritten bool
}

func (e *AnthropicPassthroughStreamError) Error() string {
	return fmt.Sprintf("anthropic upstream stream failed: status=%d type=%s", e.StatusCode, e.ErrorType)
}
