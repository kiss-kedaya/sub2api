package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/util/responseheaders"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

// forwardAnthropicViaRawChatCompletions serves /v1/messages clients through
// an OpenAI-compatible upstream that only supports /v1/chat/completions.
//
// Conversion chain (direct, no Responses intermediary):
//
//	Request:  Anthropic Messages → Chat Completions (AnthropicToChatCompletionsRequest)
//	Response: CC chunk/response → Anthropic events/response (direct bridge)
//
// This is the /v1/messages counterpart of forwardResponsesViaRawChatCompletions
// (which serves /v1/responses clients). Unlike the Responses path, the direct
// bridge skips the Responses API intermediate representation entirely — every
// streaming token runs through a single state machine instead of two.
func (s *OpenAIGatewayService) forwardAnthropicViaRawChatCompletions(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	defaultMappedModel string,
) (*OpenAIForwardResult, error) {
	startTime := time.Now()

	// 1. Parse Anthropic request
	var anthropicReq apicompat.AnthropicRequest
	if err := json.Unmarshal(body, &anthropicReq); err != nil {
		writeAnthropicError(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return nil, fmt.Errorf("parse anthropic request: %w", err)
	}
	originalModel := anthropicReq.Model
	if strings.TrimSpace(originalModel) == "" {
		writeAnthropicError(c, http.StatusBadRequest, "invalid_request_error", "model is required")
		return nil, fmt.Errorf("missing model in request")
	}
	applyOpenAICompatModelNormalization(&anthropicReq)
	clientStream := anthropicReq.Stream

	// A Sol/Terra tool request needs the Responses API, so a Chat-Completions-only
	// account cannot serve it. Resolve the upstream model and detect the mismatch
	// before the lossy Anthropic→Chat conversion: a managed gateway returns a
	// request-scoped failover error so another account can answer, while a
	// direct/single-account caller (no selector) keeps the committed 400.
	billingModel := resolveOpenAIForwardModel(account, anthropicReq.Model, defaultMappedModel)
	upstreamModel := normalizeOpenAIModelForUpstream(account, billingModel)
	if err := validateGPT61SolCompatRequest(body, upstreamModel); err != nil {
		writeAnthropicError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return nil, err
	}
	if openai.RequiresResponsesForToolCalls(upstreamModel) && len(anthropicReq.Tools) > 0 {
		if s.accountRepo != nil {
			return nil, newOpenAIResponsesToolsUnsupportedFailoverError(upstreamModel)
		}
		err := fmt.Errorf("%s", openAIResponsesToolsUnsupportedMessage(upstreamModel))
		writeAnthropicError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return nil, err
	}

	// 2. Anthropic → Chat Completions (direct, no Responses intermediary)
	// DeepSeek's thinking-mode Chat API requires every echoed assistant
	// thinking block to be sent back as reasoning_content, including a plain
	// assistant turn without a tool call. Other compatible providers reject
	// this vendor field, so keep the generic bridge default elsewhere.
	chatReq, err := apicompat.AnthropicToChatCompletionsRequestWithOptions(&anthropicReq, &apicompat.AnthropicToChatCompletionsOptions{
		PreserveThinking: account != nil && account.Platform == PlatformDeepseek,
	})
	if err != nil {
		writeAnthropicError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return nil, fmt.Errorf("convert anthropic to chat completions: %w", err)
	}

	chatReq.Model = upstreamModel
	chatReq.ReasoningEffort = openAICompatAnthropicReasoningEffort(&anthropicReq, upstreamModel, chatReq.ReasoningEffort)
	if account.Platform == PlatformDeepseek && anthropicReq.Thinking != nil {
		chatReq.Thinking = &apicompat.AnthropicThinking{Type: anthropicReq.Thinking.Type}
		if anthropicReq.Thinking.Type == "disabled" {
			chatReq.ReasoningEffort = ""
		}
	}
	chatReq.Stream = clientStream
	if clientStream {
		chatReq.StreamOptions = &apicompat.ChatStreamOptions{IncludeUsage: true}
	}

	convertedEffort := chatReq.ReasoningEffort
	reasoningEffort := &convertedEffort
	reasoningEffort = ApplyThinkingEnabledFallback(reasoningEffort, body, billingModel)
	serviceTier := extractOpenAIServiceTierFromBody(c, body)

	chatBody, err := json.Marshal(chatReq)
	if err != nil {
		return nil, fmt.Errorf("marshal chat completions request: %w", err)
	}
	if normalizedBody, normalized := NormalizeGLMOpenAIReasoningEffort(chatBody, upstreamModel); normalized {
		chatBody = normalizedBody
	}
	if account.Platform == PlatformOpenAI {
		policyBody, changed, policyErr := ApplyOpenAIReasoningEffortPolicyFromContext(ctx, chatBody)
		if policyErr != nil {
			if IsReasoningEffortPolicyDenied(policyErr) {
				MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
				writeAnthropicError(c, http.StatusForbidden, "forbidden_error", policyErr.Error())
			}
			return nil, policyErr
		}
		if changed {
			chatBody = policyBody
			if effectiveEffort := strings.TrimSpace(gjson.GetBytes(chatBody, "reasoning_effort").String()); effectiveEffort != "" {
				reasoningEffort = &effectiveEffort
			}
		}
	}
	// Unlike forwardResponsesViaRawChatCompletions, applyOpenAIFastPolicyToBody
	// is intentionally skipped: Anthropic Messages bodies carry no service_tier,
	// so the converted Chat Completions body never contains one and the policy
	// would always be a no-op on this path.

	logger.L().Debug("openai messages: forwarding via raw chat completions",
		zap.Int64("account_id", account.ID),
		zap.String("original_model", originalModel),
		zap.String("billing_model", billingModel),
		zap.String("upstream_model", upstreamModel),
		zap.Bool("stream", clientStream),
	)

	// 3. Build and send upstream request via the shared CC pipeline
	apiKey, targetURL, err := s.resolveCCFallbackTarget(account)
	if err != nil {
		return nil, err
	}
	clientCtx := openAIClientContext(ctx, c)
	resp, err := s.sendCCUpstreamRequest(ctx, c, account, targetURL, chatBody, clientStream, apiKey, account.GetOpenAIUserAgent(), "")
	if err != nil {
		if clientErr := openAIClientErr(clientCtx); clientErr != nil {
			if errors.Is(err, errOpenAIRequestNotSent) {
				return nil, clientErr
			}
			return newClientGoneOpenAIForwardResult(
				c, account, chatBody, originalModel, billingModel, upstreamModel, reasoningEffort, clientStream, startTime,
			), clientErr
		}
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	// 4. Handle error responses
	if resp.StatusCode >= 400 {
		if clientErr := openAIClientErr(clientCtx); clientErr != nil {
			return nil, clientErr
		}
		respBody, upstreamMsg, readErr := s.readOpenAIUpstreamErrorWithError(resp)
		if errors.Is(readErr, errOpenAIUpstreamWaitLimit) {
			return s.openAIStreamSilenceHTTPError(c, resp, respBody, readErr, originalModel, billingModel, upstreamModel, clientStream, startTime)
		}
		if clientErr := openAIClientErr(clientCtx); clientErr != nil {
			return nil, clientErr
		}
		if foErr := s.failoverOpenAIUpstreamHTTPError(ctx, c, account, resp, respBody, upstreamMsg, upstreamModel); foErr != nil {
			return nil, foErr
		}
		// Non-failover error: return Anthropic-formatted error to client via the
		// shared compat handler (passthrough rules, ops recording, cyber_policy).
		return s.handleAnthropicErrorResponse(resp, c, account, billingModel)
	}

	// 5. Convert response
	// Keep the body that went upstream: a request that ends without a usage report is
	// billed for the input it really sent (see applyCompatIncompleteUsage).
	stashCompatUpstreamRequestBody(c, account, chatBody)
	if clientStream {
		return s.streamChatCompletionsAsAnthropic(c, resp, originalModel, billingModel, upstreamModel, reasoningEffort, serviceTier, startTime)
	}
	return s.bufferChatCompletionsAsAnthropic(c, resp, originalModel, billingModel, upstreamModel, reasoningEffort, serviceTier, startTime)
}

func (s *OpenAIGatewayService) bufferChatCompletionsAsAnthropic(
	c *gin.Context,
	resp *http.Response,
	originalModel string,
	billingModel string,
	upstreamModel string,
	reasoningEffort *string,
	serviceTier *string,
	startTime time.Time,
) (*OpenAIForwardResult, error) {
	requestID := resp.Header.Get("x-request-id")
	// The client leaving closes the upstream at once, which fails the read below. The
	// request was dispatched and accepted, so its input is still billed from an estimate.
	watch := newCompatClientWatch(c, resp.Body)
	defer watch.Stop()
	ccResp, usage, nonBillableUpstreamError, err := s.readCCUpstreamJSONResponse(c, watch.wrapResponse(resp), func(c *gin.Context, status int, errType, message string) {
		if !watch.Gone() {
			writeAnthropicError(c, status, errType, message)
		}
	})
	if err != nil && ccResp == nil {
		if errors.Is(err, errOpenAIUpstreamWaitLimit) {
			return openAIStreamSilenceResult(resp, usage, originalModel, billingModel, upstreamModel, false, startTime), err
		}
		if watch.Gone() && errors.Is(err, context.Canceled) {
			base := &OpenAIForwardResult{
				RequestID:                   requestID,
				UpstreamHeaders:             resp.Header,
				Model:                       originalModel,
				BillingModel:                billingModel,
				UpstreamModel:               upstreamModel,
				ReasoningEffort:             reasoningEffort,
				UpstreamResponseServiceTier: observedUpstreamResponseServiceTier(c),
				ServiceTier:                 resolvedOpenAIUpstreamServiceTier(c, serviceTier),
				Stream:                      false,
				Duration:                    time.Since(startTime),
			}
			if result := compatBufferedGoneResult(c, nil, "messages_chat_buffered", base, OpenAIUsage{}, nil, err); result != nil {
				return result, nil
			}
		}
		return nil, err
	}
	if err == nil {
		anthropicResp := apicompat.ChatCompletionsResponseToAnthropic(ccResp, originalModel)

		if s.responseHeaderFilter != nil {
			responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
		}
		c.JSON(http.StatusOK, anthropicResp)
	}

	return &OpenAIForwardResult{
		RequestID:                   requestID,
		UpstreamHeaders:             resp.Header,
		Usage:                       usage,
		NonBillableUpstreamError:    nonBillableUpstreamError,
		Model:                       originalModel,
		BillingModel:                billingModel,
		UpstreamModel:               upstreamModel,
		ReasoningEffort:             reasoningEffort,
		UpstreamResponseServiceTier: observedUpstreamResponseServiceTier(c),
		ServiceTier:                 resolvedOpenAIUpstreamServiceTier(c, serviceTier),
		Stream:                      false,
		Duration:                    time.Since(startTime),
	}, err
}

func (s *OpenAIGatewayService) streamChatCompletionsAsAnthropic(
	c *gin.Context,
	resp *http.Response,
	originalModel string,
	billingModel string,
	upstreamModel string,
	reasoningEffort *string,
	serviceTier *string,
	startTime time.Time,
) (*OpenAIForwardResult, error) {
	requestID := resp.Header.Get("x-request-id")
	writeStreamHeaders := s.newStreamHeaderWriter(c, resp.Header)

	anthropicState := apicompat.NewChatCompletionsToAnthropicStreamState(originalModel)
	clientDisconnected := false

	// The client leaving (its request ends, or a write to it fails) closes the upstream
	// at once: the next read fails and the scan ends. Chunks already buffered are still
	// converted so a usage chunk that had arrived keeps its real numbers; otherwise the
	// output tallied so far and the input sent are billed from an estimate.
	watch := newCompatClientWatch(c, resp.Body)
	defer watch.Stop()
	var tally compatChatTally

	emitChunk := func(chunk *apicompat.ChatCompletionsChunk) {
		tally.AddChunk(chunk)
		// CC chunk → Anthropic events (direct, single state machine)
		anthropicEvents := apicompat.ChatCompletionsChunkToAnthropicEvents(chunk, anthropicState)
		if clientDisconnected {
			return
		}
		for _, aEvt := range anthropicEvents {
			sse, err := apicompat.ResponsesAnthropicEventToSSE(aEvt)
			if err != nil {
				continue
			}
			writeStreamHeaders()
			if _, err := fmt.Fprint(c.Writer, sse); err != nil {
				clientDisconnected = true
				watch.Disconnect()
				break
			}
		}
		if !clientDisconnected && len(anthropicEvents) > 0 {
			c.Writer.Flush()
		}
	}

	scan := s.scanCCStream(c, watch.wrapResponse(resp), "openai messages chat fallback", requestID, startTime, emitChunk)
	usage := scan.Usage

	var upstreamErr error
	// A scan ended by the silence limit has already told the client and carries its
	// own error; writing the upstream-error frame as well would send two errors.
	if scan.NonBillableUpstreamError && !errors.Is(scan.Err, errOpenAIUpstreamWaitLimit) {
		upstreamErr = writeCompatUpstreamStreamError(c, "messages", originalModel, scan.UpstreamError)
	}
	if gone := clientDisconnected || watch.Gone(); upstreamErr != nil || scan.Err != nil || (gone && !scan.SawDone) {
		result := &OpenAIForwardResult{
			RequestID:                   requestID,
			UpstreamHeaders:             resp.Header,
			Usage:                       usage,
			NonBillableUpstreamError:    scan.NonBillableUpstreamError,
			Model:                       originalModel,
			BillingModel:                billingModel,
			UpstreamModel:               upstreamModel,
			ReasoningEffort:             reasoningEffort,
			UpstreamResponseServiceTier: observedUpstreamResponseServiceTier(c),
			ServiceTier:                 resolvedOpenAIUpstreamServiceTier(c, serviceTier),
			Stream:                      true,
			Duration:                    time.Since(startTime),
			FirstTokenMs:                scan.FirstTokenMs,
			ClientDisconnect:            gone,
		}
		if upstreamErr != nil {
			return result, upstreamErr
		}
		cause := scan.Err
		if cause == nil {
			cause = errors.New("missing done sentinel")
		}
		applyCompatIncompleteUsage(c, &result.Usage, compatIncompleteUsage{
			Path: "messages_chat_stream", Model: originalModel, RequestID: requestID,
			ClientGone: gone, Cause: cause, OutputEstimate: tally.EstimateTokens(),
		})
		if gone {
			return result, nil
		}
		return result, fmt.Errorf("stream usage incomplete: %w", cause)
	}

	// Finalize: close open blocks + emit message_delta/message_stop.
	finalEvents := apicompat.FinalizeChatCompletionsAnthropicStream(anthropicState)
	if !clientDisconnected {
		for _, aEvt := range finalEvents {
			sse, err := apicompat.ResponsesAnthropicEventToSSE(aEvt)
			if err != nil {
				continue
			}
			writeStreamHeaders()
			if _, err := fmt.Fprint(c.Writer, sse); err != nil {
				clientDisconnected = true
				watch.Disconnect()
				break
			}
		}
		if !clientDisconnected {
			c.Writer.Flush()
		}
	}
	if !scan.SawDone {
		logCCStreamMissingDoneSentinel("openai messages chat fallback", requestID)
	}

	return &OpenAIForwardResult{
		RequestID:                   requestID,
		UpstreamHeaders:             resp.Header,
		Usage:                       usage,
		NonBillableUpstreamError:    scan.NonBillableUpstreamError,
		Model:                       originalModel,
		BillingModel:                billingModel,
		UpstreamModel:               upstreamModel,
		ReasoningEffort:             reasoningEffort,
		UpstreamResponseServiceTier: observedUpstreamResponseServiceTier(c),
		ServiceTier:                 resolvedOpenAIUpstreamServiceTier(c, serviceTier),
		Stream:                      true,
		Duration:                    time.Since(startTime),
		FirstTokenMs:                scan.FirstTokenMs,
		ClientDisconnect:            clientDisconnected || watch.Gone(),
	}, nil
}
