package service

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// Tests shorten the interval without changing the production request-size gate.
var deepSeekStreamSilenceLimit = deepSeekLargeRequestWaitLimit

var errOpenAIStreamSilenceLimit = fmt.Errorf("upstream stream silence limit: %w", errOpenAIUpstreamWaitLimit)

type openAIStreamSilenceReadError struct {
	onLimit func()
	once    sync.Once
}

func (e *openAIStreamSilenceReadError) Error() string { return errOpenAIStreamSilenceLimit.Error() }
func (e *openAIStreamSilenceReadError) Unwrap() error { return errOpenAIStreamSilenceLimit }

type openAIStreamSilenceProgress struct {
	lastOutput time.Time
	finished   bool
}

// One timer follows activity, not total duration. Its callback never touches Gin
// or the reader's line state; cleanup never holds the state mutex across Close.
type openAIStreamSilenceBody struct {
	io.ReadCloser
	mu        sync.Mutex
	ctx       context.Context
	clientCtx context.Context
	cancel    context.CancelCauseFunc
	timer     *time.Timer
	limit     time.Duration
	progress  atomic.Pointer[openAIStreamSilenceProgress]
	finished  bool
	expired   bool
	closeOnce sync.Once
	closeErr  error
	sse       bool
	semantic  atomic.Bool
	failure   *openAIStreamSilenceReadError
}

func newOpenAIStreamSilenceBody(ctx context.Context, cancel context.CancelCauseFunc, body io.ReadCloser, onLimit func(), sse bool) *openAIStreamSilenceBody {
	b := &openAIStreamSilenceBody{ReadCloser: body, ctx: ctx, cancel: cancel, limit: deepSeekStreamSilenceLimit, failure: &openAIStreamSilenceReadError{onLimit: onLimit}}
	b.sse = sse
	b.progress.Store(&openAIStreamSilenceProgress{lastOutput: time.Now()})
	if c := OpsGinContextFrom(ctx); c != nil && c.Request != nil {
		b.clientCtx = c.Request.Context()
	}
	b.mu.Lock()
	b.timer = time.AfterFunc(b.limit, b.expire)
	b.mu.Unlock()
	return b
}

func (b *openAIStreamSilenceBody) expire() {
	b.mu.Lock()
	for {
		progress := b.progress.Load()
		if b.finished || progress == nil || progress.finished || b.ctx.Err() != nil || (b.clientCtx != nil && b.clientCtx.Err() != nil) {
			b.mu.Unlock()
			return
		}
		if remaining := b.limit - time.Since(progress.lastOutput); remaining > 0 {
			b.timer.Reset(remaining)
			b.mu.Unlock()
			return
		}
		// Claim this exact observation. If Read published output or EOF while
		// expiry was deciding, recheck it instead of cancelling a completed read.
		if b.progress.CompareAndSwap(progress, nil) {
			break
		}
	}
	b.expired, b.finished = true, true
	b.cancel(errOpenAIStreamSilenceLimit)
	b.mu.Unlock()
	// A decoder may wait for Read to return; Read needs mu to finish.
	_ = b.closeBody()
}

func (b *openAIStreamSilenceBody) closeBody() error {
	b.closeOnce.Do(func() { b.closeErr = b.ReadCloser.Close() })
	return b.closeErr
}

func (b *openAIStreamSilenceBody) publishProgress(output, finished bool) {
	if !output && !finished {
		return
	}
	for previous := b.progress.Load(); previous != nil; previous = b.progress.Load() {
		progress := &openAIStreamSilenceProgress{lastOutput: previous.lastOutput, finished: previous.finished || finished}
		if output {
			progress.lastOutput = time.Now()
		}
		if b.progress.CompareAndSwap(previous, progress) {
			return
		}
	}
}

func (b *openAIStreamSilenceBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	// SSE activity is published by the existing scanner after complete tokens,
	// independent of transport Read boundaries. Buffered/non-SSE reads keep
	// byte activity until a streaming consumer attaches semantic observation.
	b.publishProgress(!b.semantic.Load() && len(bytes.TrimSpace(p[:n])) != 0, err != nil)
	if err == nil {
		// Do not hold up the parser behind expire's mutex: its output must be
		// published before the timer claims that observation.
		if b.progress.Load() == nil {
			return n, b.failure
		}
		return n, nil
	}
	b.mu.Lock()
	b.finished = true
	b.timer.Stop()
	expired := b.expired
	b.mu.Unlock()
	if expired {
		return n, b.failure
	}
	return n, err
}

// Observe tokens from the consumer's scanner, using its exact split function
// and size limits. Framing, multi-data joining and concatenated-document repair
// reuse the gateway's compatibility parsers; Read never parses another protocol.
func observeOpenAIStreamSilence(scanner *bufio.Scanner, source io.Reader, split bufio.SplitFunc) {
	var body *openAIStreamSilenceBody
	for source != nil {
		switch r := source.(type) {
		case *openAIStreamSilenceBody:
			body = r
			source = nil
		case *openAIRequestContextReadCloser:
			source = r.ReadCloser
		case *compatWatchedBody:
			source = r.ReadCloser
		default:
			source = nil
		}
	}
	if body == nil || !body.sse {
		return
	}
	body.semantic.Store(true)
	if split == nil {
		split = bufio.ScanLines
	}
	var parser openAICompatSSEFrameParser
	joinBytes := 0
	discardJoinedFrame := false
	observeDocument := func(frame openAISSEDataFrame, eventType string) {
		// A named error event remains an error even if the payload claims output.
		if !openAIUpstreamFailureMarked(frame.root, eventType) {
			body.publishProgress(openAIStreamSilenceFrameHasOutput(frame), false)
		}
	}
	observeDocuments := func(payload []byte, eventType string) bool {
		frame := parseOpenAISSEDataFrame(payload, eventType)
		if frame.validJSON || frame.isDone() {
			observeDocument(frame, eventType)
			return true
		}
		documents, repaired := splitOpenAIConcatenatedJSONDocuments(payload)
		for _, document := range documents {
			observeDocument(parseTrustedOpenAISSEDataFrame(document, eventType), eventType)
		}
		return repaired
	}
	observe := func(frame openAICompatSSEFrame, ok bool) {
		if !ok || observeDocuments([]byte(frame.Data), frame.EventType) {
			return
		}
		emitOpenAISSEDataPayloads(strings.Split(frame.Data, "\n"), func(payload []byte) {
			observeDocuments(payload, frame.EventType)
		})
	}
	scanner.Split(func(data []byte, atEOF bool) (int, []byte, error) {
		advance, token, err := split(data, atEOF)
		if token != nil {
			line := string(token)
			payload, isData := extractOpenAISSEDataLine(line)
			// The existing consumers accept independent complete data documents too.
			// Publish those now and retain no copy while waiting for an event boundary.
			// Only genuinely split multi-data JSON needs the existing frame assembler.
			if line == "" {
				observe(parser.AddLine(line))
				joinBytes, discardJoinedFrame = 0, false
			} else if !isData {
				observe(parser.AddLine(line))
			} else if len(parser.dataLines) == 0 && observeDocuments([]byte(payload), parser.eventType) {
				// A complete compatibility document needs no retained copy.
			} else if !discardJoinedFrame {
				// Bound pending observation copies using the existing scanner ceiling.
				// This does not cap or alter the consumer's original frame handling.
				fragmentBytes := len(line) + 32 // Include string/slice metadata, even for empty data.
				if fragmentBytes > sseScannerTokenMaxOpenAI-joinBytes {
					parser.dataLines = nil
					discardJoinedFrame = true
				} else {
					joinBytes += fragmentBytes
					observe(parser.AddLine(line))
				}
			}
		}
		if atEOF && advance == len(data) {
			observe(parser.Finish())
		}
		return advance, token, err
	})
}

func openAIStreamSilenceFrameHasOutput(frame openAISSEDataFrame) bool {
	if !frame.validJSON || openAIUpstreamFailureMarked(frame.root, frame.eventType) {
		return false
	}
	if openAIStreamFrameStartsVisibleOutput(frame) {
		return true
	}
	for _, choice := range frame.root.Get("choices").Array() {
		delta := choice.Get("delta")
		if delta.Get("content").Str != "" || delta.Get("reasoning_content").Str != "" ||
			delta.Get("reasoning").Str != "" || delta.Get("refusal").Str != "" ||
			delta.Get("function_call.arguments").Str != "" || delta.Get("function_call.name").Str != "" ||
			chatMessageHasImageOutput(delta) {
			return true
		}
		for _, call := range delta.Get("tool_calls").Array() {
			if call.Get("function.arguments").Str != "" || call.Get("function.name").Str != "" ||
				call.Get("custom.input").Str != "" || call.Get("custom.name").Str != "" {
				return true
			}
		}
	}
	return false
}

func (b *openAIStreamSilenceBody) Close() error {
	b.mu.Lock()
	b.finished = true
	b.timer.Stop()
	b.cancel(nil)
	b.mu.Unlock()
	return b.closeBody()
}

func recordOpenAIStreamSilenceLimit(ctx context.Context, account *Account) {
	c := OpsGinContextFrom(ctx)
	message := "Upstream did not respond in time"
	logger.LegacyPrintf("service.openai_gateway", "OpenAI stream silence limit: account=%d platform=%s", account.ID, account.Platform)
	setOpsUpstreamError(c, http.StatusGatewayTimeout, message, "")
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		ProxyID: opsUpstreamProxyID(account), ProxyName: opsUpstreamProxyName(account),
		Platform: account.Platform, AccountID: account.ID, AccountName: account.Name,
		UpstreamStatusCode: http.StatusGatewayTimeout, Kind: "stream_silence_limit", Message: message,
	})
}

// Reuse the wait-limit classification so handlers neither replay nor penalize
// an account after a generation has already been accepted.
func openAIStreamSilenceError(err error) error {
	// Report only when the consumer rejects this attempt: a successful terminal
	// can precede a body left open by the provider and must retain success billing.
	var readErr *openAIStreamSilenceReadError
	if errors.As(err, &readErr) && readErr.onLimit != nil {
		readErr.once.Do(readErr.onLimit)
	}
	return errors.Join(err, &UpstreamFailoverError{
		StatusCode: http.StatusGatewayTimeout, ResponseBody: openAIUpstreamWaitLimitFailoverBody,
		Scope: GatewayFailureScopeRequest, Reason: upstreamWaitLimitReason, NextAccountAction: NextAccountStop,
	})
}

func writeOpenAIStreamSilenceError(c *gin.Context, stream bool) {
	if c == nil || c.Request == nil || c.Request.Context().Err() != nil {
		return
	}
	StopOpenAICompactSSEKeepaliveCommitted(c)
	MarkResponseCommitted(c)
	message := "Upstream did not respond in time"
	anthropic := strings.HasSuffix(c.Request.URL.Path, "/messages")
	if stream {
		c.Header("Content-Type", "text/event-stream")
		switch {
		case anthropic:
			_, _ = fmt.Fprint(c.Writer, buildAnthropicStreamErrorSSE("upstream_error", message))
		case strings.HasSuffix(c.Request.URL.Path, "/chat/completions"):
			_, _ = fmt.Fprintf(c.Writer, "data: %s\n\n", openAIUpstreamWaitLimitFailoverBody)
		default:
			_, _ = fmt.Fprint(c.Writer, buildOpenAIResponseFailedSSE("", "", nil, message))
		}
		c.Writer.Flush()
		return
	}
	if anthropic {
		writeAnthropicError(c, http.StatusGatewayTimeout, "upstream_error", message)
	} else {
		c.Data(http.StatusGatewayTimeout, "application/json", openAIUpstreamWaitLimitFailoverBody)
	}
}

// A complete successful JSON document also ends a generation without HTTP EOF.
func openAIStreamSilenceHasCompletedJSON(body []byte) bool {
	if !gjson.ValidBytes(body) {
		return false
	}
	root := gjson.ParseBytes(body)
	return root.IsObject() && root.Get("status").String() == "completed" && !openAIBufferedUpstreamError(root, "")
}

// A terminal success, not EOF, completes a provider's SSE generation.
func openAIStreamSilenceHasSuccessfulTerminal(body []byte) bool {
	kind, _, ok := extractOpenAISSETerminalEvent(string(body))
	return ok && (kind == "response.completed" || kind == "response.done")
}

func (s *OpenAIGatewayService) openAIStreamSilenceHTTPError(c *gin.Context, resp *http.Response, body []byte, err error, model, billingModel, upstreamModel string, stream bool, start time.Time) (*OpenAIForwardResult, error) {
	writeOpenAIStreamSilenceError(c, stream)
	return openAIStreamSilenceResult(resp, s.openAIStreamSilenceUsage(body), model, billingModel, upstreamModel, stream, start), openAIStreamSilenceError(err)
}

func (s *OpenAIGatewayService) openAIStreamSilenceUsage(body []byte) OpenAIUsage {
	if bodyHasSSEFraming(body) {
		return *s.parseSSEUsageFromBody(string(body))
	}
	usage, _ := extractOpenAIUsageFromJSONBytes(body)
	return usage
}

func openAIStreamSilenceResult(resp *http.Response, usage OpenAIUsage, model, billingModel, upstreamModel string, stream bool, start time.Time) *OpenAIForwardResult {
	return &OpenAIForwardResult{
		RequestID: resp.Header.Get("x-request-id"), UpstreamHeaders: resp.Header,
		Usage: usage, Model: model, BillingModel: billingModel, UpstreamModel: upstreamModel,
		Stream: stream, Duration: time.Since(start), NonBillableUpstreamError: true,
	}
}
