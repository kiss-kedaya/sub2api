package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/util/responseheaders"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

// ForwardAsResponses accepts an OpenAI Responses API request body, converts it
// to Anthropic Messages format, forwards to the Anthropic upstream, and converts
// the response back to Responses format. This enables OpenAI Responses API
// clients to access Anthropic models through Anthropic platform groups.
//
// The method follows the same pattern as OpenAIGatewayService.ForwardAsAnthropic
// but in reverse direction: Responses → Anthropic upstream → Responses.
func (s *GatewayService) ForwardAsResponses(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	parsed *ParsedRequest,
) (*ForwardResult, error) {
	startTime := time.Now()

	// 分组级跨协议转换开关：Anthropic 平台分组收到 /v1/responses（OpenAI 协议族），
	// 必须经 Responses→Anthropic 转换链。与 ForwardAsChatCompletions 同口径：
	// 关闭时返回可诊断的 400，不静默转换。
	if !CrossProtocolConversionAllowed(getAPIKeyFromContext(c)) {
		writeResponsesError(c, http.StatusBadRequest, crossProtocolDisabledCode, crossProtocolDisabledMessage)
		return nil, CrossProtocolConversionError{}
	}

	normalizedBody, normalized, err := normalizeOpenAIResponsesLegacyIngress(body)
	if err != nil {
		return nil, err
	}
	if normalized {
		body = normalizedBody
	}

	// 1. Lower Codex client-side tools to function tools understood by Anthropic.
	adaptedBody, clientToolMapping, err := adaptResponsesClientToolsForAnthropic(body)
	if err != nil {
		return nil, fmt.Errorf("adapt responses client tools: %w", err)
	}

	// 2. Parse Responses request
	var responsesReq apicompat.ResponsesRequest
	if err := json.Unmarshal(adaptedBody, &responsesReq); err != nil {
		return nil, fmt.Errorf("parse responses request: %w", err)
	}
	originalModel := responsesReq.Model
	clientStream := responsesReq.Stream

	// 3. Convert Responses → Anthropic
	anthropicReq, err := apicompat.ResponsesToAnthropicRequest(&responsesReq)
	if err != nil {
		return nil, fmt.Errorf("convert responses to anthropic: %w", err)
	}

	// 3. Force upstream streaming (Anthropic works best with streaming)
	anthropicReq.Stream = true
	reqStream := true

	// 4. Model mapping
	mappedModel := originalModel
	if account.Type == AccountTypeAPIKey || account.Type == AccountTypeServiceAccount {
		mappedModel = account.GetMappedModel(originalModel)
	}
	if mappedModel == originalModel && account.Platform == PlatformAnthropic && account.Type == AccountTypeServiceAccount {
		normalized := normalizeVertexAnthropicModelID(claude.NormalizeModelID(originalModel))
		if normalized != originalModel {
			mappedModel = normalized
		}
	} else if mappedModel == originalModel && account.Platform == PlatformAnthropic && account.Type != AccountTypeAPIKey {
		normalized := claude.NormalizeModelID(originalModel)
		if normalized != originalModel {
			mappedModel = normalized
		}
	}
	reasoningEffort := ExtractResponsesReasoningEffortFromBody(body, mappedModel, originalModel)
	// 国产模型默认 effort 补充：需要 mappedModel 判定，推迟到 mapping 完成之后。
	reasoningEffort = ApplyThinkingEnabledFallback(reasoningEffort, body, mappedModel)
	anthropicReq.Model = mappedModel

	logger.L().Debug("gateway forward_as_responses: model mapping applied",
		zap.Int64("account_id", account.ID),
		zap.String("original_model", originalModel),
		zap.String("mapped_model", mappedModel),
		zap.Bool("client_stream", clientStream),
	)

	// 5. Marshal Anthropic request body
	anthropicBody, err := json.Marshal(anthropicReq)
	if err != nil {
		return nil, fmt.Errorf("marshal anthropic request: %w", err)
	}

	// 6. Apply Claude Code mimicry for OAuth accounts (non-Claude-Code endpoints).
	// OpenAI Responses 协议进来的请求永远不是 Claude Code 客户端，所以对 OAuth 账号
	// 必须完整执行 /v1/messages 主路径上的伪装链路（system 重写 + normalize + metadata 注入），
	// 否则会被 Anthropic 判为第三方应用并扣 extra usage。
	// 见 applyClaudeCodeOAuthMimicryToBody 的 godoc。
	isClaudeCode := false
	shouldMimicClaudeCode := account.IsOAuth() && !isClaudeCode

	if shouldMimicClaudeCode {
		anthropicBody = s.applyClaudeCodeOAuthMimicryToBody(ctx, c, account, anthropicBody, anthropicReq.System, mappedModel)
	}

	// Apply the same key/global TTL policy as native Messages requests.
	if ttlMode, applyTTL := s.resolveAnthropicCacheTTLMode(ctx, c, account); applyTTL {
		anthropicBody = forceEphemeralCacheControlTTL(anthropicBody, ttlMode)
	}

	// 7. Enforce cache_control block limit
	anthropicBody = enforceCacheControlLimit(anthropicBody)

	// 7b. Normalize cache_control ttl ordering (an 1h block must not follow a 5m one).
	anthropicBody = normalizeCacheControlTTLOrder(anthropicBody)

	// 7c. 同 CC 入站路径：Responses→Anthropic 转换合成的
	// thinking.type="enabled"+budget_tokens 对 Claude 5.x 必 400，按映射后的
	// 出站模型名统一修正（enabled/disabled/between_tools 三种取值）。
	anthropicBody = sanitizeAnthropicThinkingForModel(anthropicBody, mappedModel)

	// 8. Get access token
	token, tokenType, err := s.GetAccessToken(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("get access token: %w", err)
	}

	// 9. Get proxy URL
	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	// 10. Build upstream request. The call ends with the client: a departed client
	// closes the upstream at once, also while the response headers are awaited.
	upstream := newAnthropicUpstreamCall(ctx, c, reqStream)
	defer upstream.close()
	upstreamReq, wireBody, err := s.buildUpstreamRequest(upstream.attempt(), c, account, anthropicBody, token, tokenType, mappedModel, reqStream, shouldMimicClaudeCode)
	if err != nil {
		return nil, fmt.Errorf("build upstream request: %w", err)
	}

	// 11. Send request
	resp, err := s.httpUpstream.DoWithTLS(upstreamReq, proxyURL, account.ID, account.Concurrency, s.tlsFPProfileService.ResolveTLSProfile(account))
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		// The client left: no failover, no account fault. A request that was fully
		// written to the upstream is billed for its input, estimated.
		if upstream.departedClient(err) {
			var gone *ForwardResult
			if upstream.dispatched() {
				gone = anthropicClientGoneResult(account, wireBody, originalModel, mappedModel, clientStream, startTime, nil)
				if gone != nil {
					gone.ReasoningEffort = reasoningEffort
				}
			}
			return gone, newAnthropicClientGoneError(anthropicGoneRequest, err)
		}
		return nil, s.handleUpstreamTransportError(ctx, c, account, err, OpsUpstreamErrorEvent{
			UpstreamURL: safeUpstreamURL(upstreamReq.URL.String()),
		})
	}
	defer func() { _ = resp.Body.Close() }()

	// 12. Handle error response with failover
	if resp.StatusCode >= 400 {
		respBody, _ := s.readUpstreamErrorBody(resp)
		_ = resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(respBody))

		upstreamMsg := strings.TrimSpace(extractUpstreamErrorMessage(respBody))
		upstreamMsg = sanitizeUpstreamErrorMessage(upstreamMsg)

		if s.shouldFailoverUpstreamResponse(resp.StatusCode, resp.Header, respBody) {
			appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
				ProxyID:            opsUpstreamProxyID(account),
				ProxyName:          opsUpstreamProxyName(account),
				Platform:           account.Platform,
				AccountID:          account.ID,
				AccountName:        account.Name,
				UpstreamStatusCode: resp.StatusCode,
				UpstreamRequestID:  resp.Header.Get("x-request-id"),
				Kind:               "failover",
				Message:            upstreamMsg,
			})
			shouldDisable := false
			if s.rateLimitService != nil {
				shouldDisable = s.rateLimitService.HandleUpstreamError(ctx, account, resp.StatusCode, resp.Header, respBody, mappedModel)
			}
			return nil, &UpstreamFailoverError{
				StatusCode:             resp.StatusCode,
				ResponseBody:           respBody,
				RetryableOnSameAccount: !shouldDisable && PoolModeSameAccountRetry(account, resp.StatusCode, resp.Header, respBody),
			}
		}

		// Non-failover error: return Responses-formatted error to client
		defer GuardUpstreamFinancialError(c, resp.StatusCode, respBody)()
		writeResponsesError(c, mapUpstreamStatusCode(resp.StatusCode), "server_error", upstreamMsg)
		return nil, fmt.Errorf("upstream error: %d %s", resp.StatusCode, upstreamMsg)
	}

	// 13. Handle normal response (convert Anthropic → Responses)
	estimate := anthropicEstimateSource{account: account, body: wireBody, model: originalModel}
	var result *ForwardResult
	var handleErr error
	if clientStream {
		result, handleErr = s.handleResponsesStreamingResponse(resp, c, originalModel, mappedModel, reasoningEffort, startTime, clientToolMapping, estimate)
	} else {
		result, handleErr = s.handleResponsesBufferedStreamingResponse(resp, c, originalModel, mappedModel, reasoningEffort, startTime, clientToolMapping, estimate)
	}

	return result, handleErr
}

func adaptResponsesClientToolsForAnthropic(body []byte) ([]byte, apicompat.ResponsesClientToolMapping, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var requestBody map[string]any
	if err := decoder.Decode(&requestBody); err != nil {
		return body, apicompat.ResponsesClientToolMapping{}, err
	}
	additionalToolsChanged, err := liftResponsesAdditionalTools(requestBody)
	if err != nil {
		return body, apicompat.ResponsesClientToolMapping{}, err
	}

	mapping, changed, err := apicompat.AdaptResponsesClientTools(requestBody)
	if err != nil {
		return body, apicompat.ResponsesClientToolMapping{}, err
	}
	changed = changed || additionalToolsChanged
	if !changed {
		return body, mapping, nil
	}
	rebuilt, err := json.Marshal(requestBody)
	if err != nil {
		return body, apicompat.ResponsesClientToolMapping{}, err
	}
	return rebuilt, mapping, nil
}

func liftResponsesAdditionalTools(requestBody map[string]any) (bool, error) {
	input, ok := requestBody["input"].([]any)
	if !ok {
		return false, nil
	}

	tools, _ := requestBody["tools"].([]any)
	kept := make([]any, 0, len(input))
	changed := false
	for _, raw := range input {
		item, ok := raw.(map[string]any)
		if !ok || strings.TrimSpace(fmt.Sprint(item["type"])) != "additional_tools" {
			kept = append(kept, raw)
			continue
		}
		additional, ok := item["tools"].([]any)
		if !ok {
			return false, fmt.Errorf("additional_tools.tools must be an array")
		}
		tools = append(tools, additional...)
		changed = true
	}
	if !changed {
		return false, nil
	}
	requestBody["tools"] = tools
	requestBody["input"] = kept
	return true, nil
}

// ExtractResponsesReasoningEffortFromBody reads Responses API reasoning.effort
// and normalizes it for usage logging.
func ExtractResponsesReasoningEffortFromBody(body []byte, modelCandidates ...string) *string {
	raw := strings.TrimSpace(gjson.GetBytes(body, "reasoning.effort").String())
	if raw == "" {
		return nil
	}
	model := firstNonEmpty(modelCandidates...)
	if model == "" {
		model = strings.TrimSpace(gjson.GetBytes(body, "model").String())
	}
	normalized := normalizeOpenAIReasoningEffortForModel(raw, model)
	if normalized == "" {
		return nil
	}
	return &normalized
}

func mergeAnthropicUsage(dst *ClaudeUsage, src apicompat.AnthropicUsage) {
	if dst == nil {
		return
	}

	// Some Anthropic-compatible providers retain OpenAI-style prompt/cache
	// fields. Prefer those authoritative totals or hit/miss buckets over the
	// overloaded input_tokens field. This covers Kimi's changing stream
	// semantics as well as GLM/DeepSeek cache aliases.
	if src.PromptTokens > 0 || src.PromptCacheHitTokens != nil || src.PromptCacheMissTokens != nil {
		cacheReadTokens := src.CacheReadInputTokens
		if cacheReadTokens == 0 && src.CachedTokens > 0 {
			cacheReadTokens = src.CachedTokens
		}
		if cacheReadTokens == 0 && src.PromptTokensDetails != nil && src.PromptTokensDetails.CachedTokens > 0 {
			cacheReadTokens = src.PromptTokensDetails.CachedTokens
		}
		if cacheReadTokens == 0 && src.PromptCacheHitTokens != nil {
			cacheReadTokens = max(*src.PromptCacheHitTokens, 0)
		}

		if src.PromptCacheMissTokens != nil {
			dst.InputTokens = max(*src.PromptCacheMissTokens, 0)
		} else {
			dst.InputTokens = max(src.PromptTokens-cacheReadTokens-src.CacheCreationInputTokens, 0)
		}
		dst.CacheReadInputTokens = cacheReadTokens
		dst.CacheCreationInputTokens = src.CacheCreationInputTokens
	} else {
		// An explicit input_tokens=0 is only authoritative when the same event
		// restates the cache split; a bare placeholder zero must keep the
		// message_start input instead of dropping the uncached-input bucket.
		if src.HasInputTokens() && (src.InputTokens != 0 || src.HasPositiveCacheTokens()) {
			dst.InputTokens = src.InputTokens
		}
		if src.HasCacheReadInputTokens() {
			dst.CacheReadInputTokens = src.CacheReadInputTokens
		}
		if src.CacheReadInputTokens == 0 && src.CachedTokens > 0 {
			dst.CacheReadInputTokens = src.CachedTokens
		}
		if src.HasCacheCreationInputTokens() {
			dst.CacheCreationInputTokens = src.CacheCreationInputTokens
		}
	}
	if src.CacheCreation != nil {
		if value := src.CacheCreation.Ephemeral5mInputTokens; value != nil {
			dst.CacheCreation5mTokens = max(*value, 0)
		}
		if value := src.CacheCreation.Ephemeral1hInputTokens; value != nil {
			dst.CacheCreation1hTokens = max(*value, 0)
		}
		if !src.HasCacheCreationInputTokens() {
			dst.CacheCreationInputTokens = dst.CacheCreation5mTokens + dst.CacheCreation1hTokens
		}
	} else if src.HasCacheCreationInputTokens() && src.CacheCreationInputTokens == 0 {
		dst.CacheCreation5mTokens = 0
		dst.CacheCreation1hTokens = 0
	}
	if src.HasOutputTokens() {
		dst.OutputTokens = src.OutputTokens
	}
}

func claudeUsageToAnthropicUsage(usage ClaudeUsage) apicompat.AnthropicUsage {
	out := apicompat.AnthropicUsage{
		InputTokens:              usage.InputTokens,
		OutputTokens:             usage.OutputTokens,
		CacheCreationInputTokens: usage.CacheCreationInputTokens,
		CacheReadInputTokens:     usage.CacheReadInputTokens,
	}
	if usage.CacheCreation5mTokens > 0 || usage.CacheCreation1hTokens > 0 {
		out.CacheCreation = &apicompat.AnthropicCacheCreation{
			Ephemeral5mInputTokens: &usage.CacheCreation5mTokens,
			Ephemeral1hInputTokens: &usage.CacheCreation1hTokens,
		}
	}
	return out
}

// parseAnthropicSSEField parses an SSE field line in the form "field:value" or "field: value".
// According to the SSE spec (https://html.spec.whatwg.org/multipage/server-sent-events.html#event-stream-interpretation),
// the space after the colon is optional. This function handles both formats.
func parseAnthropicSSEField(line, field string) (string, bool) {
	prefix := field + ":"
	if !strings.HasPrefix(line, prefix) {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(line, prefix)), true
}

// handleResponsesBufferedStreamingResponse reads all Anthropic SSE events from
// the upstream streaming response, assembles them into a complete Anthropic
// response, converts to Responses API JSON format, and writes it to the client.
func (s *GatewayService) handleResponsesBufferedStreamingResponse(
	resp *http.Response,
	c *gin.Context,
	originalModel string,
	mappedModel string,
	reasoningEffort *string,
	startTime time.Time,
	clientToolMapping apicompat.ResponsesClientToolMapping,
	estimate anthropicEstimateSource,
) (*ForwardResult, error) {
	requestID := resp.Header.Get("x-request-id")

	scanner := bufio.NewScanner(newUpstreamErrorFrameReader(resp.Body, resolveUpstreamResponseReadLimit(s.cfg)))
	maxLineSize := defaultMaxLineSize
	if s.cfg != nil && s.cfg.Gateway.MaxLineSize > 0 {
		maxLineSize = s.cfg.Gateway.MaxLineSize
	}
	attachSSEScannerBuffer(scanner, nil, maxLineSize)

	// Accumulate the final Anthropic response from streaming events
	var finalResp *apicompat.AnthropicResponse
	var usage ClaudeUsage
	progress := &anthropicStreamProgress{}
	sawStop := false
	// 客户端一走就关上游，不再读完整个应答。
	clientWatch := newAnthropicClientWatch(context.Background(), c, resp.Body)
	defer clientWatch.stop()

	for scanner.Scan() {
		line := scanner.Text()
		eventType, ok := parseAnthropicSSEField(line, "event")
		if !ok {
			continue
		}

		// Read the data line
		if !scanner.Scan() {
			break
		}
		dataLine := scanner.Text()
		payload, ok := parseAnthropicSSEField(dataLine, "data")
		if !ok {
			continue
		}

		if upstreamFinancialFailureEnvelope([]byte(payload)) && WriteUpstreamFinancialError(c, 0, []byte(payload)) {
			return &ForwardResult{Usage: usage}, fmt.Errorf("anthropic upstream financial error")
		}

		var event apicompat.AnthropicStreamEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			logger.L().Warn("forward_as_responses buffered: failed to parse event",
				zap.Error(err),
				zap.String("request_id", requestID),
				zap.String("event_type", eventType),
			)
			continue
		}
		progress.observeEvent(&event)
		if event.Type == "message_stop" {
			sawStop = true
		}

		// message_start carries the initial response structure
		if event.Type == "message_start" && event.Message != nil {
			finalResp = event.Message
			mergeAnthropicUsage(&usage, event.Message.Usage)
		}

		// message_delta carries final usage and stop_reason
		if event.Type == "message_delta" {
			if event.Usage != nil {
				mergeAnthropicUsage(&usage, *event.Usage)
			}
			if event.Delta != nil && event.Delta.StopReason != "" && finalResp != nil {
				finalResp.StopReason = apicompat.AnthropicStopReasonPtr(event.Delta.StopReason)
			}
		}

		// Accumulate content blocks
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

	scanErr := scanner.Err()
	// The client left before the answer was complete: the upstream was closed, there
	// is nobody to write to, and no usage will ever arrive. Bill what was produced.
	if !sawStop && clientWatch.isGone() {
		clientWatch.markGone()
		estimate.fill(&usage, progress, requestID, true, scanErr)
		return &ForwardResult{
			RequestID:        requestID,
			UpstreamHeaders:  resp.Header,
			Usage:            usage,
			Model:            originalModel,
			UpstreamModel:    mappedModel,
			ReasoningEffort:  reasoningEffort,
			Stream:           false,
			Duration:         time.Since(startTime),
			ClientDisconnect: true,
		}, newAnthropicClientGoneError(anthropicGoneStream, scanErr)
	}
	if scanErr != nil {
		if !errors.Is(scanErr, context.Canceled) && !errors.Is(scanErr, context.DeadlineExceeded) {
			logger.L().Warn("forward_as_responses buffered: read error",
				zap.Error(scanErr),
				zap.String("request_id", requestID),
			)
		}
	}

	if finalResp == nil {
		writeResponsesError(c, http.StatusBadGateway, "server_error", "Upstream stream ended without a response")
		return nil, fmt.Errorf("upstream stream ended without response")
	}

	// The client-visible answer carries what the upstream reported. When the upstream
	// ended without a final usage, only the billed copy is estimated.
	billedUsage := usage
	if !sawStop {
		estimate.fill(&billedUsage, progress, requestID, false, scanErr)
	}

	// Update usage from accumulated delta
	finalResp.Usage = claudeUsageToAnthropicUsage(usage)

	// Convert to Responses format
	responsesResp := apicompat.AnthropicToResponsesResponse(finalResp)
	responsesResp.Model = originalModel // Use original model name

	if s.responseHeaderFilter != nil {
		responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	}
	// 非流式响应必须是 application/json。上游被强制流式后会返回
	// Content-Type: text/event-stream，经 WriteFilteredHeaders 透传后会污染
	// 响应头；而 c.Data/c.JSON 走 Gin 的 writeContentType（仅当头不存在时才设置），
	// 无法覆盖已存在的 SSE 头。这里显式 Set 强制改回 JSON，避免下游中间层
	// （如 new-api）按 Content-Type 误判为流式。
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

	return &ForwardResult{
		RequestID:       requestID,
		UpstreamHeaders: resp.Header,
		Usage:           billedUsage,
		Model:           originalModel,
		UpstreamModel:   mappedModel,
		ReasoningEffort: reasoningEffort,
		Stream:          false,
		Duration:        time.Since(startTime),
	}, nil
}

// handleResponsesStreamingResponse reads Anthropic SSE events from upstream,
// converts each to Responses SSE events, and writes them to the client.
func (s *GatewayService) handleResponsesStreamingResponse(
	resp *http.Response,
	c *gin.Context,
	originalModel string,
	mappedModel string,
	reasoningEffort *string,
	startTime time.Time,
	clientToolMapping apicompat.ResponsesClientToolMapping,
	estimate anthropicEstimateSource,
) (*ForwardResult, error) {
	requestID := resp.Header.Get("x-request-id")

	if s.responseHeaderFilter != nil {
		responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	}
	// 上游已确认 2xx 才开始允许 pre-header 心跳写下游（Messages 入口有 gate）。
	ArmOpenAIStreamSSEKeepalive(c)
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)

	state := apicompat.NewAnthropicEventToResponsesState()
	state.Model = originalModel
	clientToolRestorer := apicompat.NewResponsesClientToolStreamRestorer(clientToolMapping)
	var usage ClaudeUsage
	var firstTokenMs *int
	firstChunk := true
	clientDisconnected := false
	sawStop := false
	progress := &anthropicStreamProgress{}

	scanner := bufio.NewScanner(newUpstreamErrorFrameReader(resp.Body, resolveUpstreamResponseReadLimit(s.cfg)))
	maxLineSize := defaultMaxLineSize
	if s.cfg != nil && s.cfg.Gateway.MaxLineSize > 0 {
		maxLineSize = s.cfg.Gateway.MaxLineSize
	}
	attachSSEScannerBuffer(scanner, nil, maxLineSize)
	interval := time.Duration(0)
	if s.cfg != nil && s.cfg.Gateway.StreamDataIntervalTimeout > 0 {
		interval = time.Duration(s.cfg.Gateway.StreamDataIntervalTimeout) * time.Second
	}
	pump := newAnthropicNativeLinePump(scanner, interval)
	defer pump.stop()
	defer func() { _ = resp.Body.Close() }()
	clientWatch := newAnthropicClientWatch(context.Background(), c, resp.Body)
	defer clientWatch.stop()
	// 客户端已走：立刻关上游，不再排空。已读入队列的帧仍会处理，已到达的终帧保留真实 usage。
	markClientGone := func(reason string) {
		if clientDisconnected {
			return
		}
		clientDisconnected = true
		clientWatch.markGone()
		logger.L().Info("forward_as_responses stream: client disconnected, upstream connection closed",
			zap.String("request_id", requestID),
			zap.String("reason", reason),
		)
	}

	resultWithUsage := func() *ForwardResult {
		return &ForwardResult{
			RequestID:        requestID,
			UpstreamHeaders:  resp.Header,
			Usage:            usage,
			Model:            originalModel,
			UpstreamModel:    mappedModel,
			ReasoningEffort:  reasoningEffort,
			Stream:           true,
			Duration:         time.Since(startTime),
			FirstTokenMs:     firstTokenMs,
			ClientDisconnect: clientDisconnected,
		}
	}

	// processEvent handles a single parsed Anthropic SSE event.
	processEvent := func(event *apicompat.AnthropicStreamEvent) bool {
		if !clientDisconnected && clientWatch.isGone() {
			markClientGone("request context ended")
		}
		if firstChunk {
			firstChunk = false
			ms := int(time.Since(startTime).Milliseconds())
			firstTokenMs = &ms
		}
		progress.observeEvent(event)

		// Extract usage from message_delta
		if event.Type == "message_delta" && event.Usage != nil {
			mergeAnthropicUsage(&usage, *event.Usage)
		}
		// Also capture usage from message_start
		if event.Type == "message_start" && event.Message != nil {
			mergeAnthropicUsage(&usage, event.Message.Usage)
		}

		if clientDisconnected {
			return false
		}

		// Convert to Responses events
		events := apicompat.AnthropicEventToResponsesEvents(event, state)
		for _, evt := range events {
			payload, err := json.Marshal(evt)
			if err != nil {
				logger.L().Warn("forward_as_responses stream: failed to marshal event",
					zap.Error(err),
					zap.String("request_id", requestID),
				)
				continue
			}
			payload = reverseToolNamesIfPresent(c, payload)
			payloads, _, err := clientToolRestorer.RestoreEvent(payload)
			if err != nil {
				logger.L().Warn("forward_as_responses stream: failed to restore client tools",
					zap.Error(err),
					zap.String("request_id", requestID),
				)
				continue
			}
			for _, restored := range payloads {
				eventType := gjson.GetBytes(restored, "type").String()
				if _, err := fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", eventType, restored); err != nil {
					// Close the upstream at once; frames already read are still processed.
					markClientGone("client write failed")
					return false
				}
			}
		}
		if len(events) > 0 && !clientDisconnected {
			c.Writer.Flush()
		}
		return false
	}

	finalizeStream := func() (*ForwardResult, error) {
		if finalEvents := apicompat.FinalizeAnthropicResponsesStream(state); len(finalEvents) > 0 && !clientDisconnected {
			for _, evt := range finalEvents {
				sse, err := apicompat.ResponsesEventToSSE(evt)
				if err != nil {
					continue
				}
				out := string(reverseToolNamesIfPresent(c, []byte(sse)))
				fmt.Fprint(c.Writer, out) //nolint:errcheck
			}
			c.Writer.Flush()
		}
		return resultWithUsage(), nil
	}

	// Read Anthropic SSE events
	var readErr error
	for {
		line, err := pump.next()
		if err != nil {
			readErr = err
			break
		}
		eventType, ok := parseAnthropicSSEField(line, "event")
		if !ok {
			continue
		}

		// Read data line
		dataLine, err := pump.next()
		if err != nil {
			readErr = err
			break
		}
		payload, ok := parseAnthropicSSEField(dataLine, "data")
		if !ok {
			continue
		}

		if upstreamFinancialFailureEnvelope([]byte(payload)) && (clientDisconnected || WriteUpstreamFinancialError(c, 0, []byte(payload))) {
			return resultWithUsage(), fmt.Errorf("anthropic upstream financial error")
		}

		var event apicompat.AnthropicStreamEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			logger.L().Warn("forward_as_responses stream: failed to parse event",
				zap.Error(err),
				zap.String("request_id", requestID),
				zap.String("event_type", eventType),
			)
			continue
		}

		if processEvent(&event) {
			return resultWithUsage(), nil
		}
		if event.Type == "message_stop" {
			sawStop = true
			break
		}
	}

	// The upstream ended without a terminal frame, so no final usage will arrive.
	// A departed client is billed for the dispatched request; an attached client
	// only for the output that was really produced.
	if !sawStop {
		if clientDisconnected || clientWatch.isGone() {
			markClientGone("request context ended")
			estimate.fill(&usage, progress, requestID, true, readErr)
			return resultWithUsage(), newAnthropicClientGoneError(anthropicGoneStream, readErr)
		}
		estimate.fill(&usage, progress, requestID, false, readErr)
	}

	if err := readErr; err != nil && !errors.Is(err, io.EOF) {
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			logger.L().Warn("forward_as_responses stream: read error",
				zap.Error(err),
				zap.String("request_id", requestID),
			)
		}
		return resultWithUsage(), err
	}

	return finalizeStream()
}

// appendRawJSON appends a JSON fragment string to existing raw JSON.
func appendRawJSON(existing json.RawMessage, fragment string) json.RawMessage {
	// Anthropic initializes tool_use.input to {} in content_block_start, then
	// streams the actual input through input_json_delta events. Treat that empty
	// object as a placeholder instead of prefixing it to the streamed JSON.
	var existingObject map[string]json.RawMessage
	isEmptyObject := json.Unmarshal(existing, &existingObject) == nil && existingObject != nil && len(existingObject) == 0
	if len(existing) == 0 || isEmptyObject {
		return json.RawMessage(fragment)
	}
	return json.RawMessage(string(existing) + fragment)
}

// writeResponsesError writes an error response in OpenAI Responses API format.
func writeResponsesError(c *gin.Context, statusCode int, code, message string) {
	MarkResponseCommitted(c)
	c.JSON(statusCode, gin.H{
		"error": gin.H{
			"code":    code,
			"message": message,
		},
	})
}

// mapUpstreamStatusCode keeps upstream 4xx/5xx as-is. Only invalid codes become 502.
func mapUpstreamStatusCode(code int) int {
	if code >= 400 && code <= 599 {
		return code
	}
	return http.StatusBadGateway
}
