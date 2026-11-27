package service

// 国产供应商 Anthropic 协议账号的 Responses 入站反向路径。
//
// 客户端说 OpenAI Responses（/v1/responses，Codex 等）、上游是供应商原生
// Anthropic 端点（api_protocol=anthropic）时的交叉组合：请求 Responses→Anthropic
// 单次转换，响应 Anthropic 事件→Responses 事件转换。转换链与 Anthropic 平台的
// gateway_forward_as_responses.go 完全一致（复用同一组 apicompat 状态机），仅上游
// 发送/错误处理对齐 OpenAI 网关语义（模型映射、failover、transport error）。

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
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

// forwardResponsesViaNativeAnthropic serves OpenAI /v1/responses clients through
// a CN provider's native Anthropic endpoint.
//
// Conversion chain:
//
//	Request:  Responses → Anthropic (single conversion)
//	Response: Anthropic events → Responses events (stream state machine)
func (s *OpenAIGatewayService) forwardResponsesViaNativeAnthropic(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	defaultMappedModel string,
) (*OpenAIForwardResult, error) {
	startTime := time.Now()

	// 1. Lower Codex client-side tools to function tools understood by Anthropic.
	adaptedBody, clientToolMapping, err := adaptResponsesClientToolsForAnthropic(body)
	if err != nil {
		writeResponsesError(c, http.StatusBadRequest, "invalid_request_error", "Failed to adapt request tools")
		return nil, fmt.Errorf("adapt responses client tools: %w", err)
	}

	// 2. Parse Responses request
	var responsesReq apicompat.ResponsesRequest
	if err := json.Unmarshal(adaptedBody, &responsesReq); err != nil {
		writeResponsesError(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return nil, fmt.Errorf("parse responses request: %w", err)
	}
	originalModel := responsesReq.Model
	if strings.TrimSpace(originalModel) == "" {
		writeResponsesError(c, http.StatusBadRequest, "invalid_request_error", "model is required")
		return nil, fmt.Errorf("missing model in request")
	}
	clientStream := responsesReq.Stream

	// 3. Convert Responses → Anthropic。国产 Anthropic 协议端点（DeepSeek / Kimi / 智谱 /
	// MiniMax）保持 2.3.153 的转换口径：reasoning.effort 映射成 thinking/output_config，
	// 只放行 Anthropic 白名单顶层键，其余未映射字段丢弃。整包透传是透明账号
	// （accounts.extra.anthropic_passthrough，仅 Anthropic API Key）的账号级选择，见
	// ForwardAsResponses。
	anthropicReq, passthroughFields, err := apicompat.ResponsesToAnthropicRequestWithPassthrough(&responsesReq, adaptedBody, &apicompat.ResponsesToAnthropicOptions{
		PreserveReasoningText: account.Platform == PlatformDeepseek,
	})
	if err != nil {
		writeResponsesError(c, http.StatusBadRequest, "invalid_request_error", "Failed to convert request")
		return nil, fmt.Errorf("convert responses to anthropic: %w", err)
	}

	// 4. Model mapping（OpenAI 网关统一入口的映射语义）
	billingModel := resolveOpenAIForwardModel(account, originalModel, defaultMappedModel)
	upstreamModel := normalizeOpenAIModelForUpstream(account, billingModel)
	anthropicReq.Model = upstreamModel

	reasoningEffort := ExtractResponsesReasoningEffortFromBody(body, upstreamModel, billingModel, originalModel)
	reasoningEffort = ApplyThinkingEnabledFallback(reasoningEffort, body, billingModel)

	// 5. Force upstream streaming（客户端原始终决定响应格式；
	// 上游恒为流式，非流式由缓冲路径组装）。
	anthropicReq.Stream = true
	reqStream := true

	logger.L().Debug("openai responses: forwarding via native anthropic endpoint",
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
	anthropicBody = mergeResponsesPassthroughFieldsBestEffort(anthropicBody, passthroughFields)

	// 与 /v1/messages 直通路径相同的 pre-filter。
	anthropicBody = StripEmptyTextBlocks(anthropicBody)
	anthropicBody = FilterWebSearchHistoryBlocks(anthropicBody, upstreamModel)
	// Claude 5.x 只认 adaptive thinking；Responses→Anthropic 的合成会把
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
		writeResponsesError(c, mapUpstreamStatusCode(resp.StatusCode), "server_error", upstreamMsg)
		return nil, fmt.Errorf("upstream error: %d %s", resp.StatusCode, upstreamMsg)
	}

	stashCompatUpstreamRequestBody(c, account, anthropicBody)
	if clientStream {
		return s.handleResponsesStreamingFromNativeAnthropic(resp, c, originalModel, billingModel, upstreamModel, reasoningEffort, startTime, clientToolMapping)
	}
	return s.handleResponsesBufferedFromNativeAnthropic(resp, c, originalModel, billingModel, upstreamModel, reasoningEffort, startTime, clientToolMapping)
}

// anthropicCompatOutcome retains provider failures until a successful stop,
// since converters otherwise ignore error events and synthesize successful endings.
type anthropicCompatOutcome struct {
	failed bool
	source []byte
}

func (o *anthropicCompatOutcome) observe(event *apicompat.AnthropicStreamEvent, eventType string, payload []byte) {
	if event.Type == "" {
		event.Type = eventType
	}
	if event.Type == "error" || eventType == "error" {
		o.failed = true
		o.source = append(o.source[:0], payload...)
		return
	}
	if event.Type == "message_stop" {
		o.failed = false
		o.source = nil
	}
}

// handleResponsesBufferedFromNativeAnthropic reads Anthropic SSE events, assembles
// the full response, then converts Anthropic → Responses.
func (s *OpenAIGatewayService) handleResponsesBufferedFromNativeAnthropic(
	resp *http.Response,
	c *gin.Context,
	originalModel string,
	billingModel string,
	upstreamModel string,
	reasoningEffort *string,
	startTime time.Time,
	clientToolMapping apicompat.ResponsesClientToolMapping,
) (*OpenAIForwardResult, error) {
	requestID := resp.Header.Get("x-request-id")

	// The client leaving closes the upstream body immediately (no drain window);
	// input and partial output are billed from observed usage plus estimates.
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
	var rawJSON strings.Builder
	var usage ClaudeUsage
	var outcome anthropicCompatOutcome

	// 读间隔上限：上游挂住 SSE 时中止组装（缓冲路径尚未提交响应头，可回 502）。
	streamInterval := s.anthropicNativeStreamInterval()
	pump := newAnthropicNativeLinePump(scanner, streamInterval)
	defer pump.stop()

	logReadErr := func(err error) {
		if !errors.Is(err, io.EOF) && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			logger.L().Warn("openai responses via native anthropic buffered: read error",
				zap.Error(err),
				zap.String("request_id", requestID),
			)
		}
	}
	onIdle := func() (*OpenAIForwardResult, error) {
		_ = resp.Body.Close()
		logger.L().Warn("openai responses via native anthropic buffered: data interval timeout",
			zap.String("request_id", requestID),
			zap.Duration("interval", streamInterval),
		)
		writeResponsesError(c, http.StatusBadGateway, "server_error", "Upstream stream data interval timeout")
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
		// A provider may return a JSON rejection despite the requested SSE format.
		// Keep that source before the SSE parser would discard its physical lines.
		if rawJSON.Len() > 0 || strings.HasPrefix(strings.TrimSpace(line), "{") {
			if int64(rawJSON.Len()+len(line)+1) > resolveUpstreamResponseReadLimit(s.cfg) {
				openAITooLargeError(c)
				return nil, ErrUpstreamResponseBodyTooLarge
			}
			_, _ = rawJSON.WriteString(line)
			_ = rawJSON.WriteByte('\n')
			continue
		}
		// SSE 规范允许 `event:xxx`（冒号后无空格）：Kimi 等上游返回紧凑格式。
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

	if rawJSON.Len() > 0 && openAIExplicitUpstreamError(gjson.Parse(rawJSON.String()), "") {
		observeDeepSeekHostedUpstreamEvent(c.Request.Context(), "error")
		writeResponsesError(c, http.StatusBadGateway, "upstream_error", "Upstream response failed")
		MarkResponseCommitted(c)
		return nil, fmt.Errorf("upstream response failed: invalid upstream JSON envelope")
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
		writeResponsesError(c, http.StatusBadGateway, "upstream_error", message)
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
			Path: "responses_native_anthropic_buffered", Model: originalModel, RequestID: requestID,
			ClientGone: true, Cause: cause, OutputEstimate: tally.EstimateTokens(),
			ReplaceOutputUpTo: replaceUpTo,
		})
		return result, nil
	}
	if finalResp == nil {
		writeResponsesError(c, http.StatusBadGateway, "server_error", "Upstream stream ended without a response")
		return nil, fmt.Errorf("upstream stream ended without response")
	}

	finalResp.Usage = claudeUsageToAnthropicUsage(usage)

	responsesResp := apicompat.AnthropicToResponsesResponse(finalResp)
	responsesResp.Model = originalModel

	if s.responseHeaderFilter != nil {
		responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	}
	// 非流式响应必须是 application/json（上游被强制流式，透传头会污染）。
	c.Writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	if respBytes, err := json.Marshal(responsesResp); err == nil {
		respBytes = reverseToolNamesIfPresent(c, respBytes)
		respBytes, _, err = apicompat.RestoreResponsesClientToolPayload(respBytes, clientToolMapping)
		if err != nil {
			return nil, fmt.Errorf("restore responses client tools: %w", err)
		}
		c.Data(http.StatusOK, "application/json; charset=utf-8", respBytes)
	} else {
		c.JSON(http.StatusOK, responsesResp)
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

// handleResponsesStreamingFromNativeAnthropic reads Anthropic SSE events, converts
// each to Responses SSE events, and writes them to the client.
func (s *OpenAIGatewayService) handleResponsesStreamingFromNativeAnthropic(
	resp *http.Response,
	c *gin.Context,
	originalModel string,
	billingModel string,
	upstreamModel string,
	reasoningEffort *string,
	startTime time.Time,
	clientToolMapping apicompat.ResponsesClientToolMapping,
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

	state := apicompat.NewAnthropicEventToResponsesState()
	state.Model = originalModel
	clientToolRestorer := apicompat.NewResponsesClientToolStreamRestorer(clientToolMapping)

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
			logger.L().Warn("openai responses via native anthropic stream: read error",
				zap.Error(err),
				zap.String("request_id", requestID),
			)
		}
	}
	onIdle := func() (*OpenAIForwardResult, error) {
		_ = resp.Body.Close()
		gone := clientDisconnected || watch.Gone()
		if !gone {
			logger.L().Warn("openai responses via native anthropic stream: data interval timeout",
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
				Path: "responses_native_anthropic_stream", Model: originalModel, RequestID: requestID,
				ClientGone: true, Cause: context.Canceled, OutputEstimate: tally.EstimateTokens(),
				ReplaceOutputUpTo: replaceUpTo,
			})
			return res, nil
		}
		return res, fmt.Errorf("stream data interval timeout")
	}

	processAnthropicEvent := func(event *apicompat.AnthropicStreamEvent) {
		if firstChunk {
			firstChunk = false
			ms := int(time.Since(startTime).Milliseconds())
			firstTokenMs = &ms
		}

		if event.Type == "message_stop" {
			if outcome.failed {
				return
			}
			sawMessageStop = true
		}
		if event.Type == "message_delta" && event.Usage != nil {
			mergeAnthropicUsage(&usage, *event.Usage)
		}
		if event.Type == "message_start" && event.Message != nil {
			mergeAnthropicUsage(&usage, event.Message.Usage)
		}

		events := apicompat.AnthropicEventToResponsesEvents(event, state)
		if clientDisconnected || watch.Gone() {
			return
		}
		for _, evt := range events {
			payload, err := json.Marshal(evt)
			if err != nil {
				continue
			}
			payload = reverseToolNamesIfPresent(c, payload)
			payloads, _, err := clientToolRestorer.RestoreEvent(payload)
			if err != nil {
				continue
			}
			for _, restored := range payloads {
				eventType := gjson.GetBytes(restored, "type").String()
				if _, err := fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", eventType, restored); err != nil {
					clientDisconnected = true
					watch.Disconnect()
					return
				}
			}
		}
		if len(events) > 0 {
			c.Writer.Flush()
		}
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
		processAnthropicEvent(&event)
	}

	if outcome.failed {
		return resultWithUsage(), writeCompatUpstreamStreamError(c, "responses", originalModel, outcome.source)
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
			Path: "responses_native_anthropic_stream", Model: originalModel, RequestID: requestID,
			ClientGone: true, Cause: cause, OutputEstimate: tally.EstimateTokens(),
			ReplaceOutputUpTo: replaceUpTo,
		})
		return res, nil
	}

	// Finalize state machine（客户端已断开时仍推进，保证 usage 汇总完整；仅在
	// 客户端仍连接时写出）。终态帧与逐事件路径一致过工具名反转与客户端工具还原，
	// 避免流截断时终态帧携带改写后的工具名。
	if finalEvents := apicompat.FinalizeAnthropicResponsesStream(state); len(finalEvents) > 0 && !clientDisconnected {
		wrote := false
		for _, evt := range finalEvents {
			payload, err := json.Marshal(evt)
			if err != nil {
				continue
			}
			payload = reverseToolNamesIfPresent(c, payload)
			payloads, _, err := clientToolRestorer.RestoreEvent(payload)
			if err != nil {
				continue
			}
			for _, restored := range payloads {
				eventType := gjson.GetBytes(restored, "type").String()
				if _, err := fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", eventType, restored); err != nil {
					clientDisconnected = true
					break
				}
				wrote = true
			}
			if clientDisconnected {
				break
			}
		}
		if wrote {
			c.Writer.Flush()
		}
	}

	return resultWithUsage(), nil
}
