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
	"net/url"
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
	// 关闭时返回携带 path/group_platform/hint 的 400 PROTOCOL_MISMATCH，不静默转换，
	// 也不再落到「无可用账号」的 503。
	if !CrossProtocolConversionAllowed(getAPIKeyFromContext(c)) {
		path, platform := inboundProtocolMismatchCoordinates(c, "/v1/responses")
		WriteProtocolMismatchError(c, path, platform)
		return nil, CrossProtocolConversionError{}
	}

	normalizedBody, normalized, err := normalizeOpenAIResponsesLegacyIngress(c, body)
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

	// 3. Convert Responses → Anthropic，并收集要原样并入请求体的顶层字段。
	//
	// 透明账号（accounts.extra.anthropic_passthrough：只替换认证、不改写请求，上游是
	// 宽容的中转）按「透传优先」：转换器不认识的顶层字段（previous_response_id、store、
	// include、prompt_cache_key……）全部原样并入，reasoning 也原样带给上游（不合成
	// thinking/output_config），上游拒绝就原样报错。
	//
	// 其余账号（OAuth / Vertex / 官方或未知上游的 API Key）保持 2.3.153 的转换口径：
	// reasoning.effort 映射成 thinking/output_config，未映射字段丢弃，只放行 Anthropic
	// 白名单键。原因：Anthropic 官方对未知顶层字段返回 400，且 400 不换号、不重试。
	transparent := account.IsAnthropicAPIKeyPassthroughEnabled()
	anthropicReq, passthroughFields, err := apicompat.ResponsesToAnthropicRequestWithPassthrough(&responsesReq, adaptedBody, &apicompat.ResponsesToAnthropicOptions{
		PassthroughReasoning:      transparent,
		PassthroughUnmappedFields: transparent,
	})
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
	// 5b. 未映射的 Responses 顶层字段原样透传（转换结果优先，不覆盖 messages 等）。
	anthropicBody = mergeResponsesPassthroughFieldsBestEffort(anthropicBody, passthroughFields)

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
	// 开关默认关闭：关闭时不碰 thinking，原样透传。
	anthropicBody = s.settingService.RewriteAnthropicThinkingForModel(ctx, anthropicBody, mappedModel)

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
	// 入站 query 只放行白名单内的键（目前仅 beta），且不覆盖 buildUpstreamRequest
	// 已写入的 ?beta=true；其余键（access_token、alt、key 等会改变上游鉴权或
	// 响应形态的参数）一律丢弃，不原样带给上游。
	mergeInboundQueryParams(upstreamReq, c)

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
				ResponseHeaders:        resp.Header.Clone(),
				RetryableOnSameAccount: !shouldDisable && PoolModeSameAccountRetry(account, resp.StatusCode, resp.Header, respBody),
			}
		}

		// Non-failover error: 上游状态码、错误码与错误文本原样透传。只做协议
		// envelope 的必要搬运（Anthropic error.type → Responses error.code），
		// 不把上游的 "Unsupported value: 'none'" 之类改写成统一的
		// "Upstream request failed"，客户端才能看到真实原因。
		defer GuardUpstreamFinancialError(c, resp.StatusCode, respBody)()
		writeConvertedUpstreamError(c, resp.StatusCode, respBody)
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
	// 内置检索工具按次计费的来源；累计值取最大，避免流式重复事件被累加。
	if n := src.WebSearchRequests(); n > dst.WebSearchRequests {
		dst.WebSearchRequests = n
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
	if usage.WebSearchRequests > 0 {
		n := usage.WebSearchRequests
		out.ServerToolUse = &apicompat.AnthropicServerToolUse{WebSearchRequests: &n}
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
	sawStop, sawError := false, false
	// 客户端一走就关上游，不再读完整个应答。
	clientWatch := newAnthropicClientWatch(context.Background(), c, resp.Body)
	defer clientWatch.stop()

	for scanner.Scan() {
		line := scanner.Text()
		eventType, ok := parseAnthropicSSEField(line, "event")
		if !ok {
			if anthropicDataOnlyErrorLine(line) {
				sawError = true
			}
			continue
		}
		if eventType == "error" {
			sawError = true
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
			return &ForwardResult{Usage: usage, NonBillableUpstreamError: true}, fmt.Errorf("anthropic upstream financial error")
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
		if event.Type == "error" {
			sawError = true
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
			if event.Delta != nil {
				usage.noteStopReason(event.Delta.StopReason)
				if event.Delta.StopReason != "" && finalResp != nil {
					finalResp.StopReason = apicompat.AnthropicStopReasonPtr(event.Delta.StopReason)
				}
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
		responseMsgID := ""
		if finalResp != nil {
			responseMsgID = finalResp.ID
		}
		return &ForwardResult{
			RequestID:                requestID,
			ResponseMsgID:            responseMsgID,
			UpstreamHeaders:          resp.Header,
			Usage:                    usage,
			Model:                    originalModel,
			UpstreamModel:            mappedModel,
			ReasoningEffort:          reasoningEffort,
			Stream:                   false,
			Duration:                 time.Since(startTime),
			ClientDisconnect:         true,
			NonBillableUpstreamError: sawError && !sawStop,
			// Anthropic 上游已完成的检索在客户端断开时仍要收费（issue #491）。
			SearchCount: usage.WebSearchRequests,
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

	if sawStop && !sawError && scanErr == nil {
		s.rateLimitService.observeModelTransientSuccess(context.Background(), c, estimate.account, mappedModel)
	}
	var responseMsgID string
	if finalResp != nil {
		responseMsgID = normalizeUsageTraceID(finalResp.ID)
	}
	return &ForwardResult{
		RequestID:                requestID,
		ResponseMsgID:            responseMsgID,
		UpstreamHeaders:          resp.Header,
		Usage:                    billedUsage,
		NonBillableUpstreamError: sawError && !sawStop,
		Model:                    originalModel,
		UpstreamModel:            mappedModel,
		ReasoningEffort:          reasoningEffort,
		Stream:                   false,
		Duration:                 time.Since(startTime),
		SearchCount:              billedUsage.WebSearchRequests,
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
	var responseMsgID string
	firstChunk := true
	clientDisconnected := false
	sawStop, sawError := false, false
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
			RequestID:                requestID,
			ResponseMsgID:            responseMsgID,
			UpstreamHeaders:          resp.Header,
			Usage:                    usage,
			Model:                    originalModel,
			UpstreamModel:            mappedModel,
			ReasoningEffort:          reasoningEffort,
			Stream:                   true,
			Duration:                 time.Since(startTime),
			FirstTokenMs:             firstTokenMs,
			ClientDisconnect:         clientDisconnected,
			NonBillableUpstreamError: sawError && !sawStop,
			SearchCount:              usage.WebSearchRequests,
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
		if event.Type == "error" {
			sawError = true
		}
		progress.observeEvent(event)

		// Extract usage from message_delta
		if event.Type == "message_delta" && event.Usage != nil {
			mergeAnthropicUsage(&usage, *event.Usage)
		}
		// 违规拒答的 message_delta 可以不带 usage，stop_reason 单独记。
		if event.Type == "message_delta" && event.Delta != nil {
			usage.noteStopReason(event.Delta.StopReason)
		}
		// Also capture usage from message_start
		if event.Type == "message_start" && event.Message != nil {
			if responseMsgID == "" && event.Message.ID != "" {
				responseMsgID = normalizeUsageTraceID(event.Message.ID)
			}
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
				if _, err := fmt.Fprint(c.Writer, out); err != nil {
					markClientGone("client write failed")
					break
				}
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
			if anthropicDataOnlyErrorLine(line) {
				sawError = true
			}
			continue
		}
		if eventType == "error" {
			sawError = true
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
			sawError = true
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

	result, err := finalizeStream()
	if err == nil && sawStop && !sawError && !clientDisconnected {
		s.rateLimitService.observeModelTransientSuccess(context.Background(), c, estimate.account, mappedModel)
	}
	return result, err
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
			"message": ClientUpstreamErrorMessage(message),
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

// mergeResponsesPassthroughFieldsBestEffort merges the Responses top-level
// fields the bridge does not map into the converted Anthropic body. Merging is
// additive and conversion results win; a merge failure must never fail the
// request (the worst case is the previous behavior: the field is dropped).
func mergeResponsesPassthroughFieldsBestEffort(body []byte, fields map[string]json.RawMessage) []byte {
	if len(fields) == 0 {
		return body
	}
	merged, err := apicompat.MergeResponsesPassthroughFields(body, fields)
	if err != nil {
		logger.L().Debug("responses passthrough fields merge skipped",
			zap.Int("field_count", len(fields)),
			zap.Error(err),
		)
		return body
	}
	return merged
}

// forwardedInboundQueryKeys is the allow list of inbound query parameters the
// Responses -> Anthropic bridge copies onto the upstream URL. The Messages
// endpoint defines a single query parameter, beta (set by the Anthropic SDK and
// Claude Code). Any other key a client puts on POST /v1/responses either means
// something only to this gateway or steers the upstream (access_token, alt, key
// and the like act as credentials or response-format switches at some
// providers), so it is dropped. Keys match exactly (case-sensitive), as the
// upstream would read them.
var forwardedInboundQueryKeys = map[string]struct{}{"beta": {}}

// deniedInboundQueryKeys is the defence-in-depth deny list: credential-like names
// (compared case-insensitively, ignoring "-" and "_") never reach the upstream,
// even if forwardedInboundQueryKeys is widened later.
var deniedInboundQueryKeys = map[string]struct{}{
	"key": {}, "apikey": {}, "xapikey": {}, "token": {}, "accesstoken": {}, "authtoken": {},
	"idtoken": {}, "refreshtoken": {}, "bearer": {}, "authorization": {}, "auth": {},
	"secret": {}, "clientsecret": {}, "password": {}, "signature": {}, "sig": {},
}

var queryKeyNormalizer = strings.NewReplacer("-", "", "_", "")

const (
	// maxInboundQueryKeys: a normal client sends a couple of query parameters.
	// A request carrying more is not forwarded any of them.
	maxInboundQueryKeys = 16
	// maxForwardedQueryValueLen bounds the bytes of one forwarded value; a longer
	// value is dropped, not truncated.
	maxForwardedQueryValueLen = 64
)

func isCredentialQueryKey(key string) bool {
	_, denied := deniedInboundQueryKeys[queryKeyNormalizer.Replace(strings.ToLower(key))]
	return denied
}

// mergeInboundQueryParams copies the allow-listed inbound query parameters onto
// the converted upstream request. A parameter the upstream URL already carries
// (for example beta=true) is never overridden, the existing query string stays
// byte-for-byte as built, and new parameters are appended in key order.
func mergeInboundQueryParams(upstreamReq *http.Request, c *gin.Context) {
	if upstreamReq == nil || upstreamReq.URL == nil || c == nil || c.Request == nil || c.Request.URL == nil {
		return
	}
	inbound := c.Request.URL.Query()
	if len(inbound) == 0 || len(inbound) > maxInboundQueryKeys {
		return
	}
	current := upstreamReq.URL.Query()
	extra := url.Values{}
	for key, values := range inbound {
		if _, ok := forwardedInboundQueryKeys[key]; !ok || isCredentialQueryKey(key) {
			continue
		}
		if _, exists := current[key]; exists || len(values) == 0 || len(values[0]) > maxForwardedQueryValueLen {
			continue
		}
		extra[key] = values[:1]
	}
	if len(extra) == 0 {
		return
	}
	if upstreamReq.URL.RawQuery != "" {
		upstreamReq.URL.RawQuery += "&"
	}
	upstreamReq.URL.RawQuery += extra.Encode()
}

// convertedUpstreamErrorRawBodyMax caps how much of a non-JSON upstream body
// (for example a relay's HTML error page) may be used as the client-facing
// message when the upstream gave no error.message at all.
const convertedUpstreamErrorRawBodyMax = 512

// writeConvertedUpstreamError forwards a non-failover Anthropic upstream error to
// an OpenAI Responses client while preserving the upstream status code, error
// code/type and error text. It is a transport/envelope adaptation only: the
// message is never replaced with a generic "Upstream request failed" string.
//
// The text is still redacted at this client boundary (clientprivacy.Text): the
// upstream's own wording is kept, but upstream hosts/IPs/URLs, account or proxy
// identifiers, e-mail addresses and echoed credentials ("Incorrect API key
// provided: sk-…") must never reach the end user — that is what hides which
// relay/provider sits behind the gateway.
func writeConvertedUpstreamError(c *gin.Context, statusCode int, respBody []byte) {
	errorType := strings.TrimSpace(gjson.GetBytes(respBody, "error.type").String())
	errorCode := strings.TrimSpace(gjson.GetBytes(respBody, "error.code").String())
	message := strings.TrimSpace(gjson.GetBytes(respBody, "error.message").String())
	if message == "" {
		message = strings.TrimSpace(extractUpstreamErrorMessage(respBody))
	}
	if message == "" {
		message = truncateString(strings.TrimSpace(string(respBody)), convertedUpstreamErrorRawBodyMax)
	}
	if errorCode == "" {
		errorCode = errorType
	}
	if errorCode == "" {
		errorCode = "upstream_error"
	}
	writeResponsesError(c, mapUpstreamStatusCode(statusCode), sanitizeUpstreamErrorMessage(errorCode), sanitizeUpstreamErrorMessage(message))
}
