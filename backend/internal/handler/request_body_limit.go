package handler

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func extractMaxBytesError(err error) (*http.MaxBytesError, bool) {
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		return maxErr, true
	}
	return nil, false
}

func formatBodyLimit(limit int64) string {
	const mb = 1024 * 1024
	if limit >= mb {
		return fmt.Sprintf("%dMB", limit/mb)
	}
	return fmt.Sprintf("%dB", limit)
}

func buildBodyTooLargeMessage(limit int64) string {
	return fmt.Sprintf("Request body too large, limit is %s", formatBodyLimit(limit))
}

// deepSeekChatBodyLimit is the largest chat-style request body accepted for
// DeepSeek (owner decision 2026-11-18). Over six hours of production traffic
// only three requests were larger and none of them produced an answer, while
// bodies of 20-48MB completed more than nine times out of ten, so the line sits
// above every request that was seen to work. Tool routes such as OCR keep
// their own limits.
//
// Route middleware has already read the body by the time a handler runs, so
// this does not save the upload; it keeps the request from being forwarded and
// held while the provider works on it. It is a variable only so tests can
// shrink it.
var deepSeekChatBodyLimit int64 = 48 << 20

func deepSeekEntryGroup(apiKey *service.APIKey) bool {
	return apiKey != nil && apiKey.Group != nil && apiKey.Group.Platform == service.PlatformDeepseek
}

// deepSeekChatBodyTooLarge reports whether a chat-style request must be
// rejected before it is forwarded. Call it once with the declared
// Content-Length and an empty model (nothing has been read yet; only a
// DeepSeek entry group can decide), and again with the real size and the
// requested model, which also catches smart-route keys whose entry group is
// another platform.
func deepSeekChatBodyTooLarge(apiKey *service.APIKey, model string, size int64) bool {
	if size <= deepSeekChatBodyLimit {
		return false
	}
	return deepSeekEntryGroup(apiKey) || strings.HasPrefix(strings.ToLower(strings.TrimSpace(model)), "deepseek")
}

func readLenientJSONRequestBodyWithPrealloc(req *http.Request, cfg *config.Config) ([]byte, error) {
	return pkghttputil.ReadLenientJSONRequestBodyWithPrealloc(req, gatewayMaxBodySize(cfg))
}

func gatewayMaxBodySize(cfg *config.Config) int64 {
	if cfg == nil {
		return 0
	}
	return cfg.Gateway.MaxBodySize
}
