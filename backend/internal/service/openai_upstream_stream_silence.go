package service

import (
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
	mu          sync.Mutex
	ctx         context.Context
	clientCtx   context.Context
	cancel      context.CancelCauseFunc
	timer       *time.Timer
	limit       time.Duration
	progress    atomic.Pointer[openAIStreamSilenceProgress]
	finished    bool
	expired     bool
	closeOnce   sync.Once
	closeErr    error
	lineStarted bool
	comment     bool
	failure     *openAIStreamSilenceReadError
}

func newOpenAIStreamSilenceBody(ctx context.Context, cancel context.CancelCauseFunc, body io.ReadCloser, onLimit func()) *openAIStreamSilenceBody {
	b := &openAIStreamSilenceBody{ReadCloser: body, ctx: ctx, cancel: cancel, limit: deepSeekStreamSilenceLimit, failure: &openAIStreamSilenceReadError{onLimit: onLimit}}
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

func (b *openAIStreamSilenceBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	// Publish returned output/EOF before contending with the timer. Otherwise a
	// completed read queued behind expire could be mistaken for provider silence.
	if output := b.hasOutput(p[:n]); output || err != nil {
		for previous := b.progress.Load(); previous != nil; previous = b.progress.Load() {
			progress := &openAIStreamSilenceProgress{lastOutput: previous.lastOutput, finished: err != nil}
			if output {
				progress.lastOutput = time.Now()
			}
			if b.progress.CompareAndSwap(previous, progress) {
				break
			}
		}
	}
	b.mu.Lock()
	if err != nil {
		b.finished = true
		b.timer.Stop()
	}
	expired := b.expired
	b.mu.Unlock()
	if expired {
		return n, b.failure
	}
	return n, err
}

// Keep line state across reads: a split comment must never buy another interval.
func (b *openAIStreamSilenceBody) hasOutput(p []byte) bool {
	output := false
	for _, ch := range p {
		switch ch {
		case '\r', '\n':
			b.lineStarted, b.comment = false, false
		case ' ', '\t':
			if b.lineStarted && !b.comment {
				output = true
			}
		default:
			if !b.lineStarted {
				b.lineStarted = true
				b.comment = ch == ':'
			}
			output = output || !b.comment
		}
	}
	return output
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
	return root.IsObject() && root.Get("status").String() == "completed" && !openAIExplicitUpstreamError(root, "")
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
