package service

// 国产供应商 Anthropic 协议账号的 CC 入站反向路径。
//
// 客户端说 OpenAI Chat Completions、上游是供应商原生 Anthropic 端点
// （api_protocol=anthropic）时的交叉组合：请求 CC→Responses→Anthropic 转换，
// 响应 Anthropic→Responses→CC 转换。转换链与 Anthropic 平台的
// gateway_forward_as_chat_completions.go 完全一致（复用同一组 apicompat
// 状态机），仅上游发送/错误处理对齐 OpenAI 网关语义。

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/util/responseheaders"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// forwardChatCompletionsViaNativeAnthropic serves OpenAI /v1/chat/completions
// clients through a CN provider's native Anthropic endpoint.
//
// Conversion chain:
//
//	Request:  Chat Completions → Responses → Anthropic (chained)
//	Response: Anthropic events → Responses events → CC chunks (chained state machines)
func (s *OpenAIGatewayService) forwardChatCompletionsViaNativeAnthropic(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	defaultMappedModel string,
) (*OpenAIForwardResult, error) {
	startTime := time.Now()

	// 1. Parse Chat Completions request
	var ccReq apicompat.ChatCompletionsRequest
	if err := json.Unmarshal(body, &ccReq); err != nil {
		writeChatCompletionsError(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return nil, fmt.Errorf("parse chat completions request: %w", err)
	}
	originalModel := ccReq.Model
	if strings.TrimSpace(originalModel) == "" {
		writeChatCompletionsError(c, http.StatusBadRequest, "invalid_request_error", "model is required")
		return nil, fmt.Errorf("missing model in request")
	}
	clientStream := ccReq.Stream
	includeUsage := ccReq.StreamOptions != nil && ccReq.StreamOptions.IncludeUsage

	// 2. Convert CC → Responses → Anthropic (chained conversion)
	anthropicReq, err := apicompat.ChatCompletionsToAnthropicRequestWithOptions(&ccReq, &apicompat.ChatCompletionsToResponsesOptions{
		PreserveReasoningContent: account.Platform == PlatformDeepseek,
	})
	if err != nil {
		writeChatCompletionsError(c, http.StatusBadRequest, "invalid_request_error", "Failed to convert request")
		return nil, fmt.Errorf("convert chat completions to anthropic: %w", err)
	}

	// 3. Model mapping（OpenAI 网关统一入口的映射语义）
	billingModel := resolveOpenAIForwardModel(account, originalModel, defaultMappedModel)
	upstreamModel := normalizeOpenAIModelForUpstream(account, billingModel)
	anthropicReq.Model = upstreamModel

	// 4. Force upstream streaming（客户端原始终决定响应格式；
	// 上游恒为流式，非流式由缓冲路径组装）。
	anthropicReq.Stream = true
	reqStream := true

	logger.L().Debug("openai chat_completions: forwarding via native anthropic endpoint",
		zap.Int64("account_id", account.ID),
		zap.String("original_model", originalModel),
		zap.String("billing_model", billingModel),
		zap.String("upstream_model", upstreamModel),
		zap.Bool("client_stream", clientStream),
	)

	anthropicBody, err := json.Marshal(anthropicReq)
	if err != nil {
		return nil, fmt.Errorf("marshal anthropic request: %w", err)
	}

	// 与 /v1/messages 直通路径相同的 pre-filter。
	anthropicBody = StripEmptyTextBlocks(anthropicBody)
	anthropicBody = FilterWebSearchHistoryBlocks(anthropicBody, upstreamModel)
	// Claude 5.x 只认 adaptive thinking；CC→Responses→Anthropic 的合成会把
	// reasoning.effort 落成 enabled+budget_tokens，出站前按模型名修正。
	// 改写开关默认关闭：关闭时不碰 thinking，原样透传。
	anthropicBody = s.settingService.RewriteAnthropicThinkingForModel(ctx, anthropicBody, upstreamModel)

	apiKey := strings.TrimSpace(account.GetOpenAIProtocolAPIKey())
	if apiKey == "" {
		return nil, fmt.Errorf("account %d missing api_key", account.ID)
	}
	targetURL, err := s.nativeAnthropicTargetURL(account)
	if err != nil {
		return nil, err
	}

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	reasoningEffort := extractCCReasoningEffortFromBody(body, upstreamModel, billingModel, originalModel)
	reasoningEffort = ApplyThinkingEnabledFallback(reasoningEffort, body, billingModel)

	clientCtx := openAIClientContext(ctx, c)
	upstreamCtx, releaseUpstreamCtx := detachStreamUpstreamContext(ctx, reqStream)
	upstreamReq, _, err := s.buildNativeAnthropicUpstreamRequest(upstreamCtx, c, account, anthropicBody, apiKey, targetURL)
	releaseUpstreamCtx()
	if err != nil {
		return nil, fmt.Errorf("build upstream request: %w", err)
	}

	resp, err := s.doOpenAIUpstreamWithHeaderCancel(clientCtx, upstreamReq, proxyURL, account)
	if err != nil {
		if clientErr := openAIClientErr(clientCtx); clientErr != nil {
			if errors.Is(err, errOpenAIRequestNotSent) {
				return nil, clientErr
			}
			return newClientGoneOpenAIForwardResult(
				c, account, anthropicBody, originalModel, billingModel, upstreamModel, reasoningEffort, clientStream, startTime,
			), clientErr
		}
		return nil, s.handleOpenAIUpstreamTransportError(ctx, c, account, err, true)
	}
	if !reqStream || resp.StatusCode >= http.StatusBadRequest {
		resp.Body = s.openAIBufferedBodyWithCancel(clientCtx, resp.Body)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		if clientErr := openAIClientErr(clientCtx); clientErr != nil {
			return nil, clientErr
		}
		respBody, upstreamMsg := s.readOpenAIUpstreamError(resp)
		if clientErr := openAIClientErr(clientCtx); clientErr != nil {
			return nil, clientErr
		}
		if foErr := s.failoverOpenAIUpstreamHTTPError(ctx, c, account, resp, respBody, upstreamMsg, upstreamModel); foErr != nil {
			return nil, foErr
		}
		writeChatCompletionsError(c, mapUpstreamStatusCode(resp.StatusCode), "server_error", upstreamMsg)
		return nil, fmt.Errorf("upstream error: %d %s", resp.StatusCode, upstreamMsg)
	}

	stashCompatUpstreamRequestBody(c, account, anthropicBody)
	if clientStream {
		return s.handleCCStreamingFromNativeAnthropic(resp, c, originalModel, billingModel, upstreamModel, reasoningEffort, startTime, includeUsage, account.Platform == PlatformDeepseek)
	}
	return s.handleCCBufferedFromNativeAnthropic(resp, c, originalModel, billingModel, upstreamModel, reasoningEffort, startTime, account.Platform == PlatformDeepseek)
}

// handleCCBufferedFromNativeAnthropic reads Anthropic SSE events, assembles the
// full response, then converts Anthropic → Responses → Chat Completions.
func (s *OpenAIGatewayService) handleCCBufferedFromNativeAnthropic(
	resp *http.Response,
	c *gin.Context,
	originalModel string,
	billingModel string,
	upstreamModel string,
	reasoningEffort *string,
	startTime time.Time,
	requireReasoningContent ...bool,
) (*OpenAIForwardResult, error) {
	requestID := resp.Header.Get("x-request-id")

	// The client leaving closes the upstream body immediately (no drain window);
	// input/partial output are billed from what was observed plus the estimate.
	watch := newCompatClientWatch(c, resp.Body)
	defer watch.Stop()
	var tally compatAnthropicTally
	sawMessageStop := false
	var readErr error

	scanner := bufio.NewScanner(watch.wrapResponse(resp).Body)
	maxLineSize := defaultMaxLineSize
	if s.cfg != nil && s.cfg.Gateway.MaxLineSize > 0 {
		maxLineSize = s.cfg.Gateway.MaxLineSize
	}
	attachSSEScannerBuffer(scanner, nil, maxLineSize)

	var finalResp *apicompat.AnthropicResponse
	var usage ClaudeUsage
	var outcome anthropicCompatOutcome

	// 读间隔上限：上游挂住 SSE 时中止组装（缓冲路径尚未提交响应头，可回 502）。
	streamInterval := s.anthropicNativeStreamInterval()
	pump := newAnthropicNativeLinePump(scanner, streamInterval)
	defer pump.stop()

	logReadErr := func(err error) {
		if !errors.Is(err, io.EOF) && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			logger.L().Warn("openai cc via native anthropic buffered: read error",
				zap.Error(err),
				zap.String("request_id", requestID),
			)
		}
	}
	onIdle := func() (*OpenAIForwardResult, error) {
		_ = resp.Body.Close()
		logger.L().Warn("openai cc via native anthropic buffered: data interval timeout",
			zap.String("request_id", requestID),
			zap.Duration("interval", streamInterval),
		)
		writeChatCompletionsError(c, http.StatusBadGateway, "server_error", "Upstream stream data interval timeout")
		return nil, fmt.Errorf("stream data interval timeout")
	}

	for {
		line, rerr := pump.next()
		if rerr != nil {
			if errors.Is(rerr, errAnthropicNativeStreamIdle) {
				if watch.Gone() {
					readErr = context.Canceled
					break
				}
				return onIdle()
			}
			if !errors.Is(rerr, io.EOF) {
				readErr = rerr
			}
			logReadErr(rerr)
			break
		}
		// SSE 规范允许 `event:xxx`（冒号后无空格）：Kimi 等 Anthropic 兼容上游
		// 返回紧凑格式，严格匹配 "event: " 会丢弃全部事件（#4653 同根因）。
		eventType, ok := extractOpenAISSEEventLine(line)
		if !ok {
			continue
		}
		if eventType == "error" {
			outcome.failed = true
		}

		dataLine, rerr := pump.next()
		if rerr != nil {
			if errors.Is(rerr, errAnthropicNativeStreamIdle) {
				if watch.Gone() {
					readErr = context.Canceled
					break
				}
				return onIdle()
			}
			if !errors.Is(rerr, io.EOF) {
				readErr = rerr
			}
			logReadErr(rerr)
			break
		}
		payload, ok := extractOpenAISSEDataLine(dataLine)
		if !ok {
			continue
		}

		var event apicompat.AnthropicStreamEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			continue
		}
		tally.AddData(payload)
		outcome.observe(&event, eventType, []byte(payload))
		if event.Type == "message_stop" {
			sawMessageStop = true
		}
		if event.Type == "message_start" && event.Message != nil {
			finalResp = event.Message
			mergeAnthropicUsage(&usage, event.Message.Usage)
		}
		if event.Type == "message_delta" {
			if event.Usage != nil {
				mergeAnthropicUsage(&usage, *event.Usage)
			}
			if event.Delta != nil && event.Delta.StopReason != "" && finalResp != nil {
				finalResp.StopReason = apicompat.AnthropicStopReasonPtr(event.Delta.StopReason)
			}
		}
		if event.Type == "content_block_start" && event.ContentBlock != nil && finalResp != nil {
			finalResp.Content = append(finalResp.Content, *event.ContentBlock)
		}
		if event.Type == "content_block_delta" && event.Delta != nil && finalResp != nil && event.Index != nil {
			idx := *event.Index
			if idx >= 0 && idx < len(finalResp.Content) {
				switch event.Delta.Type {
				case "text_delta":
					finalResp.Content[idx].Text += event.Delta.Text
				case "thinking_delta":
					finalResp.Content[idx].Thinking += event.Delta.Thinking
				case "input_json_delta":
					finalResp.Content[idx].Input = appendRawJSON(finalResp.Content[idx].Input, event.Delta.PartialJSON)
				}
			}
		}
	}

	result := &OpenAIForwardResult{
		RequestID: requestID, UpstreamHeaders: resp.Header,
		Usage: claudeUsageToOpenAIUsage(&usage), SearchCount: usage.WebSearchRequests,
		NonBillableUpstreamError: outcome.failed,
		Model:                    originalModel, BillingModel: billingModel, UpstreamModel: upstreamModel,
		UpstreamEndpoint: "/v1/messages", ReasoningEffort: reasoningEffort,
		Stream: false, Duration: time.Since(startTime),
		ClientDisconnect: watch.Gone(),
	}
	if outcome.failed {
		message := sanitizeUpstreamErrorMessage(extractOpenAISSEErrorMessage(outcome.source))
		writeChatCompletionsError(c, http.StatusBadGateway, "upstream_error", message)
		MarkResponseCommitted(c)
		return result, fmt.Errorf("upstream response failed: %s", message)
	}
	if watch.Gone() && !sawMessageStop {
		replaceUpTo := 0
		if !tally.sawFinalOutput {
			replaceUpTo = result.Usage.OutputTokens
		}
		cause := readErr
		if cause == nil {
			cause = context.Canceled
		}
		applyCompatIncompleteUsage(c, &result.Usage, compatIncompleteUsage{
			Path: "cc_native_anthropic_buffered", Model: originalModel, RequestID: requestID,
			ClientGone: true, Cause: cause, OutputEstimate: tally.EstimateTokens(),
			ReplaceOutputUpTo: replaceUpTo,
		})
		return result, nil
	}
	if finalResp == nil {
		writeChatCompletionsError(c, http.StatusBadGateway, "server_error", "Upstream stream ended without a response")
		return nil, fmt.Errorf("upstream stream ended without response")
	}

	finalResp.Usage = claudeUsageToAnthropicUsage(usage)

	responsesResp := apicompat.AnthropicToResponsesResponse(finalResp)
	ccResp := apicompat.ResponsesToChatCompletionsWithOptions(responsesResp, originalModel, len(requireReasoningContent) > 0 && requireReasoningContent[0])

	if s.responseHeaderFilter != nil {
		responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	}
	// 非流式响应必须是 application/json（上游被强制流式，透传头会污染）。
	c.Writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	if respBytes, err := json.Marshal(ccResp); err == nil {
		respBytes = reverseToolNamesIfPresent(c, respBytes)
		c.Data(http.StatusOK, "application/json; charset=utf-8", respBytes)
	} else {
		c.JSON(http.StatusOK, ccResp)
	}

	return &OpenAIForwardResult{
		RequestID:                requestID,
		UpstreamHeaders:          resp.Header,
		Usage:                    claudeUsageToOpenAIUsage(&usage),
		SearchCount:              usage.WebSearchRequests,
		NonBillableUpstreamError: outcome.failed,
		Model:                    originalModel,
		BillingModel:             billingModel,
		UpstreamModel:            upstreamModel,
		UpstreamEndpoint:         "/v1/messages",
		ReasoningEffort:          reasoningEffort,
		Stream:                   false,
		Duration:                 time.Since(startTime),
	}, nil
}

// handleCCStreamingFromNativeAnthropic reads Anthropic SSE events, converts each
// to Responses events, then to Chat Completions chunks, and writes them.
func (s *OpenAIGatewayService) handleCCStreamingFromNativeAnthropic(
	resp *http.Response,
	c *gin.Context,
	originalModel string,
	billingModel string,
	upstreamModel string,
	reasoningEffort *string,
	startTime time.Time,
	includeUsage bool,
	requireReasoningContent ...bool,
) (*OpenAIForwardResult, error) {
	requestID := resp.Header.Get("x-request-id")

	if s.responseHeaderFilter != nil {
		responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	}
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)

	anthState := apicompat.NewAnthropicEventToResponsesState()
	anthState.Model = originalModel
	ccState := apicompat.NewResponsesEventToChatState()
	ccState.RequireReasoningContent = len(requireReasoningContent) > 0 && requireReasoningContent[0]
	ccState.Model = originalModel
	ccState.IncludeUsage = includeUsage

	var usage ClaudeUsage
	var outcome anthropicCompatOutcome
	var firstTokenMs *int
	firstChunk := true
	clientDisconnected := false
	sawMessageStop := false
	var readErr error

	// The client leaving (request context ends or client write fails) closes the upstream
	// body immediately so we never drain upstream tokens after disconnect; input and partial
	// output are billed from observed usage plus estimates.
	watch := newCompatClientWatch(c, resp.Body)
	defer watch.Stop()
	var tally compatAnthropicTally

	scanner := bufio.NewScanner(watch.wrapResponse(resp).Body)
	maxLineSize := defaultMaxLineSize
	if s.cfg != nil && s.cfg.Gateway.MaxLineSize > 0 {
		maxLineSize = s.cfg.Gateway.MaxLineSize
	}
	attachSSEScannerBuffer(scanner, nil, maxLineSize)

	resultWithUsage := func() *OpenAIForwardResult {
		return &OpenAIForwardResult{
			RequestID:                requestID,
			UpstreamHeaders:          resp.Header,
			Usage:                    claudeUsageToOpenAIUsage(&usage),
			SearchCount:              usage.WebSearchRequests,
			NonBillableUpstreamError: outcome.failed,
			Model:                    originalModel,
			BillingModel:             billingModel,
			UpstreamModel:            upstreamModel,
			UpstreamEndpoint:         "/v1/messages",
			ReasoningEffort:          reasoningEffort,
			Stream:                   true,
			Duration:                 time.Since(startTime),
			FirstTokenMs:             firstTokenMs,
			ClientDisconnect:         clientDisconnected || watch.Gone(),
		}
	}

	streamInterval := s.anthropicNativeStreamInterval()
	pump := newAnthropicNativeLinePump(scanner, streamInterval)
	defer pump.stop()

	logReadErr := func(err error) {
		if !errors.Is(err, io.EOF) && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			logger.L().Warn("openai cc via native anthropic stream: read error",
				zap.Error(err),
				zap.String("request_id", requestID),
			)
		}
	}
	onIdle := func() (*OpenAIForwardResult, error) {
		_ = resp.Body.Close()
		gone := clientDisconnected || watch.Gone()
		if !gone {
			logger.L().Warn("openai cc via native anthropic stream: data interval timeout",
				zap.String("request_id", requestID),
				zap.Duration("interval", streamInterval),
			)
		}
		res := resultWithUsage()
		if gone {
			replaceUpTo := 0
			if !tally.sawFinalOutput {
				replaceUpTo = res.Usage.OutputTokens
			}
			applyCompatIncompleteUsage(c, &res.Usage, compatIncompleteUsage{
				Path: "cc_native_anthropic_stream", Model: originalModel, RequestID: requestID,
				ClientGone: true, Cause: context.Canceled, OutputEstimate: tally.EstimateTokens(),
				ReplaceOutputUpTo: replaceUpTo,
			})
			return res, nil
		}
		return res, fmt.Errorf("stream data interval timeout")
	}

	writeChunk := func(chunk apicompat.ChatCompletionsChunk) bool {
		if clientDisconnected {
			return false
		}
		sse, err := apicompat.ChatChunkToSSE(chunk)
		if err != nil {
			return false
		}
		out := string(reverseToolNamesIfPresent(c, []byte(sse)))
		if _, err := fmt.Fprint(c.Writer, out); err != nil {
			clientDisconnected = true
			watch.Disconnect()
			return false
		}
		return false
	}

	processAnthropicEvent := func(event *apicompat.AnthropicStreamEvent) bool {
		if firstChunk {
			firstChunk = false
			ms := int(time.Since(startTime).Milliseconds())
			firstTokenMs = &ms
		}

		if event.Type == "message_stop" {
			if outcome.failed {
				return false
			}
			sawMessageStop = true
		}
		if event.Type == "message_delta" && event.Usage != nil {
			mergeAnthropicUsage(&usage, *event.Usage)
		}
		if event.Type == "message_start" && event.Message != nil {
			mergeAnthropicUsage(&usage, event.Message.Usage)
		}

		if clientDisconnected || watch.Gone() {
			return false
		}

		responsesEvents := apicompat.AnthropicEventToResponsesEvents(event, anthState)
		for _, resEvt := range responsesEvents {
			ccChunks := apicompat.ResponsesEventToChatChunks(&resEvt, ccState)
			for _, chunk := range ccChunks {
				writeChunk(chunk)
			}
		}
		if !clientDisconnected && len(responsesEvents) > 0 {
			c.Writer.Flush()
		}
		return false
	}

	for {
		line, rerr := pump.next()
		if rerr != nil {
			if errors.Is(rerr, errAnthropicNativeStreamIdle) {
				return onIdle()
			}
			if !errors.Is(rerr, io.EOF) {
				readErr = rerr
			}
			logReadErr(rerr)
			break
		}
		eventType, ok := extractOpenAISSEEventLine(line)
		if !ok {
			continue
		}
		if eventType == "error" {
			outcome.failed = true
		}

		dataLine, rerr := pump.next()
		if rerr != nil {
			if errors.Is(rerr, errAnthropicNativeStreamIdle) {
				return onIdle()
			}
			if !errors.Is(rerr, io.EOF) {
				readErr = rerr
			}
			logReadErr(rerr)
			break
		}
		payload, ok := extractOpenAISSEDataLine(dataLine)
		if !ok {
			continue
		}

		var event apicompat.AnthropicStreamEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			continue
		}

		tally.AddData(payload)
		outcome.observe(&event, eventType, []byte(payload))
		if processAnthropicEvent(&event) {
			return resultWithUsage(), nil
		}
	}

	if outcome.failed {
		return resultWithUsage(), writeCompatUpstreamStreamError(c, "chat", originalModel, outcome.source)
	}
	if gone := clientDisconnected || watch.Gone(); gone && !sawMessageStop {
		res := resultWithUsage()
		replaceUpTo := 0
		if !tally.sawFinalOutput {
			replaceUpTo = res.Usage.OutputTokens
		}
		cause := readErr
		if cause == nil {
			cause = context.Canceled
		}
		applyCompatIncompleteUsage(c, &res.Usage, compatIncompleteUsage{
			Path: "cc_native_anthropic_stream", Model: originalModel, RequestID: requestID,
			ClientGone: true, Cause: cause, OutputEstimate: tally.EstimateTokens(),
			ReplaceOutputUpTo: replaceUpTo,
		})
		return res, nil
	}

	// Finalize both state machines（客户端已断开时仍执行，保证 usage 汇总完整）。
	finalResEvents := apicompat.FinalizeAnthropicResponsesStream(anthState)
	for _, resEvt := range finalResEvents {
		ccChunks := apicompat.ResponsesEventToChatChunks(&resEvt, ccState)
		for _, chunk := range ccChunks {
			writeChunk(chunk) //nolint:errcheck
		}
	}
	finalCCChunks := apicompat.FinalizeResponsesChatStream(ccState)
	for _, chunk := range finalCCChunks {
		writeChunk(chunk) //nolint:errcheck
	}

	if !clientDisconnected {
		fmt.Fprint(c.Writer, "data: [DONE]\n\n") //nolint:errcheck
		c.Writer.Flush()
	}

	return resultWithUsage(), nil
}
