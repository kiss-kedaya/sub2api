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
// DeepSeek. The provider accepts larger uploads and then never answers, which
// pinned tens of megabytes per request for hours; the owner set 20MB
// (2026-11-17). Tool routes such as OCR keep their own, larger limits.
const deepSeekChatBodyLimit int64 = 20 << 20

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
