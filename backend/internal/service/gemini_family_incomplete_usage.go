package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

// Gemini family (native Gemini, the Claude compatible Gemini bridge and the
// Antigravity paths) billing for a request that ends without a usage report because
// the client left. The policy itself lives in incomplete_request_usage.go; this file
// maps it onto ClaudeUsage, where InputTokens is the fresh input only and the cache
// share is carried separately in CacheReadInputTokens. Gemini reports promptTokenCount
// including cachedContentTokenCount, extractGeminiUsage already subtracts the cached
// part, so a reported figure and an estimate use the same convention.
//
// A figure the upstream really reported is never overwritten or re-split.

// errGeminiClientGone is returned when the client left and nothing billable was
// observed (for example the upstream only produced an error envelope).
var errGeminiClientGone = fmt.Errorf("gemini client disconnected: %w", context.Canceled)

// geminiClientGone reports whether the client of this request has left. The upstream
// request is bound to the client request context, so a disconnect also surfaces as a
// failed upstream read, but the context is the reliable signal.
func geminiClientGone(c *gin.Context) bool {
	return c != nil && c.Request != nil && c.Request.Context().Err() != nil
}

// geminiClientGoneCtx is geminiClientGone for call sites that also hold the
// forwarding context.
func geminiClientGoneCtx(ctx context.Context, c *gin.Context) bool {
	return (ctx != nil && ctx.Err() != nil) || geminiClientGone(c)
}

// geminiOutputMeter accumulates the content a Gemini upstream really produced
// (visible text, thought summaries, function call name and arguments, code and code
// results) so the output can be estimated when no usage figure arrives. It counts
// characters and rounds once at the end, the same way estimateTokensForText does for
// a whole string, instead of rounding every chunk up. Hidden thinking tokens are not
// streamed, so the result is a lower bound.
type geminiOutputMeter struct {
	runes int
	ascii int
}

func (m *geminiOutputMeter) addText(s string) {
	if m == nil {
		return
	}
	for _, r := range s {
		m.runes++
		if r <= 0x7f {
			m.ascii++
		}
	}
}

// observe adds the content of one raw Gemini response chunk (already unwrapped from
// the Code Assist envelope). Use it only for upstreams that stream deltas; a path that
// has to cope with cumulative text feeds addText with its computed delta instead.
func (m *geminiOutputMeter) observe(chunk []byte) {
	if m == nil || len(chunk) == 0 {
		return
	}
	gjson.GetBytes(chunk, "candidates").ForEach(func(_, candidate gjson.Result) bool {
		candidate.Get("content.parts").ForEach(func(_, part gjson.Result) bool {
			m.addText(part.Get("text").String())
			if call := part.Get("functionCall"); call.Exists() {
				m.addText(call.Get("name").String())
				if args := call.Get("args"); args.Exists() {
					m.addText(args.Raw)
				}
			}
			m.addText(part.Get("executableCode.code").String())
			m.addText(part.Get("codeExecutionResult.output").String())
			return true
		})
		return true
	})
}

func (m *geminiOutputMeter) tokens() int {
	if m == nil || m.runes == 0 {
		return 0
	}
	if float64(m.ascii)/float64(m.runes) >= 0.8 {
		return (m.runes + 3) / 4
	}
	return m.runes
}

// geminiUsageReported reports whether the upstream gave any usage figure at all.
func geminiUsageReported(usage *ClaudeUsage) bool {
	return usage != nil && (usage.InputTokens > 0 || usage.OutputTokens > 0 ||
		usage.CacheReadInputTokens > 0 || usage.CacheCreationInputTokens > 0 || usage.ImageOutputTokens > 0)
}

// fillIncompleteGeminiUsage applies the estimates field by field and returns what it
// filled in (0 when untouched). Output is filled only when the upstream reported no
// output at all. Input is filled from the request only when the upstream reported no
// input side at all: InputTokens takes the normal share and CacheReadInputTokens the
// cache share, flagged as an estimate so a model without a cache price bills it at
// the input price.
func fillIncompleteGeminiUsage(usage *ClaudeUsage, outputTokens int, requestBody []byte) (filledOutput, filledInput int) {
	if usage == nil {
		return 0, 0
	}
	if outputTokens > 0 && usage.OutputTokens == 0 && usage.ImageOutputTokens == 0 {
		usage.OutputTokens = outputTokens
		filledOutput = outputTokens
	}
	if usage.InputTokens == 0 && usage.CacheReadInputTokens == 0 && usage.CacheCreationInputTokens == 0 {
		if total := estimateRequestInputTokens(requestBody); total > 0 {
			split := splitIncompleteInput(total)
			usage.InputTokens = split.Fresh
			usage.CacheReadInputTokens = split.CacheRead
			usage.CacheReadEstimated = true
			filledInput = split.Total
		}
	}
	return filledOutput, filledInput
}

// applyIncompleteGeminiUsage fills the missing usage of a request whose client left
// after the upstream accepted it, and logs the estimate. event names the log line.
func applyIncompleteGeminiUsage(event string, account *Account, model string, usage *ClaudeUsage, outputTokens int, requestBody []byte) {
	filledOutput, filledInput := fillIncompleteGeminiUsage(usage, outputTokens, requestBody)
	if filledOutput == 0 && filledInput == 0 {
		return
	}
	accountID := int64(0)
	if account != nil {
		accountID = account.ID
	}
	logger.L().With(
		zap.String("component", "service.gemini_family"),
		zap.Int64("account_id", accountID),
		zap.String("model", strings.TrimSpace(model)),
		zap.Bool("client_disconnect", true),
		zap.Int("estimated_output_tokens", filledOutput),
		zap.Int("estimated_input_tokens", filledInput),
		zap.Int("estimated_cache_read_tokens", incompleteCacheReadTokens(filledInput)),
	).Warn(event)
}
