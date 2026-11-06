package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptrace"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

// Billing policy for an Anthropic Messages request that ends without a usage
// report (the client left, the upstream was cut or stalled). The protocol neutral
// rules and the input estimate live in incomplete_request_usage.go; this file maps
// them onto ClaudeUsage, where InputTokens is the fresh input only and the cache
// read is counted separately.
//
// Anthropic reports input and cache usage on message_start together with a
// provisional output count, and the cumulative output on message_delta. So:
//   - input that message_start reported is kept, never re-estimated;
//   - output is estimated from the content the upstream produced, unless a
//     message_delta reported a cumulative count;
//   - a request that never got a message_start usage is estimated from the request
//     body, 95% of it as cache read and the rest as normal input.

// anthropicOutputMeter counts streamed characters without keeping the text and
// estimates tokens by the rule of estimateTokensForText: about four characters
// per token for mostly ASCII text, one token per character otherwise.
type anthropicOutputMeter struct {
	runes int
	ascii int
}

func (m *anthropicOutputMeter) add(text string) {
	for _, r := range text {
		m.runes++
		if r <= 0x7f {
			m.ascii++
		}
	}
}

func (m *anthropicOutputMeter) tokens() int {
	if m.runes == 0 {
		return 0
	}
	if float64(m.ascii)/float64(m.runes) >= 0.8 {
		return (m.runes + 3) / 4
	}
	return m.runes
}

// anthropicStreamProgress is what a stream handler learned about the upstream
// answer: how much it produced, and whether a message_delta ever reported the
// cumulative output (message_start only carries a provisional count).
type anthropicStreamProgress struct {
	text           anthropicOutputMeter
	thinking       anthropicOutputMeter
	tool           anthropicOutputMeter
	outputReported bool
}

// producedTokens is a lower bound of the output the upstream produced.
func (p *anthropicStreamProgress) producedTokens() int {
	if p == nil {
		return 0
	}
	return p.text.tokens() + p.thinking.tokens() + p.tool.tokens()
}

// observeData reads one SSE data payload (native, passthrough and Bedrock).
func (p *anthropicStreamProgress) observeData(data string) {
	if p == nil || data == "" || data == "[DONE]" {
		return
	}
	parsed := gjson.Parse(data)
	switch parsed.Get("type").String() {
	case "content_block_start":
		block := parsed.Get("content_block")
		switch block.Get("type").String() {
		case "text":
			p.text.add(block.Get("text").String())
		case "thinking":
			p.thinking.add(block.Get("thinking").String())
		case "tool_use", "server_tool_use":
			p.tool.add(block.Get("name").String())
		}
	case "content_block_delta":
		delta := parsed.Get("delta")
		switch delta.Get("type").String() {
		case "text_delta":
			p.text.add(delta.Get("text").String())
		case "thinking_delta":
			p.thinking.add(delta.Get("thinking").String())
		case "input_json_delta":
			p.tool.add(delta.Get("partial_json").String())
		}
	case "message_delta":
		if v := parsed.Get("usage.output_tokens"); v.Exists() && v.Type != gjson.Null {
			p.outputReported = true
		}
	}
}

// observeEvent is observeData for an already decoded event.
func (p *anthropicStreamProgress) observeEvent(event *apicompat.AnthropicStreamEvent) {
	if p == nil || event == nil {
		return
	}
	switch event.Type {
	case "content_block_start":
		if block := event.ContentBlock; block != nil {
			switch block.Type {
			case "text":
				p.text.add(block.Text)
			case "thinking":
				p.thinking.add(block.Thinking)
			case "tool_use", "server_tool_use":
				p.tool.add(block.Name)
			}
		}
	case "content_block_delta":
		if delta := event.Delta; delta != nil {
			switch delta.Type {
			case "text_delta":
				p.text.add(delta.Text)
			case "thinking_delta":
				p.thinking.add(delta.Thinking)
			case "input_json_delta":
				p.tool.add(delta.PartialJSON)
			}
		}
	case "message_delta":
		if event.Usage != nil && event.Usage.HasOutputTokens() {
			p.outputReported = true
		}
	}
}

// fillIncompleteAnthropicUsage applies the estimates field by field and returns
// what it filled in (0 when untouched). Output is raised to the produced content
// only when no message_delta reported the cumulative output, and never lowered.
// Input is estimated from the request only when the upstream reported no input
// side at all; InputTokens then holds the normal-priced share and
// CacheReadInputTokens the cache-priced share.
func fillIncompleteAnthropicUsage(usage *ClaudeUsage, progress *anthropicStreamProgress, requestBody []byte) (filledOutput, filledInput int) {
	if usage == nil {
		return 0, 0
	}
	if progress != nil && !progress.outputReported && usage.ImageOutputTokens == 0 {
		if produced := progress.producedTokens(); produced > usage.OutputTokens {
			usage.OutputTokens = produced
			filledOutput = produced
		}
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

// estimateIncompleteAnthropicUsage gates fillIncompleteAnthropicUsage: input is
// billed once the request was dispatched and the client then left, or whenever
// output was produced. An upstream break that produced nothing stays free.
func estimateIncompleteAnthropicUsage(usage *ClaudeUsage, progress *anthropicStreamProgress, requestBody []byte, clientDisconnect bool) (outputTokens, inputTokens int) {
	if usage == nil {
		return 0, 0
	}
	if !clientDisconnect && progress.producedTokens() <= 0 {
		return 0, 0
	}
	return fillIncompleteAnthropicUsage(usage, progress, requestBody)
}

// anthropicEstimateSkippedForError lists the failures that keep their own
// accounting: a replayable failover (the retry is billed on its own), an upstream
// error event already forwarded to the client, and a balance abort.
func anthropicEstimateSkippedForError(err error) bool {
	if err == nil {
		return false
	}
	var failoverErr *UpstreamFailoverError
	var streamErr *AnthropicPassthroughStreamError
	return errors.As(err, &failoverErr) || errors.As(err, &streamErr) || errors.Is(err, ErrBalanceWithholdingFailed)
}

func logAnthropicIncompleteEstimate(accountID int64, requestID, model string, clientDisconnect bool, outputTokens, inputTokens int, cause error) {
	if outputTokens == 0 && inputTokens == 0 {
		return
	}
	logger.L().With(
		zap.String("component", "service.gateway"),
		zap.Int64("account_id", accountID),
		zap.String("model", strings.TrimSpace(model)),
		zap.String("request_id", requestID),
		zap.Bool("client_disconnect", clientDisconnect),
		zap.Int("estimated_output_tokens", outputTokens),
		zap.Int("estimated_input_tokens", inputTokens),
		zap.Int("estimated_cache_read_tokens", incompleteCacheReadTokens(inputTokens)),
		zap.Error(cause),
	).Warn("anthropic_usage.incomplete_stream_estimated")
}

// applyIncompleteAnthropicStreamUsage fills the stream result's usage from the
// estimates when the stream ended without a terminal frame. requestBody is the
// body that was sent to the upstream.
func applyIncompleteAnthropicStreamUsage(account *Account, result *streamingResult, requestBody []byte, model, requestID string, streamErr error) (outputTokens, inputTokens int) {
	if result == nil || result.usage == nil || !result.incompleteStream || anthropicEstimateSkippedForError(streamErr) {
		return 0, 0
	}
	outputTokens, inputTokens = estimateIncompleteAnthropicUsage(result.usage, result.progress, requestBody, result.clientDisconnect)
	accountID := int64(0)
	if account != nil {
		accountID = account.ID
	}
	logAnthropicIncompleteEstimate(accountID, requestID, model, result.clientDisconnect, outputTokens, inputTokens, streamErr)
	return outputTokens, inputTokens
}

// anthropicEstimateSource is what the converted handlers (Chat Completions and
// Responses served by an Anthropic upstream) need to estimate usage.
type anthropicEstimateSource struct {
	account *Account
	// body is the Anthropic request body that was sent to the upstream.
	body []byte
	// model is the model name used for the log line.
	model string
}

// fill estimates the usage of a converted answer that ended without a terminal
// frame, in place, and logs what it filled in.
func (e anthropicEstimateSource) fill(usage *ClaudeUsage, progress *anthropicStreamProgress, requestID string, clientDisconnect bool, cause error) {
	outputTokens, inputTokens := estimateIncompleteAnthropicUsage(usage, progress, e.body, clientDisconnect)
	accountID := int64(0)
	if e.account != nil {
		accountID = e.account.ID
	}
	logAnthropicIncompleteEstimate(accountID, requestID, e.model, clientDisconnect, outputTokens, inputTokens, cause)
}

const (
	anthropicGoneStream  = "stream usage incomplete after disconnect"
	anthropicGoneRequest = "request abandoned by the client"
)

// anthropicClientGoneError marks a failure that is the departed client's doing.
// It matches context.Canceled, so the handler neither fails over nor evicts the
// account or its sticky session for it.
type anthropicClientGoneError struct {
	prefix string
	cause  error
}

func newAnthropicClientGoneError(prefix string, cause error) error {
	if cause == nil {
		cause = context.Canceled
	}
	return &anthropicClientGoneError{prefix: prefix, cause: cause}
}

func (e *anthropicClientGoneError) Error() string { return e.prefix + ": " + e.cause.Error() }

func (e *anthropicClientGoneError) Unwrap() error { return e.cause }

func (e *anthropicClientGoneError) Is(target error) bool { return target == context.Canceled }

// anthropicClientContexts returns the contexts whose end means the client is
// gone: the execution context and, when it is a different one, the request's.
func anthropicClientContexts(ctx context.Context, c *gin.Context) []context.Context {
	var out []context.Context
	if ctx != nil && ctx.Done() != nil {
		out = append(out, ctx)
	}
	if c != nil && c.Request != nil {
		if reqCtx := c.Request.Context(); reqCtx != nil && reqCtx.Done() != nil &&
			(len(out) == 0 || out[0].Done() != reqCtx.Done()) {
			out = append(out, reqCtx)
		}
	}
	return out
}

// anthropicClientWatch closes the upstream body the moment the client is gone,
// whether its context ended or a write to it failed. Frames the scanner already
// read are still processed, so a terminal frame that had arrived keeps its real
// usage.
type anthropicClientWatch struct {
	gone     atomic.Bool
	closer   io.Closer
	contexts []context.Context
	stops    []func() bool
}

func newAnthropicClientWatch(ctx context.Context, c *gin.Context, closer io.Closer) *anthropicClientWatch {
	w := &anthropicClientWatch{closer: closer, contexts: anthropicClientContexts(ctx, c)}
	for _, clientCtx := range w.contexts {
		w.stops = append(w.stops, context.AfterFunc(clientCtx, w.markGone))
	}
	return w
}

func (w *anthropicClientWatch) markGone() {
	if w == nil || w.gone.Swap(true) {
		return
	}
	if w.closer != nil {
		_ = w.closer.Close()
	}
}

func (w *anthropicClientWatch) isGone() bool {
	if w == nil {
		return false
	}
	if w.gone.Load() {
		return true
	}
	for _, clientCtx := range w.contexts {
		if clientCtx.Err() != nil {
			w.markGone()
			return true
		}
	}
	return false
}

func (w *anthropicClientWatch) stop() {
	if w == nil {
		return
	}
	for _, stop := range w.stops {
		stop()
	}
}

// anthropicAttempt is one try of an upstream call. written is set once the whole
// request was handed to the upstream connection; answered once the upstream began
// to answer (any status), after which the try is no longer "waiting for usage".
type anthropicAttempt struct {
	written  atomic.Bool
	answered atomic.Bool
}

// anthropicUpstreamCall ties the upstream requests of one forward to its client.
// A stream's upstream context is detached from the request (the API key slot
// lease owns its lifetime); this call context ends with either that lease or the
// client, so a departed client closes the upstream at once, also while the
// response headers are still awaited.
type anthropicUpstreamCall struct {
	ctx     context.Context
	cancel  context.CancelFunc
	release context.CancelFunc
	clients []context.Context
	stops   []func() bool
	current atomic.Pointer[anthropicAttempt]
}

func newAnthropicUpstreamCall(ctx context.Context, c *gin.Context, stream bool) *anthropicUpstreamCall {
	base, release := detachStreamUpstreamContext(ctx, stream)
	callCtx, cancel := context.WithCancel(base)
	call := &anthropicUpstreamCall{ctx: callCtx, cancel: cancel, release: release, clients: anthropicClientContexts(ctx, c)}
	for _, clientCtx := range call.clients {
		call.stops = append(call.stops, context.AfterFunc(clientCtx, cancel))
	}
	return call
}

// attempt starts a new try and returns the context to build its request with. The
// request-written mark is per try, so a try the upstream rejected can never make
// a later try that was cut off look dispatched.
func (u *anthropicUpstreamCall) attempt() context.Context {
	current := &anthropicAttempt{}
	u.current.Store(current)
	return httptrace.WithClientTrace(u.ctx, &httptrace.ClientTrace{
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			if info.Err == nil {
				current.written.Store(true)
			}
		},
		GotFirstResponseByte: func() { current.answered.Store(true) },
	})
}

// markAnswered records that the current try got an answer from the upstream.
func (u *anthropicUpstreamCall) markAnswered() {
	if current := u.current.Load(); current != nil {
		current.answered.Store(true)
	}
}

// dispatched reports whether the current try was fully written to the upstream and
// is still waiting for its answer. A try the upstream already answered (for
// example with a retryable status) was judged by that answer, not by the client.
func (u *anthropicUpstreamCall) dispatched() bool {
	current := u.current.Load()
	return current != nil && current.written.Load() && !current.answered.Load()
}

func (u *anthropicUpstreamCall) clientGone() bool {
	for _, clientCtx := range u.clients {
		if clientCtx.Err() != nil {
			return true
		}
	}
	return false
}

// departedClient reports whether err is the departed client's doing: its context
// ended and the upstream call failed with that cancellation, not with a fault.
func (u *anthropicUpstreamCall) departedClient(err error) bool {
	if err == nil || !u.clientGone() {
		return false
	}
	var failoverErr *UpstreamFailoverError
	if errors.As(err, &failoverErr) {
		return false
	}
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func (u *anthropicUpstreamCall) close() {
	for _, stop := range u.stops {
		stop()
	}
	u.cancel()
	u.release()
}

// anthropicClientGoneResult is the billing result of a request the client left
// before any usage was reported: the request was sent, so its input is billed,
// estimated from the body that went upstream (95% as cache read). Nothing is
// billed when the body cannot be estimated. resp is the upstream response when
// its headers already arrived.
func anthropicClientGoneResult(account *Account, requestBody []byte, model, upstreamModel string, stream bool, startTime time.Time, resp *http.Response) *ForwardResult {
	total := estimateRequestInputTokens(requestBody)
	if total <= 0 {
		return nil
	}
	split := splitIncompleteInput(total)
	result := &ForwardResult{
		Usage:            ClaudeUsage{InputTokens: split.Fresh, CacheReadInputTokens: split.CacheRead, CacheReadEstimated: true},
		Model:            model,
		UpstreamModel:    upstreamModel,
		Stream:           stream,
		Duration:         time.Since(startTime),
		ClientDisconnect: true,
	}
	if resp != nil {
		result.RequestID = resp.Header.Get("x-request-id")
		result.UpstreamHeaders = resp.Header
	}
	accountID := int64(0)
	if account != nil {
		accountID = account.ID
	}
	logAnthropicIncompleteEstimate(accountID, result.RequestID, model, true, 0, split.Total, nil)
	return result
}
