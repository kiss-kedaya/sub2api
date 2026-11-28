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
	"github.com/tidwall/gjson"
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
			// Allow the first chunk's top-up so chunk 1 is actually funded, emitted, and metered,
			// then reject chunk 2's top-up before emission so chunk 1's delivered output (>100 tokens)
			// is billed while rejected chunk 2 is excluded.
			fixture := newPreauthorizationFixture()
			fixture.wallet.topUp = []LiveBalanceResult{
				{Outcome: LiveBalanceOutcomeApplied, State: LiveBalanceAttemptAuthorized},
				{Outcome: LiveBalanceOutcomeInsufficient, State: LiveBalanceAttemptAuthorized},
			}
			guard := streamingPreauthorizationGuard(t, fixture)
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

	// Case 1: Stream hint true with omitted output window (0) uses 256-token window.
	streamFixture := newFixtureWithPrice()
	streamReq := balancePreauthorizationTestRequest()
	streamReq.InitialOutputWindowTokens = 0
	streamGuard, err := streamFixture.service.Preauthorize(WithBalancePreauthorizationStreamHint(context.Background(), true), streamReq)
	require.NoError(t, err)
	require.NotNil(t, streamGuard)

	// Case 2: Stream hint false with omitted output window (0) uses 4096-token window.
	nonStreamFixture := newFixtureWithPrice()
	nonStreamReq := balancePreauthorizationTestRequest()
	nonStreamReq.InitialOutputWindowTokens = 0
	nonStreamGuard, err := nonStreamFixture.service.Preauthorize(WithBalancePreauthorizationStreamHint(context.Background(), false), nonStreamReq)
	require.NoError(t, err)
	require.NotNil(t, nonStreamGuard)

	// Case 3: Explicit max_tokens=4096 on a streaming request is NEVER clobbered to 256 by stream hint.
	explicitStreamFixture := newFixtureWithPrice()
	explicitStreamReq := balancePreauthorizationTestRequest()
	explicitStreamReq.InitialOutputWindowTokens = DefaultBalancePreauthorizationNonStreamOutputWindow
	explicitStreamGuard, err := explicitStreamFixture.service.Preauthorize(WithBalancePreauthorizationStreamHint(context.Background(), true), explicitStreamReq)
	require.NoError(t, err)
	require.NotNil(t, explicitStreamGuard)

	require.Greater(t, nonStreamGuard.HoldAmount(), streamGuard.HoldAmount()*10,
		"non-streaming 4096-token default hold must vastly exceed streaming 256-token window")
	require.InDelta(t, 0.028+float64(DefaultBalancePreauthorizationOutputWindow)*0.001, streamGuard.HoldAmount(), 0.05)
	require.InDelta(t, float64(DefaultBalancePreauthorizationNonStreamOutputWindow-DefaultBalancePreauthorizationOutputWindow)*0.001, nonStreamGuard.HoldAmount()-streamGuard.HoldAmount(), 1e-6)
	require.InDelta(t, nonStreamGuard.HoldAmount(), explicitStreamGuard.HoldAmount(), 1e-6,
		"explicit max_tokens=4096 with stream hint=true must keep 4096-token hold")
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
		require.Equal(t, http.StatusTooManyRequests, rec.Code)
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
		require.Equal(t, http.StatusTooManyRequests, rec.Code)
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
		require.Equal(t, http.StatusTooManyRequests, rec.Code)
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
		require.Equal(t, http.StatusTooManyRequests, rec.Code)
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
		require.Equal(t, http.StatusTooManyRequests, rec.Code)
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
		require.Equal(t, http.StatusTooManyRequests, rec2.Code)
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
		require.Equal(t, http.StatusTooManyRequests, rec2.Code)
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
		require.Equal(t, http.StatusTooManyRequests, rec1.Code)
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
		require.Equal(t, http.StatusTooManyRequests, rec2.Code)
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
		require.True(t, IsBalanceHoldTopUpError(gotErr))
	}
	require.False(t, IsBalanceHoldTopUpError(nil))
	require.False(t, IsBalanceHoldTopUpError(errors.New("unrelated error")))
}

// ============================================================================
// Part 4 (P0-2): Provisional OutputTokens > 0 MUST NOT skip output estimation
// ============================================================================

func TestProvisionalOutputTokens_RaisedToObservedOutputOnBalanceAbort(t *testing.T) {
	t.Run("antigravity_forward_upstream_message_start_output_tokens_1", func(t *testing.T) {
		guard, fixture := newFailingStreamPreauthGuard(t)
		c, rec, ctx := newPreauthGinContext(t, "/antigravity/v1/messages", guard)

		sseBody := newPreauthClosingBody(buildAnthropicAbortSSE())
		upstream := &preauthStaticUpstream{
			fn: func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": {"text/event-stream"}},
					Body:       sseBody,
				}, nil
			},
		}
		svc := newAntigravityTestService(&config.Config{})
		svc.httpUpstream = upstream
		account := &Account{
			ID:       501,
			Name:     "ag-upstream",
			Platform: PlatformAntigravity,
			Type:     AccountTypeAPIKey,
			Credentials: map[string]any{
				"api_key":  "test-key-must-not-leak",
				"base_url": "https://api.example.invalid",
			},
		}
		reqBody := []byte(`{"model":"claude-sonnet-4-5","stream":true,"messages":[{"role":"user","content":"Explain preauth billing."}]}`)

		res, err := svc.ForwardUpstream(ctx, c, account, reqBody)
		require.ErrorIs(t, err, ErrBalanceWithholdingFailed)
		require.NotNil(t, res)
		require.Greater(t, fixture.wallet.topUpCalls, 0)
		require.True(t, sseBody.closed.Load())
		require.Equal(t, 42, res.Usage.InputTokens)
		require.Greater(t, res.Usage.OutputTokens, 100, "provisional message_start output_tokens=1 must be raised to delivered output")
		require.True(t, strings.HasSuffix(rec.Body.String(), "\n\n"), "aborted SSE stream must end on a clean blank-line terminator")
	})

	t.Run("gemini_native_streaming_partial_candidatesTokenCount_1", func(t *testing.T) {
		guard, fixture := newFailingStreamPreauthGuard(t)
		c, rec, ctx := newPreauthGinContext(t, "/v1beta/models/gemini-2.5-pro:streamGenerateContent", guard)

		chunk := longChunkPayload(12)
		stream := strings.Join([]string{
			fmt.Sprintf(`data: {"candidates":[{"content":{"role":"model","parts":[{"text":%q}]}}],"usageMetadata":{"promptTokenCount":55,"candidatesTokenCount":1,"totalTokenCount":56}}`, chunk),
			"",
			fmt.Sprintf(`data: {"candidates":[{"content":{"role":"model","parts":[{"text":%q}]}}]}`, chunk),
			"",
			fmt.Sprintf(`data: {"candidates":[{"content":{"role":"model","parts":[{"text":%q}]}}]}`, chunk),
			"",
			fmt.Sprintf(`data: {"candidates":[{"content":{"role":"model","parts":[{"text":%q}]}}]}`, chunk),
			"",
		}, "\n")
		sseBody := newPreauthClosingBody(stream)
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
			ID:       502,
			Platform: PlatformGemini,
			Type:     AccountTypeAPIKey,
			Credentials: map[string]any{
				"api_key":  "test-key-must-not-leak",
				"base_url": "https://generativelanguage.googleapis.com",
			},
		}
		reqBody := []byte(`{"contents":[{"role":"user","parts":[{"text":"Prompt for partial candidatesTokenCount test."}]}]}`)

		res, err := svc.ForwardNative(ctx, c, account, "gemini-2.5-pro", "streamGenerateContent", true, reqBody)
		require.ErrorIs(t, err, ErrBalanceWithholdingFailed)
		require.NotNil(t, res)
		require.Greater(t, fixture.wallet.topUpCalls, 0)
		require.Equal(t, 55, res.Usage.InputTokens)
		require.Greater(t, res.Usage.OutputTokens, 100, "partial candidatesTokenCount=1 must be raised to delivered output")
		require.True(t, strings.HasSuffix(rec.Body.String(), "\n\n"), "AI Studio pass-through SSE abort must end with blank-line terminator")
	})
}

// ============================================================================
// Part 5 (P0-3): Semantic Responses SSE counting — NO envelope over-counting
// or false aborts on .done / output_item.done / response.completed
// ============================================================================

func TestResponsesSSEObserver_NoEnvelopeOvercountOrFalseCompletedAbort(t *testing.T) {
	// Verify semantic byte extraction for newly added delta types and zero on all non-delta events.
	require.Equal(t, 12, responsesSSEDataOutputBytes("response.content_part.added", []byte(`{"type":"response.content_part.added","part":{"type":"output_text","text":"hello world!"}}`)))
	require.Equal(t, 7, responsesSSEDataOutputBytes("response.content_part.added", []byte(`{"type":"response.content_part.added","part":{"type":"refusal","refusal":"blocked"}}`)))
	require.Equal(t, 11, responsesSSEDataOutputBytes("response.code_interpreter_call_code.delta", []byte(`{"type":"response.code_interpreter_call_code.delta","delta":"print(1+1)\n"}`)))
	require.Equal(t, 9, responsesSSEDataOutputBytes("response.mcp_call_arguments.delta", []byte(`{"type":"response.mcp_call_arguments.delta","delta":"{\"a\":123}"}`)))
	require.Equal(t, len("get_weather")+len(`{"city":"Tokyo"}`), responsesSSEDataOutputBytes("response.output_item.added", []byte(`{"type":"response.output_item.added","item":{"type":"function_call","name":"get_weather","arguments":"{\"city\":\"Tokyo\"}"}}`)))
	require.Equal(t, len("hello inline")+len("nope"), responsesSSEDataOutputBytes("response.output_item.added", []byte(`{"type":"response.output_item.added","item":{"type":"message","content":[{"type":"output_text","text":"hello inline"},{"type":"refusal","refusal":"nope"}]}}`)))
	require.Equal(t, len("get_weather")+len(`{"city":"Tokyo"}`), responsesEventOutputBytes(&apicompat.ResponsesStreamEvent{
		Type: "response.output_item.added",
		Item: &apicompat.ResponsesOutput{Type: "function_call", Name: "get_weather", Arguments: `{"city":"Tokyo"}`},
	}))
	require.Equal(t, len("hello inline")+len("nope"), responsesEventOutputBytes(&apicompat.ResponsesStreamEvent{
		Type: "response.output_item.added",
		Item: &apicompat.ResponsesOutput{
			Type: "message",
			Content: []apicompat.ResponsesContentPart{
				{Type: "output_text", Text: "hello inline"},
				{Type: "refusal", Refusal: "nope"},
			},
		},
	}))

	bigText := strings.Repeat("word ", 400) // ~2000 bytes (~500 tokens)
	for _, nonDelta := range []struct {
		eventType string
		data      string
	}{
		{"response.output_text.done", fmt.Sprintf(`{"type":"response.output_text.done","text":%q}`, bigText)},
		{"response.content_part.done", fmt.Sprintf(`{"type":"response.content_part.done","part":{"type":"output_text","text":%q}}`, bigText)},
		{"response.output_item.done", fmt.Sprintf(`{"type":"response.output_item.done","item":{"id":"msg_1","type":"message","content":[{"type":"output_text","text":%q}]}}`, bigText)},
		{"response.completed", fmt.Sprintf(`{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":%q}]}],"usage":{"input_tokens":20,"output_tokens":500}}}`, bigText)},
		{"", "[DONE]"},
	} {
		require.Zero(t, responsesSSEDataOutputBytes(nonDelta.eventType, []byte(nonDelta.data)),
			"non-delta event %q must contribute 0 bytes", nonDelta.eventType)
	}

	// Build a realistic 500-token Responses SSE stream sent in small 4-byte deltas (500 frames)
	// followed by .done + content_part.done + output_item.done + response.completed.
	// With raw len(line) counting, 500 small JSON envelopes + 4 duplicate full-text terminal frames
	// would count >75,000 bytes (~18,750 tokens, 37x over-count!) and falsely abort on response.completed.
	// With semantic delta counting, total observed bytes == 2000 bytes (~500 tokens).
	buildStream := func() string {
		var sb strings.Builder
		sb.WriteString("event: response.created\n")
		sb.WriteString(`data: {"type":"response.created","response":{"id":"resp_500","status":"in_progress","model":"gpt-5.1"}}` + "\n\n")
		var full strings.Builder
		for i := 0; i < 500; i++ {
			delta := "abcd" // 4 bytes (~1 token), 500 * 4 = 2000 bytes (~500 tokens)
			full.WriteString(delta)
			sb.WriteString("event: response.output_text.delta\n")
			sb.WriteString(fmt.Sprintf(`data: {"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":%q,"sequence_number":%d}`+"\n\n", delta, i+1))
		}
		fullText := full.String()
		sb.WriteString("event: response.output_text.done\n")
		sb.WriteString(fmt.Sprintf(`data: {"type":"response.output_text.done","text":%q}`+"\n\n", fullText))
		sb.WriteString("event: response.content_part.done\n")
		sb.WriteString(fmt.Sprintf(`data: {"type":"response.content_part.done","part":{"type":"output_text","text":%q}}`+"\n\n", fullText))
		sb.WriteString("event: response.output_item.done\n")
		sb.WriteString(fmt.Sprintf(`data: {"type":"response.output_item.done","item":{"id":"msg_1","type":"message","content":[{"type":"output_text","text":%q}]}}`+"\n\n", fullText))
		sb.WriteString("event: response.completed\n")
		sb.WriteString(fmt.Sprintf(`data: {"type":"response.completed","response":{"id":"resp_500","status":"completed","model":"gpt-5.1","output":[{"type":"message","content":[{"type":"output_text","text":%q}]}],"usage":{"input_tokens":30,"output_tokens":500}}}`+"\n\n", fullText))
		return sb.String()
	}

	// Standard 256-token streaming guard whose wallet allows exactly 8 top-ups
	// (covering up to 2048 observed semantic bytes) and rejects the 9th top-up with
	// LiveBalanceOutcomeInsufficient. Our 500-token (2000-byte) stream requires exactly
	// 8 top-ups under semantic delta counting and ZERO extra bytes/top-ups on .done,
	// content_part.done, output_item.done, or response.completed. Raw len(line) counting
	// would tally >80,000 bytes (~40x over-count) and abort falsely well before completion
	// (and double-count again on every .done/completed frame).
	newGuardAllowing8TopUps := func(t *testing.T) (*BalancePreauthorizationGuard, *preauthorizationFixture) {
		t.Helper()
		f := newPreauthorizationFixture()
		f.calculator.outputUnitPrice = 0.001
		f.wallet.topUp = make([]LiveBalanceResult, 0, 9)
		for i := 0; i < 8; i++ {
			f.wallet.topUp = append(f.wallet.topUp, LiveBalanceResult{
				Outcome: LiveBalanceOutcomeApplied,
				State:   LiveBalanceAttemptAuthorized,
			})
		}
		f.wallet.topUp = append(f.wallet.topUp, LiveBalanceResult{
			Outcome: LiveBalanceOutcomeInsufficient,
			State:   LiveBalanceAttemptAuthorized,
		})
		req := balancePreauthorizationTestRequest()
		req.InitialOutputWindowTokens = DefaultBalancePreauthorizationOutputWindow
		g, err := f.service.Preauthorize(context.Background(), req)
		require.NoError(t, err)
		require.NotNil(t, g)
		return g, f
	}

	svc := &OpenAIGatewayService{cfg: &config.Config{}}
	account := &Account{ID: 503, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}

	t.Run("responses_handleStreamingResponse_no_false_abort", func(t *testing.T) {
		guard, fixture := newGuardAllowing8TopUps(t)
		c, _, _ := newPreauthGinContext(t, "/v1/responses", guard)
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(buildStream())),
		}
		// Pass background ctx (without guard) to also verify fallback to Gin request ctx after route rebinding!
		res, err := svc.handleStreamingResponse(context.Background(), resp, c, account, time.Now(), "gpt-5.1", "gpt-5.1")
		require.NoError(t, err, "500-token (2000-byte) stream must NOT over-count envelopes or abort falsely on response.completed")
		require.NotNil(t, res)
		require.Equal(t, 500, res.usage.OutputTokens)
		require.Equal(t, int64(2000), guard.core.outputHoldTracker.observedOutputBytes, "must observe exact 2000 semantic delta bytes (zero JSON envelopes, zero .done/completed)")
		require.Equal(t, 8, fixture.wallet.topUpCalls, "2000 semantic bytes across 256-token windows must take exactly 8 top-ups")
	})

	t.Run("responses_handleStreamingResponsePassthrough_no_false_abort", func(t *testing.T) {
		guard, fixture := newGuardAllowing8TopUps(t)
		c, _, _ := newPreauthGinContext(t, "/v1/responses", guard)
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(buildStream())),
		}
		res, err := svc.handleStreamingResponsePassthrough(context.Background(), resp, c, account, time.Now(), "gpt-5.1", "gpt-5.1")
		require.NoError(t, err, "500-token (2000-byte) passthrough stream must NOT over-count envelopes or abort falsely on response.completed")
		require.NotNil(t, res)
		require.Equal(t, 500, res.usage.OutputTokens)
		require.Equal(t, int64(2000), guard.core.outputHoldTracker.observedOutputBytes, "must observe exact 2000 semantic delta bytes (zero JSON envelopes, zero .done/completed)")
		require.Equal(t, 8, fixture.wallet.topUpCalls, "2000 semantic bytes across 256-token windows must take exactly 8 top-ups")
	})
}

// ============================================================================
// Part 6 (P1-1 / P1-3 / P1-4 / P1-5 / P2): Snake-case Gemini->OpenAI, Copilot,
// SSE terminators, MarkResponseCommitted, Affinity, NonBillable & CodeExec
// ============================================================================

func TestGeminiNativeRequestToChatCompletions_HonorsSnakeCaseMaxOutputTokens(t *testing.T) {
	body := []byte(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}],"generation_config":{"max_output_tokens":321}}`)
	converted, err := geminiNativeRequestToChatCompletions("gpt-5.1", body, false)
	require.NoError(t, err)
	require.Equal(t, int64(321), gjson.GetBytes(converted, "max_tokens").Int())

	// camelCase still takes precedence when both are present.
	both := []byte(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}],"generationConfig":{"maxOutputTokens":654},"generation_config":{"max_output_tokens":321}}`)
	convertedBoth, err := geminiNativeRequestToChatCompletions("gpt-5.1", both, false)
	require.NoError(t, err)
	require.Equal(t, int64(654), gjson.GetBytes(convertedBoth, "max_tokens").Int())
}

func TestBalanceHoldAbort_SSEEventTerminators_MarkResponseCommitted_AndAffinityPreserved(t *testing.T) {
	t.Run("writeAnthropicError_marks_response_committed", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		writeAnthropicError(c, http.StatusTooManyRequests, "insufficient_quota", ErrBalanceWithholdingFailed.Message)
		require.True(t, IsResponseCommitted(c))
		require.Equal(t, http.StatusTooManyRequests, rec.Code)
	})

	t.Run("sse_terminators_on_balance_abort", func(t *testing.T) {
		// Anthropic passthrough
		guard1, _ := newFailingStreamPreauthGuard(t)
		c1, rec1, ctx1 := newPreauthGinContext(t, "/v1/messages", guard1)
		svc := &GatewayService{cfg: &config.Config{}}
		resp1 := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: newPreauthClosingBody(buildAnthropicAbortSSE())}
		_, err1 := svc.handleStreamingResponseAnthropicAPIKeyPassthrough(ctx1, resp1, c1, &Account{ID: 601, Platform: PlatformAnthropic, Type: AccountTypeAPIKey}, time.Now(), "claude-sonnet-4-5")
		require.ErrorIs(t, err1, ErrBalanceWithholdingFailed)
		require.True(t, strings.HasSuffix(rec1.Body.String(), "\n\n"), "anthropic passthrough must terminate open SSE event with \\n\\n")

		// Raw chat completions
		guard2, _ := newFailingStreamPreauthGuard(t)
		c2, rec2, _ := newPreauthGinContext(t, "/v1/chat/completions", guard2)
		oaiSvc := &OpenAIGatewayService{cfg: &config.Config{}}
		resp2 := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: newPreauthClosingBody(buildOpenAIChatAbortSSE("content"))}
		_, err2 := oaiSvc.streamRawChatCompletions(c2, resp2, &Account{ID: 602, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, "gpt-5.1", "gpt-5.1", "gpt-5.1", nil, nil, time.Now(), 100)
		require.ErrorIs(t, err2, ErrBalanceWithholdingFailed)
		require.True(t, strings.HasSuffix(rec2.Body.String(), "\n\n"), "raw chat completions must terminate open SSE event with \\n\\n")

		// Native Anthropic messages on OpenAI gateway
		guard3, _ := newFailingStreamPreauthGuard(t)
		c3, rec3, ctx3 := newPreauthGinContext(t, "/v1/messages", guard3)
		resp3 := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: newPreauthClosingBody(buildAnthropicAbortSSE())}
		_, err3 := oaiSvc.handleNativeAnthropicStreamingResponse(ctx3, resp3, c3, &Account{ID: 603, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, "claude-sonnet-4-5", "claude-sonnet-4-5", "claude-sonnet-4-5", nil, time.Now())
		require.ErrorIs(t, err3, ErrBalanceWithholdingFailed)
		require.True(t, strings.HasSuffix(rec3.Body.String(), "\n\n"), "native anthropic messages must terminate open SSE event with \\n\\n")
	})

	t.Run("invalidate_upstream_affinity_ignores_balance_hold_topup_errors", func(t *testing.T) {
		ctx := withUpstreamAttempts(context.Background())
		account := &Account{ID: 604}
		invalidateUpstreamAffinity(ctx, nil, nil, []string{"sess-1"}, account, wrapStreamOutputHoldTopUpFailure(ErrBalanceWithholdingFailed))
		state, ok := ctx.Value(upstreamAttemptContextKey{}).(*upstreamAttempts)
		require.True(t, ok)
		require.Empty(t, state.failed, "client balance exhaustion must never mark a healthy upstream account as failed")
	})
}

func TestBalanceAbort_ClearsNonBillableAndObservesGeminiCodeExecution(t *testing.T) {
	t.Run("scanCCStream_clears_non_billable_after_prelude_error_and_output_abort", func(t *testing.T) {
		guard, fixture := newFailingStreamPreauthGuard(t)
		c, _, _ := newPreauthGinContext(t, "/v1/responses", guard)
		chunk := longChunkPayload(12)
		stream := strings.Join([]string{
			`data: {"error":{"type":"server_error","message":"transient prelude"}}`,
			"",
			fmt.Sprintf(`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","model":"gpt-5.1","choices":[{"index":0,"delta":{"content":%q}}]}`, chunk),
			"",
			fmt.Sprintf(`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","model":"gpt-5.1","choices":[{"index":0,"delta":{"content":%q}}]}`, chunk),
			"",
			fmt.Sprintf(`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","model":"gpt-5.1","choices":[{"index":0,"delta":{"content":%q}}]}`, chunk),
			"",
			fmt.Sprintf(`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","model":"gpt-5.1","choices":[{"index":0,"delta":{"content":%q}}]}`, chunk),
			"",
		}, "\n")
		body := newPreauthClosingBody(stream)
		resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: body}
		svc := &OpenAIGatewayService{cfg: &config.Config{}}
		stashed := []byte(`{"model":"gpt-5.1","input":"hello"}`)
		stashCompatUpstreamRequestBody(c, nil, stashed)

		res, err := svc.streamChatCompletionsAsResponses(c, resp, "gpt-5.1", nil, nil, false, nil, nil, "gpt-5.1", "gpt-5.1", nil, nil, time.Now())
		require.ErrorIs(t, err, ErrBalanceWithholdingFailed)
		require.NotNil(t, res)
		require.False(t, res.NonBillableUpstreamError, "delivered output on balance abort must never be waived as non-billable")
		require.Greater(t, fixture.wallet.topUpCalls, 0)
		require.Greater(t, res.Usage.OutputTokens, 100)
	})

	t.Run("gemini_code_execution_parts_observed_in_messages_and_chat_compat", func(t *testing.T) {
		chunk := longChunkPayload(12)
		codeSSE := strings.Join([]string{
			fmt.Sprintf(`data: {"candidates":[{"content":{"role":"model","parts":[{"executableCode":{"language":"PYTHON","code":%q}}]}}]}`, chunk),
			"",
			fmt.Sprintf(`data: {"candidates":[{"content":{"role":"model","parts":[{"codeExecutionResult":{"outcome":"OUTCOME_OK","output":%q}}]}}]}`, chunk),
			"",
			fmt.Sprintf(`data: {"candidates":[{"content":{"role":"model","parts":[{"executableCode":{"language":"PYTHON","code":%q}}]}}]}`, chunk),
			"",
			fmt.Sprintf(`data: {"candidates":[{"content":{"role":"model","parts":[{"codeExecutionResult":{"outcome":"OUTCOME_OK","output":%q}}]}}]}`, chunk),
			"",
		}, "\n")
		account := &Account{
			ID:       605,
			Platform: PlatformGemini,
			Type:     AccountTypeAPIKey,
			Credentials: map[string]any{
				"api_key":  "test-key-must-not-leak",
				"base_url": "https://generativelanguage.googleapis.com",
			},
		}

		// Messages compat
		guard1, fixture1 := newFailingStreamPreauthGuard(t)
		c1, _, ctx1 := newPreauthGinContext(t, "/v1/messages", guard1)
		svc1 := &GeminiMessagesCompatService{
			httpUpstream: &preauthStaticUpstream{fn: func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: newPreauthClosingBody(codeSSE)}, nil
			}},
			cfg: &config.Config{},
		}
		res1, err1 := svc1.Forward(ctx1, c1, account, []byte(`{"model":"gemini-2.5-pro","stream":true,"messages":[{"role":"user","content":"Run python."}]}`))
		require.ErrorIs(t, err1, ErrBalanceWithholdingFailed)
		require.NotNil(t, res1)
		require.Greater(t, fixture1.wallet.topUpCalls, 0)
		require.Greater(t, res1.Usage.OutputTokens, 100)

		// Chat completions compat
		guard2, fixture2 := newFailingStreamPreauthGuard(t)
		c2, _, ctx2 := newPreauthGinContext(t, "/v1/chat/completions", guard2)
		svc2 := &GeminiMessagesCompatService{
			httpUpstream: &preauthStaticUpstream{fn: func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: newPreauthClosingBody(codeSSE)}, nil
			}},
			cfg: &config.Config{},
		}
		res2, err2 := svc2.ForwardAsChatCompletions(ctx2, c2, account, []byte(`{"model":"gemini-2.5-pro","stream":true,"messages":[{"role":"user","content":"Run python."}]}`))
		require.ErrorIs(t, err2, ErrBalanceWithholdingFailed)
		require.NotNil(t, res2)
		require.Greater(t, fixture2.wallet.topUpCalls, 0)
		require.Greater(t, res2.Usage.OutputTokens, 100)
	})
}

func TestIncompleteUsage_PreservesFinalOutputWhileRaisingProvisional(t *testing.T) {
	reqBody := []byte(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`)
	oaiBody := []byte(`{"model":"gpt-5.1","input":"hello"}`)

	t.Run("gemini_preserves_authoritative_final_usage_and_raises_provisional", func(t *testing.T) {
		finalUsage := &ClaudeUsage{InputTokens: 40, OutputTokens: 12}
		filledOut, filledIn := fillIncompleteGeminiUsage(finalUsage, 180, reqBody, true)
		require.Zero(t, filledOut, "authoritative final output must never be overwritten by meter estimate")
		require.Zero(t, filledIn)
		require.Equal(t, 12, finalUsage.OutputTokens)

		provisionalUsage := &ClaudeUsage{InputTokens: 40, OutputTokens: 1}
		filledOut, _ = fillIncompleteGeminiUsage(provisionalUsage, 180, reqBody, false)
		require.Equal(t, 180, filledOut, "provisional output_tokens=1 must be raised to delivered meter estimate")
		require.Equal(t, 180, provisionalUsage.OutputTokens)
	})

	t.Run("openai_stream_preserves_authoritative_final_usage_and_raises_provisional", func(t *testing.T) {
		finalUsage := &OpenAIUsage{InputTokens: 40, OutputTokens: 12}
		filledOut, filledIn := fillIncompleteStreamUsageWithFinal(finalUsage, 180, oaiBody, true)
		require.Zero(t, filledOut, "authoritative final output must never be overwritten by estimate")
		require.Zero(t, filledIn)
		require.Equal(t, 12, finalUsage.OutputTokens)

		provisionalUsage := &OpenAIUsage{InputTokens: 40, OutputTokens: 1}
		filledOut, _ = fillIncompleteStreamUsageWithFinal(provisionalUsage, 180, oaiBody, false)
		require.Equal(t, 180, filledOut)
		require.Equal(t, 180, provisionalUsage.OutputTokens)
	})

	t.Run("messages_chat_fallback_raises_provisional_on_balance_abort_and_preserves_final_after_finish_reason", func(t *testing.T) {
		// Provisional completion_tokens=1 before finish_reason is raised on balance abort.
		guard, _ := newFailingStreamPreauthGuard(t)
		c, _, _ := newPreauthGinContext(t, "/v1/messages", guard)
		chunk := longChunkPayload(12)
		provisionalSSE := strings.Join([]string{
			fmt.Sprintf(`data: {"id":"chatcmpl_p","object":"chat.completion.chunk","model":"gpt-5.1","choices":[{"index":0,"delta":{"content":%q}}],"usage":{"prompt_tokens":30,"completion_tokens":1}}`, chunk),
			"",
			fmt.Sprintf(`data: {"id":"chatcmpl_p","object":"chat.completion.chunk","model":"gpt-5.1","choices":[{"index":0,"delta":{"content":%q}}]}`, chunk),
			"",
			fmt.Sprintf(`data: {"id":"chatcmpl_p","object":"chat.completion.chunk","model":"gpt-5.1","choices":[{"index":0,"delta":{"content":%q}}]}`, chunk),
			"",
		}, "\n")
		resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: newPreauthClosingBody(provisionalSSE)}
		svc := &OpenAIGatewayService{cfg: &config.Config{}}
		stashCompatUpstreamRequestBody(c, nil, []byte(`{"model":"gpt-5.1","messages":[{"role":"user","content":"hi"}]}`))

		res, err := svc.streamChatCompletionsAsAnthropic(c, resp, "gpt-5.1", "gpt-5.1", "gpt-5.1", nil, nil, time.Now())
		require.ErrorIs(t, err, ErrBalanceWithholdingFailed)
		require.NotNil(t, res)
		require.Equal(t, 30, res.Usage.InputTokens)
		require.Greater(t, res.Usage.OutputTokens, 100, "provisional completion_tokens=1 before finish_reason must be raised to delivered output")

		// Authoritative final completion_tokens=12 on/after finish_reason is preserved even if stream ends before [DONE].
		c2, _, _ := newPreauthGinContext(t, "/v1/messages", nil)
		finalSSE := strings.Join([]string{
			fmt.Sprintf(`data: {"id":"chatcmpl_f","object":"chat.completion.chunk","model":"gpt-5.1","choices":[{"index":0,"delta":{"content":%q}}]}`, chunk),
			"",
			`data: {"id":"chatcmpl_f","object":"chat.completion.chunk","model":"gpt-5.1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":30,"completion_tokens":12}}`,
			"",
		}, "\n")
		scan := svc.scanCCStream(c2, &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(finalSSE))}, "test", "req-f", time.Now(), func(*apicompat.ChatCompletionsChunk) {})
		require.True(t, scan.SawFinalOutput)
		require.Equal(t, 12, scan.Usage.OutputTokens)
	})

	t.Run("openai_responses_stream_balance_abort_clears_earlier_bare_error_non_billable", func(t *testing.T) {
		guard, fixture := newFailingStreamPreauthGuard(t)
		c, _, ctx := newPreauthGinContext(t, "/v1/responses", guard)
		chunk := longChunkPayload(12)
		sse := strings.Join([]string{
			"event: error",
			`data: {"type":"error","error":{"type":"server_error","message":"transient prelude"}}`,
			"",
			"event: response.created",
			`data: {"type":"response.created","response":{"id":"resp_bare","status":"in_progress","model":"gpt-5.1"}}`,
			"",
			"event: response.output_text.delta",
			fmt.Sprintf(`data: {"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":%q}`, chunk),
			"",
			"event: response.output_text.delta",
			fmt.Sprintf(`data: {"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":%q}`, chunk),
			"",
			"event: response.output_text.delta",
			fmt.Sprintf(`data: {"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":%q}`, chunk),
			"",
		}, "\n")
		svc := &OpenAIGatewayService{cfg: &config.Config{}}
		account := &Account{ID: 612, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
		resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: newPreauthClosingBody(sse)}

		res, err := svc.handleStreamingResponse(ctx, resp, c, account, time.Now(), "gpt-5.1", "gpt-5.1")
		require.ErrorIs(t, err, ErrBalanceWithholdingFailed)
		require.NotNil(t, res)
		require.Greater(t, fixture.wallet.topUpCalls, 0)
		require.False(t, res.nonBillableUpstreamError, "delivered output on balance abort must clear earlier bare error waiver")
		require.True(t, res.incompleteStream, "balance abort must mark stream incomplete so partial output is estimated")
		require.Greater(t, res.incompleteOutputTokens, 100)
	})
}
