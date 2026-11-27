//go:build unit

package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type preauthStaticUpstream struct {
	fn func(req *http.Request) (*http.Response, error)
}

func (u *preauthStaticUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return u.fn(req)
}

func (u *preauthStaticUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, accountID, concurrency)
}

type preauthClosingBody struct {
	*strings.Reader
	closed atomic.Bool
}

func newPreauthClosingBody(content string) *preauthClosingBody {
	return &preauthClosingBody{Reader: strings.NewReader(content)}
}

func (b *preauthClosingBody) Close() error {
	b.closed.Store(true)
	return nil
}

// newFailingStreamPreauthGuard constructs a live streaming BalancePreauthorizationGuard
// whose wallet rejects the first top-up with LiveBalanceOutcomeInsufficient (403).
// Crossing the initial 256-token output window (~1024 bytes) triggers ErrBalanceWithholdingFailed.
func newFailingStreamPreauthGuard(t *testing.T) (*BalancePreauthorizationGuard, *preauthorizationFixture) {
	t.Helper()
	fixture := newPreauthorizationFixture()
	fixture.wallet.topUp = []LiveBalanceResult{{Outcome: LiveBalanceOutcomeInsufficient, State: LiveBalanceAttemptAuthorized}}
	guard := streamingPreauthorizationGuard(t, fixture)
	return guard, fixture
}

func newPreauthGinContext(t *testing.T, path string, guard *BalancePreauthorizationGuard) (*gin.Context, *httptest.ResponseRecorder, context.Context) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	ctx := ContextWithBalancePreauthorizationGuard(context.Background(), guard)
	c.Request = httptest.NewRequest(http.MethodPost, path, http.NoBody).WithContext(ctx)
	return c, rec, ctx
}

// longChunkPayload emits a deterministic ASCII string exceeding the 256-token
// (~1024-byte) preauthorization window across a few frames.
func longChunkPayload(repeat int) string {
	return strings.Repeat("alpha beta gamma delta epsilon zeta eta theta ", repeat)
}

func buildAnthropicAbortSSE() string {
	chunk := longChunkPayload(12) // ~552 bytes per delta; 4 deltas > 2200 bytes > 256 tokens
	return strings.Join([]string{
		"event: message_start",
		`data: {"type":"message_start","message":{"id":"msg_preauth","type":"message","role":"assistant","model":"claude-sonnet-4-5","content":[],"usage":{"input_tokens":42,"cache_read_input_tokens":10,"output_tokens":1}}}`,
		"",
		"event: content_block_start",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		"",
		"event: content_block_delta",
		fmt.Sprintf(`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":%q}}`, chunk),
		"",
		"event: content_block_delta",
		fmt.Sprintf(`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":%q}}`, chunk),
		"",
		"event: content_block_delta",
		fmt.Sprintf(`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":%q}}`, chunk),
		"",
		"event: content_block_delta",
		fmt.Sprintf(`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":%q}}`, chunk),
		"",
		"event: message_delta",
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":999}}`,
		"",
		"event: message_stop",
		`data: {"type":"message_stop"}`,
		"",
	}, "\n")
}

func buildResponsesAbortSSE() string {
	chunk := longChunkPayload(12)
	return strings.Join([]string{
		"event: response.created",
		`data: {"type":"response.created","response":{"id":"resp_preauth","status":"in_progress","model":"gpt-5.1"}}`,
		"",
		"event: response.output_text.delta",
		fmt.Sprintf(`data: {"type":"response.output_text.delta","delta":%q}`, chunk),
		"",
		"event: response.output_text.delta",
		fmt.Sprintf(`data: {"type":"response.output_text.delta","delta":%q}`, chunk),
		"",
		"event: response.output_text.delta",
		fmt.Sprintf(`data: {"type":"response.output_text.delta","delta":%q}`, chunk),
		"",
		"event: response.output_text.delta",
		fmt.Sprintf(`data: {"type":"response.output_text.delta","delta":%q}`, chunk),
		"",
		"event: response.completed",
		`data: {"type":"response.completed","response":{"id":"resp_preauth","status":"completed","model":"gpt-5.1","usage":{"input_tokens":50,"output_tokens":999}}}`,
		"",
	}, "\n")
}

func buildOpenAIChatAbortSSE(deltaField string) string {
	chunk := longChunkPayload(12)
	makeLine := func(text string) string {
		switch deltaField {
		case "reasoning_content":
			return fmt.Sprintf(`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","model":"gpt-5.1","choices":[{"index":0,"delta":{"reasoning_content":%q}}]}`, text)
		case "tool_calls":
			return fmt.Sprintf(`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","model":"gpt-5.1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"run_tool","arguments":%q}}]}}]}`, text)
		default:
			return fmt.Sprintf(`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","model":"gpt-5.1","choices":[{"index":0,"delta":{"content":%q}}]}`, text)
		}
	}
	return strings.Join([]string{
		makeLine(chunk),
		"",
		makeLine(chunk),
		"",
		makeLine(chunk),
		"",
		makeLine(chunk),
		"",
		"data: [DONE]",
		"",
	}, "\n")
}

func buildGeminiAbortSSE(partKind string, wrapped bool) string {
	chunk := longChunkPayload(12)
	makeData := func() string {
		var part string
		switch partKind {
		case "thought":
			part = fmt.Sprintf(`{"thought":true,"text":%q}`, chunk)
		case "thought_string":
			part = fmt.Sprintf(`{"thought":%q}`, chunk)
		case "functionCall":
			part = fmt.Sprintf(`{"functionCall":{"name":"lookup_records","args":{"payload":%q}}}`, chunk)
		default:
			part = fmt.Sprintf(`{"text":%q}`, chunk)
		}
		inner := fmt.Sprintf(`{"candidates":[{"content":{"role":"model","parts":[%s]}}]}`, part)
		if wrapped {
			return fmt.Sprintf(`data: {"response":%s}`, inner)
		}
		return "data: " + inner
	}
	return strings.Join([]string{
		makeData(),
		"",
		makeData(),
		"",
		makeData(),
		"",
		makeData(),
		"",
	}, "\n")
}

// ============================================================================
// Part 1 (P0): Streaming paths MUST NEVER refund delivered output on top-up abort
// ============================================================================

func TestAnthropicStreamBalanceAbort_BillsDeliveredOutputInsteadOfRefunding(t *testing.T) {
	guard, fixture := newFailingStreamPreauthGuard(t)
	c, rec, ctx := newPreauthGinContext(t, "/v1/messages", guard)

	body := newPreauthClosingBody(buildAnthropicAbortSSE())
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"text/event-stream"}, "X-Request-Id": {"rid-anth-abort"}},
		Body:       body,
	}
	svc := &GatewayService{cfg: &config.Config{}}
	account := &Account{ID: 101, Platform: PlatformAnthropic, Type: AccountTypeAPIKey}
	reqBody := []byte(`{"model":"claude-sonnet-4-5","stream":true,"messages":[{"role":"user","content":"Explain preauthorization billing invariants in detail."}]}`)

	sr, err := svc.handleStreamingResponse(ctx, resp, c, account, time.Now(), "claude-sonnet-4-5", "claude-sonnet-4-5", false)
	require.ErrorIs(t, err, ErrBalanceWithholdingFailed)
	require.NotNil(t, sr, "balance abort must return non-nil streamingResult with progress")
	require.True(t, body.closed.Load(), "upstream body must be closed immediately on balance abort")
	require.Greater(t, fixture.wallet.topUpCalls, 0)
	require.NotContains(t, rec.Body.String(), "message_stop", "must abort before terminal frame")

	applyIncompleteAnthropicStreamUsage(account, sr, reqBody, "claude-sonnet-4-5", "rid-anth-abort", err)
	require.Equal(t, 42, sr.usage.InputTokens)
	require.Equal(t, 10, sr.usage.CacheReadInputTokens)
	require.Greater(t, sr.usage.OutputTokens, 100, "provisional output_tokens=1 must be replaced by estimated delivered output")

	forwardRes := partialStreamUsageResult(c, resp, sr, "claude-sonnet-4-5", "claude-sonnet-4-5", time.Now(), err)
	require.NotNil(t, forwardRes)
	require.Greater(t, forwardRes.Usage.OutputTokens, 100)
	require.Equal(t, 42, forwardRes.Usage.InputTokens)
}

func TestGeminiMessagesCompatStreamBalanceAbort_ObservesTextThoughtFunctionCallAndBills(t *testing.T) {
	for _, kind := range []string{"text", "thought", "functionCall"} {
		t.Run(kind, func(t *testing.T) {
			guard, fixture := newFailingStreamPreauthGuard(t)
			c, _, ctx := newPreauthGinContext(t, "/v1/messages", guard)

			sseBody := newPreauthClosingBody(buildGeminiAbortSSE(kind, false))
			upstream := &preauthStaticUpstream{
				fn: func(*http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": {"text/event-stream"}},
						Body:       sseBody,
					}, nil
				},
			}
			svc := &GeminiMessagesCompatService{httpUpstream: upstream, cfg: &config.Config{}}
			account := &Account{
				ID:       201,
				Platform: PlatformGemini,
				Type:     AccountTypeAPIKey,
				Credentials: map[string]any{
					"api_key":  "test-key-must-not-leak",
					"base_url": "https://generativelanguage.googleapis.com",
				},
			}
			reqBody := []byte(`{"model":"gemini-2.5-pro","stream":true,"messages":[{"role":"user","content":"Generate a detailed response for preauth billing test."}]}`)

			res, err := svc.Forward(ctx, c, account, reqBody)
			require.ErrorIs(t, err, ErrBalanceWithholdingFailed)
			require.NotNil(t, res, "balance abort must return billable ForwardResult (never refund 100%)")
			require.Greater(t, fixture.wallet.topUpCalls, 0)
			require.Greater(t, res.Usage.OutputTokens, 100, "delivered output tokens must be billed for kind=%s", kind)
			require.Greater(t, res.Usage.InputTokens+res.Usage.CacheReadInputTokens, 0, "input tokens must be billed")
		})
	}
}

func TestGeminiNativeOpenAICompatStreamBalanceAbort_ObservesContentReasoningToolCallsAndBills(t *testing.T) {
	for _, field := range []string{"content", "reasoning_content", "tool_calls"} {
		t.Run(field, func(t *testing.T) {
			guard, fixture := newFailingStreamPreauthGuard(t)
			c, _, ctx := newPreauthGinContext(t, "/v1beta/models/gemini-2.5-pro:streamGenerateContent", guard)

			sseBody := newPreauthClosingBody(buildOpenAIChatAbortSSE(field))
			upstream := &preauthStaticUpstream{
				fn: func(*http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": {"text/event-stream"}},
						Body:       sseBody,
					}, nil
				},
			}
			svc := &GeminiMessagesCompatService{httpUpstream: upstream, cfg: &config.Config{}}
			account := &Account{
				ID:       202,
				Platform: PlatformGemini,
				Type:     AccountTypeAPIKey,
				Credentials: map[string]any{
					"api_key":      "test-key-must-not-leak",
					"base_url":     "https://api.example.invalid/v1",
					"api_protocol": "openai",
				},
			}
			reqBody := []byte(`{"contents":[{"role":"user","parts":[{"text":"Generate a detailed answer for OpenAI-compat Gemini preauth test."}]}]}`)

			res, err := svc.forwardGeminiNativeViaOpenAICompat(ctx, c, account, "gemini-2.5-pro", "gemini-2.5-pro", true, reqBody)
			require.ErrorIs(t, err, ErrBalanceWithholdingFailed)
			require.NotNil(t, res, "balance abort must return billable ForwardResult")
			require.Greater(t, fixture.wallet.topUpCalls, 0)
			require.Greater(t, res.Usage.OutputTokens, 100, "delivered %s must be billed", field)
			require.Greater(t, res.Usage.InputTokens+res.Usage.CacheReadInputTokens, 0, "input tokens must be billed")
		})
	}
}

// ============================================================================
// Part 2 (P1 — BILL-H4): Stream Hint + Non-Streaming 4096 Default
// ============================================================================

func TestBalancePreauthorization_StreamHintDistinguishesGeminiStreamVsNonStream(t *testing.T) {
	newFixtureWithPrice := func() *preauthorizationFixture {
		f := newPreauthorizationFixture()
		f.calculator.outputUnitPrice = 0.001
		return f
	}

	// Case 1: Gemini :streamGenerateContent omits maxOutputTokens and JSON "stream".
	// EstimateBalancePreauthorizationTokens returns 4096, and WithBalancePreauthorizationStreamHint(ctx, true)
	// narrows the hold back to the 256-token incremental streaming window.
	streamFixture := newFixtureWithPrice()
	streamReq := balancePreauthorizationTestRequest()
	streamReq.InitialOutputWindowTokens = DefaultBalancePreauthorizationNonStreamOutputWindow
	streamGuard, err := streamFixture.service.Preauthorize(WithBalancePreauthorizationStreamHint(context.Background(), true), streamReq)
	require.NoError(t, err)
	require.NotNil(t, streamGuard)

	// Case 2: Gemini :generateContent where an attacker injected "stream": true into JSON body.
	// EstimateBalancePreauthorizationTokens returns 256, and WithBalancePreauthorizationStreamHint(ctx, false)
	// upgrades the hold to the 4096-token non-streaming window.
	nonStreamFixture := newFixtureWithPrice()
	nonStreamReq := balancePreauthorizationTestRequest()
	nonStreamReq.InitialOutputWindowTokens = DefaultBalancePreauthorizationOutputWindow
	nonStreamGuard, err := nonStreamFixture.service.Preauthorize(WithBalancePreauthorizationStreamHint(context.Background(), false), nonStreamReq)
	require.NoError(t, err)
	require.NotNil(t, nonStreamGuard)

	require.Greater(t, nonStreamGuard.HoldAmount(), streamGuard.HoldAmount()*10,
		"non-streaming 4096-token default hold must vastly exceed streaming 256-token window")
	require.InDelta(t, 0.028+float64(DefaultBalancePreauthorizationOutputWindow)*0.001, streamGuard.HoldAmount(), 0.05)
	require.InDelta(t, float64(DefaultBalancePreauthorizationNonStreamOutputWindow-DefaultBalancePreauthorizationOutputWindow)*0.001, nonStreamGuard.HoldAmount()-streamGuard.HoldAmount(), 1e-6)
}

// ============================================================================
// Part 3 (P1 — BILL-H4): Unified ObserveStreamingOutput Across Missing Paths
// ============================================================================

func TestAnthropicPassthroughAndBedrockStreamBalanceAbort_AbortsAndBills(t *testing.T) {
	reqBody := []byte(`{"model":"claude-sonnet-4-5","stream":true,"messages":[{"role":"user","content":"Long prompt for passthrough/bedrock preauth billing test."}]}`)

	t.Run("anthropic_passthrough", func(t *testing.T) {
		guard, fixture := newFailingStreamPreauthGuard(t)
		c, _, ctx := newPreauthGinContext(t, "/v1/messages", guard)
		body := newPreauthClosingBody(buildAnthropicAbortSSE())
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"text/event-stream"}, "X-Request-Id": {"rid-pt"}},
			Body:       body,
		}
		svc := &GatewayService{cfg: &config.Config{}}
		account := &Account{ID: 301, Platform: PlatformAnthropic, Type: AccountTypeAPIKey}

		sr, err := svc.handleStreamingResponseAnthropicAPIKeyPassthrough(ctx, resp, c, account, time.Now(), "claude-sonnet-4-5")
		require.ErrorIs(t, err, ErrBalanceWithholdingFailed)
		require.NotNil(t, sr)
		require.True(t, body.closed.Load())
		require.Greater(t, fixture.wallet.topUpCalls, 0)

		applyIncompleteAnthropicStreamUsage(account, sr, reqBody, "claude-sonnet-4-5", "rid-anth-abort", err)
		require.Greater(t, sr.usage.OutputTokens, 100)
		require.NotNil(t, partialStreamUsageResult(c, resp, sr, "claude-sonnet-4-5", "claude-sonnet-4-5", time.Now(), err))
	})

	t.Run("bedrock_stream", func(t *testing.T) {
		guard, fixture := newFailingStreamPreauthGuard(t)
		c, _, ctx := newPreauthGinContext(t, "/v1/messages", guard)
		chunk := longChunkPayload(12)
		frames := bedrockFrames(
			bedrockStartEvent(),
			bedrockBlockStartEvent,
			bedrockDeltaEvent(chunk),
			bedrockDeltaEvent(chunk),
			bedrockDeltaEvent(chunk),
			bedrockDeltaEvent(chunk),
		)
		body := newPreauthClosingBody(frames)
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"application/vnd.amazon.eventstream"}, "X-Amzn-Requestid": {"rid-br"}},
			Body:       body,
		}
		svc := &GatewayService{cfg: &config.Config{}}
		account := &Account{ID: 302, Platform: PlatformAnthropic, Type: AccountTypeBedrock}

		sr, err := svc.handleBedrockStreamingResponse(ctx, resp, c, account, time.Now(), "claude-sonnet-4-5")
		require.ErrorIs(t, err, ErrBalanceWithholdingFailed)
		require.NotNil(t, sr)
		require.True(t, body.closed.Load())
		require.Greater(t, fixture.wallet.topUpCalls, 0)

		applyIncompleteAnthropicStreamUsage(account, sr, reqBody, "claude-sonnet-4-5", "rid-anth-abort", err)
		require.Greater(t, sr.usage.OutputTokens, 100)
		require.NotNil(t, partialStreamUsageResult(c, resp, sr, "claude-sonnet-4-5", "claude-sonnet-4-5", time.Now(), err))
	})
}

func TestGatewayForwardAsChatAndResponsesBalanceAbort_StreamingAndBuffered(t *testing.T) {
	account := &Account{ID: 303, Platform: PlatformAnthropic, Type: AccountTypeAPIKey}
	reqBody := []byte(`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"Prompt for forward-as compat preauth test."}]}`)
	est := anthropicEstimateSource{account: account, body: reqBody}
	svc := &GatewayService{cfg: &config.Config{}}

	t.Run("cc_streaming", func(t *testing.T) {
		guard, fixture := newFailingStreamPreauthGuard(t)
		c, _, _ := newPreauthGinContext(t, "/v1/chat/completions", guard)
		body := newPreauthClosingBody(buildAnthropicAbortSSE())
		resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: body}

		res, err := svc.handleCCStreamingFromAnthropic(resp, c, "claude-sonnet-4-5", "claude-sonnet-4-5", nil, time.Now(), true, est)
		require.ErrorIs(t, err, ErrBalanceWithholdingFailed)
		require.NotNil(t, res)
		require.True(t, body.closed.Load())
		require.Greater(t, fixture.wallet.topUpCalls, 0)
		require.Greater(t, res.Usage.OutputTokens, 100)
	})

	t.Run("cc_buffered", func(t *testing.T) {
		guard, fixture := newFailingStreamPreauthGuard(t)
		c, rec, _ := newPreauthGinContext(t, "/v1/chat/completions", guard)
		body := newPreauthClosingBody(buildAnthropicAbortSSE())
		resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: body}

		res, err := svc.handleCCBufferedFromAnthropic(resp, c, "claude-sonnet-4-5", "claude-sonnet-4-5", nil, time.Now(), est)
		require.ErrorIs(t, err, ErrBalanceWithholdingFailed)
		require.NotNil(t, res)
		require.Equal(t, http.StatusForbidden, rec.Code)
		require.True(t, body.closed.Load())
		require.Greater(t, fixture.wallet.topUpCalls, 0)
		require.Greater(t, res.Usage.OutputTokens, 100)
	})

	t.Run("responses_streaming", func(t *testing.T) {
		guard, fixture := newFailingStreamPreauthGuard(t)
		c, _, _ := newPreauthGinContext(t, "/v1/responses", guard)
		body := newPreauthClosingBody(buildAnthropicAbortSSE())
		resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: body}

		res, err := svc.handleResponsesStreamingResponse(resp, c, "claude-sonnet-4-5", "claude-sonnet-4-5", nil, time.Now(), apicompat.ResponsesClientToolMapping{}, est)
		require.ErrorIs(t, err, ErrBalanceWithholdingFailed)
		require.NotNil(t, res)
		require.True(t, body.closed.Load())
		require.Greater(t, fixture.wallet.topUpCalls, 0)
		require.Greater(t, res.Usage.OutputTokens, 100)
	})

	t.Run("responses_buffered", func(t *testing.T) {
		guard, fixture := newFailingStreamPreauthGuard(t)
		c, rec, _ := newPreauthGinContext(t, "/v1/responses", guard)
		body := newPreauthClosingBody(buildAnthropicAbortSSE())
		resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: body}

		res, err := svc.handleResponsesBufferedStreamingResponse(resp, c, "claude-sonnet-4-5", "claude-sonnet-4-5", nil, time.Now(), apicompat.ResponsesClientToolMapping{}, est)
		require.ErrorIs(t, err, ErrBalanceWithholdingFailed)
		require.NotNil(t, res)
		require.Equal(t, http.StatusForbidden, rec.Code)
		require.True(t, body.closed.Load())
		require.Greater(t, fixture.wallet.topUpCalls, 0)
		require.Greater(t, res.Usage.OutputTokens, 100)
	})
}

func TestGeminiNativeAndChatCompatBalanceAbort_StreamingAndBuffered(t *testing.T) {
	geminiAccount := &Account{
		ID:       304,
		Platform: PlatformGemini,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "test-key-must-not-leak",
			"base_url": "https://generativelanguage.googleapis.com",
		},
	}

	t.Run("native_streaming", func(t *testing.T) {
		guard, fixture := newFailingStreamPreauthGuard(t)
		c, _, ctx := newPreauthGinContext(t, "/v1beta/models/gemini-2.5-pro:streamGenerateContent", guard)
		sseBody := newPreauthClosingBody(buildGeminiAbortSSE("text", false))
		upstream := &preauthStaticUpstream{fn: func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: sseBody}, nil
		}}
		svc := &GeminiMessagesCompatService{httpUpstream: upstream, cfg: &config.Config{}}
		reqBody := []byte(`{"contents":[{"role":"user","parts":[{"text":"Prompt for native Gemini streaming preauth test."}]}]}`)

		res, err := svc.ForwardNative(ctx, c, geminiAccount, "gemini-2.5-pro", "streamGenerateContent", true, reqBody)
		require.ErrorIs(t, err, ErrBalanceWithholdingFailed)
		require.NotNil(t, res)
		require.Greater(t, fixture.wallet.topUpCalls, 0)
		require.Greater(t, res.Usage.OutputTokens, 100)
		require.Greater(t, res.Usage.InputTokens+res.Usage.CacheReadInputTokens, 0)
	})

	t.Run("collectGeminiSSEObservedWithContext_buffered_abort", func(t *testing.T) {
		guard, fixture := newFailingStreamPreauthGuard(t)
		ctx := ContextWithBalancePreauthorizationGuard(context.Background(), guard)
		_, _, stats, err := collectGeminiSSEObservedWithContext(ctx, strings.NewReader(buildGeminiAbortSSE("text", false)), false, nil)
		require.ErrorIs(t, err, ErrBalanceWithholdingFailed)
		require.Greater(t, fixture.wallet.topUpCalls, 0)
		require.Greater(t, stats.meter.tokens(), 100)
	})

	t.Run("chat_completions_compat_streaming", func(t *testing.T) {
		guard, fixture := newFailingStreamPreauthGuard(t)
		c, _, ctx := newPreauthGinContext(t, "/v1/chat/completions", guard)
		sseBody := newPreauthClosingBody(buildGeminiAbortSSE("thought", false))
		upstream := &preauthStaticUpstream{fn: func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: sseBody}, nil
		}}
		svc := &GeminiMessagesCompatService{httpUpstream: upstream, cfg: &config.Config{}}
		reqBody := []byte(`{"model":"gemini-2.5-pro","stream":true,"messages":[{"role":"user","content":"Prompt for Gemini CC streaming preauth test."}]}`)

		res, err := svc.ForwardAsChatCompletions(ctx, c, geminiAccount, reqBody)
		require.ErrorIs(t, err, ErrBalanceWithholdingFailed)
		require.NotNil(t, res)
		require.Greater(t, fixture.wallet.topUpCalls, 0)
		require.Greater(t, res.Usage.OutputTokens, 100)
		require.Greater(t, res.Usage.InputTokens+res.Usage.CacheReadInputTokens, 0)
	})
}

func TestAntigravityStreamingAndBufferedBalanceAbort_AbortsAndBills(t *testing.T) {
	svc := newAntigravityTestService(&config.Config{})

	t.Run("gemini_streaming", func(t *testing.T) {
		guard, fixture := newFailingStreamPreauthGuard(t)
		c, _, _ := newPreauthGinContext(t, "/v1beta/models/gemini-2.5-pro:streamGenerateContent", guard)
		body := newPreauthClosingBody(buildGeminiAbortSSE("text", true))
		resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: body}

		res, err := svc.handleGeminiStreamingResponse(c, resp, time.Now())
		require.ErrorIs(t, err, ErrBalanceWithholdingFailed)
		require.NotNil(t, res)
		require.True(t, res.balanceAborted)
		require.Greater(t, res.outputTokens, 100)
		require.True(t, body.closed.Load())
		require.Greater(t, fixture.wallet.topUpCalls, 0)
	})

	t.Run("gemini_buffered", func(t *testing.T) {
		guard, fixture := newFailingStreamPreauthGuard(t)
		c, rec, _ := newPreauthGinContext(t, "/v1beta/models/gemini-2.5-pro:generateContent", guard)
		body := newPreauthClosingBody(buildGeminiAbortSSE("text", true))
		resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: body}

		res, err := svc.handleGeminiStreamToNonStreaming(c, resp, time.Now())
		require.ErrorIs(t, err, ErrBalanceWithholdingFailed)
		require.NotNil(t, res)
		require.True(t, res.balanceAborted)
		require.Greater(t, res.outputTokens, 100)
		require.Equal(t, http.StatusForbidden, rec.Code)
		require.True(t, body.closed.Load())
		require.Greater(t, fixture.wallet.topUpCalls, 0)
	})

	t.Run("claude_streaming", func(t *testing.T) {
		guard, fixture := newFailingStreamPreauthGuard(t)
		c, _, _ := newPreauthGinContext(t, "/v1/messages", guard)
		body := newPreauthClosingBody(buildGeminiAbortSSE("text", true))
		resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: body}

		res, err := svc.handleClaudeStreamingResponse(c, resp, time.Now(), "claude-sonnet-4-5")
		require.ErrorIs(t, err, ErrBalanceWithholdingFailed)
		require.NotNil(t, res)
		require.True(t, res.balanceAborted)
		require.Greater(t, res.outputTokens, 100)
		require.True(t, body.closed.Load())
		require.Greater(t, fixture.wallet.topUpCalls, 0)
	})

	t.Run("claude_buffered", func(t *testing.T) {
		guard, fixture := newFailingStreamPreauthGuard(t)
		c, _, _ := newPreauthGinContext(t, "/v1/messages", guard)
		body := newPreauthClosingBody(buildGeminiAbortSSE("text", true))
		resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: body}

		_, res, err := svc.collectClaudeStreamResponse(c, resp, time.Now(), "claude-sonnet-4-5")
		require.ErrorIs(t, err, ErrBalanceWithholdingFailed)
		require.NotNil(t, res)
		require.True(t, res.balanceAborted)
		require.Greater(t, res.outputTokens, 100)
		require.True(t, body.closed.Load())
		require.Greater(t, fixture.wallet.topUpCalls, 0)
	})

	t.Run("compat_streaming", func(t *testing.T) {
		guard, fixture := newFailingStreamPreauthGuard(t)
		c, _, _ := newPreauthGinContext(t, "/v1/chat/completions", guard)
		body := newPreauthClosingBody(buildGeminiAbortSSE("text", true))
		resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: body}

		res, err := svc.handleAntigravityCompatStream(c, resp, time.Now(), "claude-sonnet-4-5", newAntigravityResponsesStreamAdapter("claude-sonnet-4-5", apicompat.ResponsesClientToolMapping{}), "[Antigravity Responses]")
		require.ErrorIs(t, err, ErrBalanceWithholdingFailed)
		require.NotNil(t, res)
		require.True(t, res.balanceAborted)
		require.Greater(t, res.outputTokens, 100)
		require.True(t, body.closed.Load())
		require.Greater(t, fixture.wallet.topUpCalls, 0)
	})

	t.Run("upstream_passthrough", func(t *testing.T) {
		guard, fixture := newFailingStreamPreauthGuard(t)
		c, _, _ := newPreauthGinContext(t, "/antigravity/v1/messages", guard)
		body := newPreauthClosingBody(buildAnthropicAbortSSE())
		resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: body}

		res := svc.streamUpstreamResponse(c, resp, time.Now())
		require.NotNil(t, res)
		require.True(t, res.balanceAborted)
		require.ErrorIs(t, res.balanceAbortErr, ErrBalanceWithholdingFailed)
		require.Greater(t, res.outputTokens, 100)
		require.True(t, body.closed.Load())
		require.Greater(t, fixture.wallet.topUpCalls, 0)
	})
}

func TestOpenAICompatStreamingAndBufferedBalanceAbort_AbortsAndBills(t *testing.T) {
	svc := &OpenAIGatewayService{cfg: &config.Config{}}
	account := &Account{ID: 401, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	stashedBody := []byte(`{"model":"gpt-5.1","input":"Long prompt text for OpenAI compat balance preauthorization abort billing tests."}`)

	t.Run("chat_streaming", func(t *testing.T) {
		guard, fixture := newFailingStreamPreauthGuard(t)
		c, _, _ := newPreauthGinContext(t, "/v1/chat/completions", guard)
		stashCompatUpstreamRequestBody(c, account, stashedBody)
		body := newPreauthClosingBody(buildResponsesAbortSSE())
		resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: body}

		res, err := svc.handleChatStreamingResponse(resp, c, account, "gpt-5.1", "gpt-5.1", "gpt-5.1", time.Now(), len(stashedBody))
		require.ErrorIs(t, err, ErrBalanceWithholdingFailed)
		require.NotNil(t, res)
		require.True(t, body.closed.Load())
		require.Greater(t, fixture.wallet.topUpCalls, 0)
		require.Greater(t, res.Usage.OutputTokens, 100)
		require.Greater(t, res.Usage.InputTokens, 0)
	})

	t.Run("chat_buffered", func(t *testing.T) {
		guard, fixture := newFailingStreamPreauthGuard(t)
		c, rec, _ := newPreauthGinContext(t, "/v1/chat/completions", guard)
		stashCompatUpstreamRequestBody(c, account, stashedBody)
		body := newPreauthClosingBody(buildResponsesAbortSSE())
		resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: body}

		res, err := svc.handleChatBufferedStreamingResponse(resp, c, account, "gpt-5.1", "gpt-5.1", "gpt-5.1", time.Now())
		require.ErrorIs(t, err, ErrBalanceWithholdingFailed)
		require.NotNil(t, res)
		require.Equal(t, http.StatusForbidden, rec.Code)
		require.True(t, body.closed.Load())
		require.Greater(t, fixture.wallet.topUpCalls, 0)
		require.Greater(t, res.Usage.OutputTokens, 100)
		require.Greater(t, res.Usage.InputTokens, 0)
	})

	t.Run("raw_chat_streaming", func(t *testing.T) {
		guard, fixture := newFailingStreamPreauthGuard(t)
		c, _, _ := newPreauthGinContext(t, "/v1/chat/completions", guard)
		stashCompatUpstreamRequestBody(c, account, stashedBody)
		body := newPreauthClosingBody(buildOpenAIChatAbortSSE("content"))
		resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: body}

		res, err := svc.streamRawChatCompletions(c, resp, account, "gpt-5.1", "gpt-5.1", "gpt-5.1", nil, nil, time.Now(), len(stashedBody))
		require.ErrorIs(t, err, ErrBalanceWithholdingFailed)
		require.NotNil(t, res)
		require.True(t, body.closed.Load())
		require.Greater(t, fixture.wallet.topUpCalls, 0)
		require.Greater(t, res.Usage.OutputTokens, 100)
		require.Greater(t, res.Usage.InputTokens, 0)
	})

	t.Run("messages_streaming", func(t *testing.T) {
		guard, fixture := newFailingStreamPreauthGuard(t)
		c, _, _ := newPreauthGinContext(t, "/v1/messages", guard)
		stashCompatUpstreamRequestBody(c, account, stashedBody)
		body := newPreauthClosingBody(buildResponsesAbortSSE())
		resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: body}

		res, err := svc.handleAnthropicStreamingResponse(resp, c, account, "gpt-5.1", "gpt-5.1", "gpt-5.1", time.Now())
		require.ErrorIs(t, err, ErrBalanceWithholdingFailed)
		require.NotNil(t, res)
		require.True(t, body.closed.Load())
		require.Greater(t, fixture.wallet.topUpCalls, 0)
		require.Greater(t, res.Usage.OutputTokens, 100)
		require.Greater(t, res.Usage.InputTokens, 0)
	})

	t.Run("messages_buffered", func(t *testing.T) {
		guard, fixture := newFailingStreamPreauthGuard(t)
		c, rec, _ := newPreauthGinContext(t, "/v1/messages", guard)
		stashCompatUpstreamRequestBody(c, account, stashedBody)
		body := newPreauthClosingBody(buildResponsesAbortSSE())
		resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: body}

		res, err := svc.handleAnthropicBufferedStreamingResponse(resp, c, account, "gpt-5.1", "gpt-5.1", "gpt-5.1", time.Now())
		require.ErrorIs(t, err, ErrBalanceWithholdingFailed)
		require.NotNil(t, res)
		require.Equal(t, http.StatusForbidden, rec.Code)
		require.True(t, body.closed.Load())
		require.Greater(t, fixture.wallet.topUpCalls, 0)
		require.Greater(t, res.Usage.OutputTokens, 100)
		require.Greater(t, res.Usage.InputTokens, 0)
	})

	t.Run("native_anthropic_streaming", func(t *testing.T) {
		guard, fixture := newFailingStreamPreauthGuard(t)
		c, _, ctx := newPreauthGinContext(t, "/v1/messages", guard)
		stashCompatUpstreamRequestBody(c, account, stashedBody)
		body := newPreauthClosingBody(buildAnthropicAbortSSE())
		resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: body}

		res, err := svc.handleNativeAnthropicStreamingResponse(ctx, resp, c, account, "claude-sonnet-4-5", "claude-sonnet-4-5", "claude-sonnet-4-5", nil, time.Now())
		require.ErrorIs(t, err, ErrBalanceWithholdingFailed)
		require.NotNil(t, res)
		require.True(t, body.closed.Load())
		require.Greater(t, fixture.wallet.topUpCalls, 0)
		require.Greater(t, res.Usage.OutputTokens, 100)
	})

	t.Run("cc_from_native_anthropic_streaming_and_buffered", func(t *testing.T) {
		guard1, fixture1 := newFailingStreamPreauthGuard(t)
		c1, _, _ := newPreauthGinContext(t, "/v1/chat/completions", guard1)
		stashCompatUpstreamRequestBody(c1, nil, stashedBody)
		body1 := newPreauthClosingBody(buildAnthropicAbortSSE())
		resp1 := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: body1}
		res1, err1 := svc.handleCCStreamingFromNativeAnthropic(resp1, c1, "claude-sonnet-4-5", "claude-sonnet-4-5", "claude-sonnet-4-5", nil, time.Now(), true)
		require.ErrorIs(t, err1, ErrBalanceWithholdingFailed)
		require.NotNil(t, res1)
		require.True(t, body1.closed.Load())
		require.Greater(t, fixture1.wallet.topUpCalls, 0)
		require.Greater(t, res1.Usage.OutputTokens, 100)

		guard2, fixture2 := newFailingStreamPreauthGuard(t)
		c2, rec2, _ := newPreauthGinContext(t, "/v1/chat/completions", guard2)
		stashCompatUpstreamRequestBody(c2, nil, stashedBody)
		body2 := newPreauthClosingBody(buildAnthropicAbortSSE())
		resp2 := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: body2}
		res2, err2 := svc.handleCCBufferedFromNativeAnthropic(resp2, c2, "claude-sonnet-4-5", "claude-sonnet-4-5", "claude-sonnet-4-5", nil, time.Now())
		require.ErrorIs(t, err2, ErrBalanceWithholdingFailed)
		require.NotNil(t, res2)
		require.Equal(t, http.StatusForbidden, rec2.Code)
		require.True(t, body2.closed.Load())
		require.Greater(t, fixture2.wallet.topUpCalls, 0)
		require.Greater(t, res2.Usage.OutputTokens, 100)
	})

	t.Run("responses_from_native_anthropic_streaming_and_buffered", func(t *testing.T) {
		guard1, fixture1 := newFailingStreamPreauthGuard(t)
		c1, _, _ := newPreauthGinContext(t, "/v1/responses", guard1)
		stashCompatUpstreamRequestBody(c1, nil, stashedBody)
		body1 := newPreauthClosingBody(buildAnthropicAbortSSE())
		resp1 := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: body1}
		res1, err1 := svc.handleResponsesStreamingFromNativeAnthropic(resp1, c1, "claude-sonnet-4-5", "claude-sonnet-4-5", "claude-sonnet-4-5", nil, time.Now(), apicompat.ResponsesClientToolMapping{})
		require.ErrorIs(t, err1, ErrBalanceWithholdingFailed)
		require.NotNil(t, res1)
		require.True(t, body1.closed.Load())
		require.Greater(t, fixture1.wallet.topUpCalls, 0)
		require.Greater(t, res1.Usage.OutputTokens, 100)

		guard2, fixture2 := newFailingStreamPreauthGuard(t)
		c2, rec2, _ := newPreauthGinContext(t, "/v1/responses", guard2)
		stashCompatUpstreamRequestBody(c2, nil, stashedBody)
		body2 := newPreauthClosingBody(buildAnthropicAbortSSE())
		resp2 := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: body2}
		res2, err2 := svc.handleResponsesBufferedFromNativeAnthropic(resp2, c2, "claude-sonnet-4-5", "claude-sonnet-4-5", "claude-sonnet-4-5", nil, time.Now(), apicompat.ResponsesClientToolMapping{})
		require.ErrorIs(t, err2, ErrBalanceWithholdingFailed)
		require.NotNil(t, res2)
		require.Equal(t, http.StatusForbidden, rec2.Code)
		require.True(t, body2.closed.Load())
		require.Greater(t, fixture2.wallet.topUpCalls, 0)
		require.Greater(t, res2.Usage.OutputTokens, 100)
	})

	t.Run("cc_fallback_messages_and_responses", func(t *testing.T) {
		guard1, fixture1 := newFailingStreamPreauthGuard(t)
		c1, _, _ := newPreauthGinContext(t, "/v1/messages", guard1)
		stashCompatUpstreamRequestBody(c1, nil, stashedBody)
		body1 := newPreauthClosingBody(buildOpenAIChatAbortSSE("content"))
		resp1 := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: body1}
		res1, err1 := svc.streamChatCompletionsAsAnthropic(c1, resp1, "gpt-5.1", "gpt-5.1", "gpt-5.1", nil, nil, time.Now())
		require.ErrorIs(t, err1, ErrBalanceWithholdingFailed)
		require.NotNil(t, res1)
		require.True(t, body1.closed.Load())
		require.Greater(t, fixture1.wallet.topUpCalls, 0)
		require.Greater(t, res1.Usage.OutputTokens, 100)

		guard2, fixture2 := newFailingStreamPreauthGuard(t)
		c2, _, _ := newPreauthGinContext(t, "/v1/responses", guard2)
		stashCompatUpstreamRequestBody(c2, nil, stashedBody)
		body2 := newPreauthClosingBody(buildOpenAIChatAbortSSE("reasoning_content"))
		resp2 := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: body2}
		res2, err2 := svc.streamChatCompletionsAsResponses(c2, resp2, "gpt-5.1", nil, nil, false, nil, nil, "gpt-5.1", "gpt-5.1", nil, nil, time.Now())
		require.ErrorIs(t, err2, ErrBalanceWithholdingFailed)
		require.NotNil(t, res2)
		require.True(t, body2.closed.Load())
		require.Greater(t, fixture2.wallet.topUpCalls, 0)
		require.Greater(t, res2.Usage.OutputTokens, 100)
	})

	t.Run("buffered_sse_to_json_responses_and_passthrough", func(t *testing.T) {
		guard1, fixture1 := newFailingStreamPreauthGuard(t)
		c1, rec1, ctx1 := newPreauthGinContext(t, "/v1/responses", guard1)
		body1 := newPreauthClosingBody(buildResponsesAbortSSE())
		resp1 := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: body1}
		res1, err1 := svc.handleNonStreamingResponse(ctx1, resp1, c1, account, "gpt-5.1", "gpt-5.1")
		require.ErrorIs(t, err1, ErrBalanceWithholdingFailed)
		require.NotNil(t, res1)
		require.True(t, res1.balanceAborted)
		require.Greater(t, res1.incompleteOutputTokens, 100)
		require.Equal(t, http.StatusForbidden, rec1.Code)
		require.True(t, body1.closed.Load())
		require.Greater(t, fixture1.wallet.topUpCalls, 0)

		guard2, fixture2 := newFailingStreamPreauthGuard(t)
		c2, rec2, ctx2 := newPreauthGinContext(t, "/v1/responses", guard2)
		body2 := newPreauthClosingBody(buildResponsesAbortSSE())
		resp2 := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: body2}
		res2, err2 := svc.handleNonStreamingResponsePassthrough(ctx2, resp2, c2, account, "gpt-5.1", "gpt-5.1")
		require.ErrorIs(t, err2, ErrBalanceWithholdingFailed)
		require.NotNil(t, res2)
		require.True(t, res2.balanceAborted)
		require.Greater(t, res2.incompleteOutputTokens, 100)
		require.Equal(t, http.StatusForbidden, rec2.Code)
		require.True(t, body2.closed.Load())
		require.Greater(t, fixture2.wallet.topUpCalls, 0)
	})
}

func TestOpenAIWSHTTPBridgeBalanceAbort_AbortsAndBills(t *testing.T) {
	guard, fixture := newFailingStreamPreauthGuard(t)
	c, _, ctx := newPreauthGinContext(t, "/v1/responses", guard)

	sseBody := newPreauthClosingBody(buildResponsesAbortSSE())
	upstream := &preauthStaticUpstream{
		fn: func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {"text/event-stream"}},
				Body:       sseBody,
			}, nil
		},
	}
	svc := &OpenAIGatewayService{
		cfg:          &config.Config{},
		httpUpstream: upstream,
	}
	account := &Account{
		ID:       402,
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "test-key-must-not-leak",
			"base_url": "https://api.example.invalid",
		},
	}
	payload := []byte(`{"type":"response.create","model":"gpt-5.1","input":"Generate a long response over WS HTTP bridge to trigger preauth top-up abort."}`)
	var written [][]byte
	res, err := svc.proxyOpenAIWSHTTPBridgeTurn(
		ctx, c, account, "test-key-must-not-leak",
		payload, len(payload),
		"gpt-5.1", "gpt-5.1", "", "", "", 1,
		func(msg []byte) error {
			written = append(written, append([]byte(nil), msg...))
			return nil
		},
	)
	require.ErrorIs(t, err, ErrBalanceWithholdingFailed)
	require.NotNil(t, res)
	require.NotEmpty(t, written)
	require.True(t, sseBody.closed.Load())
	require.Greater(t, fixture.wallet.topUpCalls, 0)
	require.Greater(t, res.Usage.OutputTokens, 100)
	require.Greater(t, res.Usage.InputTokens, 0)
}

func TestPreserveOpenAIStreamingResultOnError_KeepsAllTopUpAbortSentinels(t *testing.T) {
	base := &OpenAIForwardResult{Model: "gpt-5.1"}
	for _, sentinel := range []error{
		ErrBalanceWithholdingFailed,
		ErrBillingServiceUnavailable,
		ErrBalancePreauthorizationOwnershipTransferred,
	} {
		err := wrapStreamOutputHoldTopUpFailure(sentinel)
		got, gotErr := preserveOpenAIStreamingResultOnError(base, err)
		require.Same(t, base, got)
		require.ErrorIs(t, gotErr, sentinel)
		require.False(t, errors.Is(gotErr, io.EOF))
	}
}
