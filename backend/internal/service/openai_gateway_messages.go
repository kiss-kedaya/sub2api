package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/util/responseheaders"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

// ForwardAsAnthropic accepts an Anthropic Messages request body, converts it
// to OpenAI Responses API format, forwards to the OpenAI upstream, and converts
// the response back to Anthropic Messages format. This enables Claude Code
// clients to access OpenAI models through the standard /v1/messages endpoint.
func (s *OpenAIGatewayService) ForwardAsAnthropic(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	promptCacheKey string,
	defaultMappedModel string,
) (*OpenAIForwardResult, error) {
	// Preserve the existing platform-scoped repairs for invalid tool schemas.
	// This is shared validation, not protocol conversion; valid schemas and
	// vendor-specific request fields remain unchanged on the native path.
	if sanitized, changed, err := sanitizeOpenAIResponsesToolSchemasForPlatform(body, account.Platform); err != nil {
		return nil, err
	} else if changed {
		body = sanitized
	}
	beginUpstreamResponseModelObservation(c)
	ClearActualOpenAIUpstreamEndpoint(c)
	// Web Search 模拟：Chat Completions 转换会丢掉 web_search 工具，纯 web_search
	// 请求在此直接调用搜索 API 构造 Anthropic 响应，不访问上游。
	if groupID := getOpenAIGroupIDFromContext(c); shouldEmulateWebSearchFor(ctx, s.settingService, s.channelService, account, &groupID, body) {
		stream := gjson.GetBytes(body, "stream").Bool()
		res, err := writeEmulatedWebSearch(ctx, c, account, body, gjson.GetBytes(body, "model").String(), stream)
		if err != nil {
			return nil, err
		}
		return &OpenAIForwardResult{Model: res.Model, Stream: stream, Duration: res.Duration}, nil
	}
	setCodexToolNameReverse(c, nil)
	if _, err := s.prepareCodexAccountIdentitySource(ctx, c, account); err != nil {
		return nil, err
	}

	// 原生优先：显式 anthropic 或实际配置了原生端点的 adaptive 账号，
	// 入站 /v1/messages 直通，保留
	// thinking / tool_use / cache 语义——不论分组是否开了跨协议转换，都先走这条。
	// 跨协议转换只在账号确实没有原生 Anthropic 端点时才作为兜底（见下方开关）。
	//
	// 2026-09-26（424684b6e）曾把 adaptive 改走 Responses/Chat 转换以换取上游缓存
	// 命中（部分国产中转的 /v1/messages 不返回 prompt 缓存）。2026-10-03 按运营要求
	// 改回原生优先：转换链反复丢参数的代价高于缓存命中率。
	if shouldForwardMessagesViaNativeAnthropic(account) {
		// 同协议族（messages 入站 -> Anthropic 原生上游），不涉及跨协议转换。
		SetActualOpenAIUpstreamEndpoint(c, "/v1/messages")
		return s.forwardAnthropicViaNativeAnthropicEndpoint(ctx, c, account, body, defaultMappedModel)
	}

	// 分组级跨协议转换开关：到这里说明上游不是 Anthropic 族，本次请求必然要
	// 把 Anthropic Messages 转成 OpenAI 族的 Chat Completions 或 Responses。
	// 默认关闭时直接返回可诊断的 400，不再静默转换。
	if !crossProtocolConversionAllowedFromContext(c) {
		writeAnthropicError(c, http.StatusBadRequest, crossProtocolDisabledCode, crossProtocolDisabledMessage)
		return nil, CrossProtocolConversionError{}
	}

	// 固定 chat_completions 的 CN 账号，以及不支持 Responses 的其他 APIKey
	// 账号，均将 Messages 转为 CC；固定 responses 的 CN 账号不受探针旧值覆盖。
	if shouldForwardOpenAIResponsesViaRawChatCompletions(account) {
		SetActualOpenAIUpstreamEndpoint(c, "/v1/chat/completions")
		return s.forwardAnthropicViaRawChatCompletions(ctx, c, account, body, defaultMappedModel)
	}

	startTime := time.Now()

	// 1. Parse Anthropic request
	var anthropicReq apicompat.AnthropicRequest
	if err := json.Unmarshal(body, &anthropicReq); err != nil {
		return nil, fmt.Errorf("parse anthropic request: %w", err)
	}
	anthropicDigestReq := cloneAnthropicRequestForDigest(&anthropicReq)
	originalModel := anthropicReq.Model
	applyOpenAICompatModelNormalization(&anthropicReq)
	normalizedModel := anthropicReq.Model
	clientStream := anthropicReq.Stream // client's original stream preference

	// 2. Model mapping
	billingModel := resolveOpenAIForwardModel(account, normalizedModel, defaultMappedModel)
	upstreamModel := normalizeOpenAIModelForUpstream(account, billingModel)
	if err := validateGPT61SolCompatRequest(body, upstreamModel); err != nil {
		writeAnthropicError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return nil, err
	}
	promptCacheKey = strings.TrimSpace(promptCacheKey)
	apiKeyID := getAPIKeyIDFromContext(c)
	anthropicDigestChain := ""
	anthropicMatchedDigestChain := ""
	compatPromptCacheInjected := false
	// Grok is outside the gpt-5/codex compat injector, but Claude Code still
	// carries a stable session id. Prefer that as the Grok prompt-cache seed so
	// multi-turn /v1/messages traffic can hit xAI's server-side cache.
	if promptCacheKey == "" && account.Platform == PlatformGrok {
		if sessionSeed := extractClaudeCodeSessionID(c, body); sessionSeed != "" {
			promptCacheKey = sessionSeed
			compatPromptCacheInjected = true
		} else if sessionSeed := promptCacheKeyFromAnthropicMetadataSession(&anthropicReq); sessionSeed != "" {
			promptCacheKey = sessionSeed
			compatPromptCacheInjected = true
		}
	}
	if promptCacheKey == "" && shouldAutoInjectPromptCacheKeyForCompat(upstreamModel) {
		promptCacheKey = promptCacheKeyFromAnthropicMetadataSession(&anthropicReq)
		if promptCacheKey == "" {
			promptCacheKey = deriveAnthropicCacheControlPromptCacheKey(&anthropicReq)
		}
		if promptCacheKey == "" {
			anthropicDigestChain = buildOpenAICompatAnthropicDigestChain(anthropicDigestReq)
			if reusedKey, matchedChain := s.findOpenAICompatAnthropicDigestPromptCacheKey(account, apiKeyID, anthropicDigestChain); reusedKey != "" {
				promptCacheKey = reusedKey
				anthropicMatchedDigestChain = matchedChain
			} else {
				promptCacheKey = promptCacheKeyFromAnthropicDigest(anthropicDigestChain)
			}
		}
		compatPromptCacheInjected = promptCacheKey != ""
	}
	compatReplayTrimmed := false
	compatReplayGuardEnabled := shouldAutoInjectPromptCacheKeyForCompat(upstreamModel)
	compatContinuationEnabled := openAICompatContinuationEnabled(account, upstreamModel)
	previousResponseID := ""
	if compatContinuationEnabled {
		previousResponseID = s.getOpenAICompatSessionResponseID(ctx, c, account, promptCacheKey)
	}
	compatContinuationDisabled := compatContinuationEnabled &&
		s.isOpenAICompatSessionContinuationDisabled(ctx, c, account, promptCacheKey)
	compatTurnState := ""
	// ChatGPT/Codex credentials rely on session_id + x-codex-turn-state; trimming to a
	// sliding 12-message window makes the cached prefix stall at system/tools.
	// Keep full replay there so upstream prompt caching can grow turn by turn.
	if compatReplayGuardEnabled && !account.UsesOpenAICodexProtocol() && previousResponseID == "" && !compatContinuationDisabled {
		compatReplayTrimmed = applyAnthropicCompatFullReplayGuard(&anthropicReq)
	}

	// 3. Convert Anthropic → Responses after compatibility-only replay guard.
	responsesReq, err := apicompat.AnthropicToResponsesWithOptions(&anthropicReq, &apicompat.AnthropicToResponsesOptions{
		PreserveThinkingContent: account.Platform == PlatformDeepseek,
	})
	if err != nil {
		return nil, fmt.Errorf("convert anthropic to responses: %w", err)
	}

	// 上游一律用流式：Anthropic→Responses 这条转换链只有一个上游读取实现，
	// 它按 SSE 解析（见 handleAnthropicBufferedStreamingResponse：客户端要同步时
	// 把流缓冲成 JSON 返回）。若这里跟随客户端发 stream=false，上游回的是普通
	// JSON，该解析器会报 "stream ended without a terminal response event"。
	responsesReq.Stream = true
	isStream := true

	// 3b. Handle BetaFastMode → service_tier: "priority"
	if containsBetaToken(c.GetHeader("anthropic-beta"), claude.BetaFastMode) {
		responsesReq.ServiceTier = "priority"
	}

	responsesReq.Model = upstreamModel
	if responsesReq.Reasoning != nil {
		responsesReq.Reasoning.Effort = openAICompatAnthropicReasoningEffort(&anthropicReq, upstreamModel, responsesReq.Reasoning.Effort)
	}
	if previousResponseID != "" {
		responsesReq.PreviousResponseID = previousResponseID
		trimAnthropicCompatResponsesInputToLatestTurn(responsesReq)
	}
	if compatReplayGuardEnabled && !account.UsesOpenAICodexProtocol() {
		appendOpenAICompatClaudeCodeTodoGuard(responsesReq)
	}

	logFields := []zap.Field{
		zap.Int64("account_id", account.ID),
		zap.String("original_model", originalModel),
		zap.String("normalized_model", normalizedModel),
		zap.String("billing_model", billingModel),
		zap.String("upstream_model", upstreamModel),
		zap.Bool("stream", isStream),
	}
	if compatPromptCacheInjected {
		logFields = append(logFields,
			zap.Bool("compat_prompt_cache_key_injected", true),
			zap.String("compat_prompt_cache_key_sha256", hashSensitiveValueForLog(promptCacheKey)),
		)
	}
	if compatReplayTrimmed {
		logFields = append(logFields,
			zap.Bool("compat_full_replay_trimmed", true),
			zap.Int("compat_messages_after_trim", len(anthropicReq.Messages)),
		)
	}
	if previousResponseID != "" {
		logFields = append(logFields,
			zap.Bool("compat_previous_response_id_attached", true),
			zap.String("compat_previous_response_id", truncateOpenAIWSLogValue(previousResponseID, openAIWSIDValueMaxLen)),
		)
	}
	if compatTurnState != "" {
		logFields = append(logFields, zap.Bool("compat_turn_state_attached", true))
	}
	logger.L().Debug("openai messages: model mapping applied", logFields...)

	// 4. Marshal Responses request body, then apply the ChatGPT/Codex transform.
	responsesBody, err := json.Marshal(responsesReq)
	if err != nil {
		return nil, fmt.Errorf("marshal responses request: %w", err)
	}

	if account.UsesOpenAICodexProtocol() && account.Platform != PlatformGrok {
		var reqBody map[string]any
		if err := json.Unmarshal(responsesBody, &reqBody); err != nil {
			return nil, fmt.Errorf("unmarshal for codex transform: %w", err)
		}
		codexResult := applyCodexOAuthTransformWithOptions(reqBody, codexOAuthTransformOptions{
			SkipDefaultInstructions: true,
			PreserveToolCallIDs:     true,
		})
		if codexResult.Error != nil {
			writeAnthropicError(c, http.StatusBadRequest, "invalid_request_error", codexResult.Error.Error())
			return nil, codexResult.Error
		}
		setCodexToolNameReverse(c, codexResult.ToolNameReverse)
		forcedTemplateText := ""
		if s.cfg != nil {
			forcedTemplateText = s.cfg.Gateway.ForcedCodexInstructionsTemplate
		}
		templateUpstreamModel := upstreamModel
		if codexResult.NormalizedModel != "" {
			templateUpstreamModel = codexResult.NormalizedModel
		}
		existingInstructions, _ := reqBody["instructions"].(string)
		if strings.TrimSpace(existingInstructions) == "" {
			existingInstructions = extractPromptLikeInstructionsFromInput(reqBody)
		}
		if _, err := applyForcedCodexInstructionsTemplate(reqBody, forcedTemplateText, forcedCodexInstructionsTemplateData{
			ExistingInstructions: strings.TrimSpace(existingInstructions),
			OriginalModel:        originalModel,
			NormalizedModel:      normalizedModel,
			BillingModel:         billingModel,
			UpstreamModel:        templateUpstreamModel,
		}); err != nil {
			return nil, err
		}
		ensureCodexOAuthInstructionsField(reqBody)
		if shouldAutoInjectPromptCacheKeyForCompat(upstreamModel) {
			appendOpenAICompatClaudeCodeTodoGuardToRequestBody(reqBody)
		}
		if codexResult.NormalizedModel != "" {
			upstreamModel = codexResult.NormalizedModel
		}
		if codexResult.PromptCacheKey != "" {
			promptCacheKey = codexResult.PromptCacheKey
		}
		applyCodexAccountIdentityClientMetadataMap(reqBody, codexAccountIdentitySource(c, account), apiKeyID)
		delete(reqBody, "prompt_cache_key")
		if shouldAutoInjectPromptCacheKeyForCompat(upstreamModel) {
			compatTurnState = s.getOpenAICompatSessionTurnState(ctx, c, account, promptCacheKey)
		}
		// OAuth codex transform forces stream=true upstream, so always use
		// the streaming response handler regardless of what the client asked.
		isStream = true
		responsesBody, err = json.Marshal(reqBody)
		if err != nil {
			return nil, fmt.Errorf("remarshal after codex transform: %w", err)
		}
	}

	// For API key accounts (including OpenAI-compatible upstream gateways),
	// ensure promptCacheKey is also propagated via the request body so that
	// upstreams using the Responses API can derive a stable session identifier
	// from prompt_cache_key. This makes our Anthropic /v1/messages compatibility
	// path behave more like a native Responses client.
	if account.Type == AccountTypeAPIKey {
		if trimmedKey := strings.TrimSpace(promptCacheKey); trimmedKey != "" {
			var reqBody map[string]any
			if err := json.Unmarshal(responsesBody, &reqBody); err != nil {
				return nil, fmt.Errorf("unmarshal for prompt cache key injection: %w", err)
			}
			if existing, ok := reqBody["prompt_cache_key"].(string); !ok || strings.TrimSpace(existing) == "" {
				reqBody["prompt_cache_key"] = trimmedKey
				updated, err := json.Marshal(reqBody)
				if err != nil {
					return nil, fmt.Errorf("remarshal after prompt cache key injection: %w", err)
				}
				responsesBody = updated
			}
		}
	}
	if account.Platform == PlatformOpenAI {
		policyBody, changed, policyErr := ApplyOpenAIReasoningEffortPolicyFromContext(ctx, responsesBody)
		if policyErr != nil {
			if IsReasoningEffortPolicyDenied(policyErr) {
				MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
				writeAnthropicError(c, http.StatusForbidden, "forbidden_error", policyErr.Error())
			}
			return nil, policyErr
		}
		if changed {
			responsesBody = policyBody
			if responsesReq.Reasoning != nil {
				responsesReq.Reasoning.Effort = gjson.GetBytes(responsesBody, "reasoning.effort").String()
			}
		}
	}

	// 4c. Apply OpenAI fast policy (may filter service_tier or block the request).
	// Mirrors the Claude anthropic-beta "fast-mode-2026-02-01" filter, but keyed
	// on the body-level service_tier field (priority/flex).
	responsesBody, _, err = normalizeGPT6ResponsesSampling(responsesBody, upstreamModel)
	if err != nil {
		return nil, err
	}
	updatedBody, policyErr := s.applyOpenAIFastPolicyToBody(ctx, account, upstreamModel, responsesBody)
	if policyErr != nil {
		var blocked *OpenAIFastBlockedError
		if errors.As(policyErr, &blocked) {
			MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
			writeAnthropicError(c, http.StatusForbidden, "forbidden_error", blocked.Message)
		}
		return nil, policyErr
	}
	responsesBody = updatedBody
	responsesReq.ServiceTier = normalizedOpenAIServiceTierValue(gjson.GetBytes(responsesBody, "service_tier").String())
	grokCacheIdentity := ""
	if account.Platform == PlatformGrok {
		grokIntentBody := responsesBody
		grokCacheIdentity = resolveGrokCacheIdentity(c, grokIntentBody, promptCacheKey, upstreamModel)
		patchedBody, patchErr := patchGrokResponsesBody(grokIntentBody, upstreamModel)
		if patchErr != nil {
			return nil, patchErr
		}
		responsesBody, patchErr = applyGrokResponsesCacheIdentity(patchedBody, grokIntentBody, grokCacheIdentity, account.IsGrokOAuth())
		if patchErr != nil {
			return nil, fmt.Errorf("apply grok prompt cache identity: %w", patchErr)
		}
		responsesBody, patchErr = applyGrokFreeMessagesFunctionToolCacheRoute(responsesBody, grokIntentBody, account, grokCacheIdentity)
		if patchErr != nil {
			return nil, fmt.Errorf("apply grok Free function-tool cache route: %w", patchErr)
		}
	}

	// 5. Get access token
	token, _, err := s.getRequestCredential(ctx, c, account)
	if err != nil {
		return nil, fmt.Errorf("get access token: %w", err)
	}

	// 6. Build upstream request
	if account.UsesOpenAICodexProtocol() && account.Platform != PlatformGrok {
		// Messages 兼容桥即使 body 未带 todo-guard/prompt_cache_key 标记（如映射到非
		// gpt-5/codex 模型），也必须让 buildUpstreamRequest 走 bridge 分支，以保留
		// 既有 body/session/conversation 行为。身份头在 post-build 阶段统一恢复。
		setOpenAICompatMessagesBridgeContext(c, true)
	}
	upstreamCtx, releaseUpstreamCtx := detachUpstreamContext(ctx)
	var upstreamReq *http.Request
	if account.Platform == PlatformGrok {
		upstreamReq, err = buildGrokResponsesRequest(upstreamCtx, c, account, responsesBody, token, grokCacheIdentity, s.cfg, s.settingService)
	} else {
		upstreamReq, err = s.buildUpstreamRequest(upstreamCtx, c, account, responsesBody, token, isStream, promptCacheKey, false)
	}
	releaseUpstreamCtx()
	if err != nil {
		return nil, fmt.Errorf("build upstream request: %w", err)
	}

	// Override session_id with a deterministic UUID derived from the isolated
	// session key, ensuring different API keys produce different upstream sessions.
	if account.Platform != PlatformGrok && promptCacheKey != "" {
		isolatedSessionID := generateSessionUUID(isolateOpenAIUpstreamSessionID(apiKeyID, codexAccountIdentitySource(c, account), promptCacheKey))
		upstreamReq.Header.Set("session_id", isolatedSessionID)
		if upstreamReq.Header.Get("conversation_id") != "" {
			upstreamReq.Header.Set("conversation_id", isolatedSessionID)
		}
	}
	if account.UsesOpenAICodexProtocol() && account.Platform != PlatformGrok {
		// buildUpstreamRequest 保留 Messages bridge 的 body/session 兼容行为，并会先
		// 清除身份头。真正发送前恢复完整 Codex 身份，避免 ChatGPT Codex 上游因缺失
		// originator/OpenAI-Beta 返回 404（issue #3901）。
		ensureCodexIdentityHeaders(upstreamReq.Header)
		enforceCodexIdentityHeaders(upstreamReq.Header)
		logger.L().Debug("openai messages: upstream identity restored",
			zap.Int64("account_id", account.ID),
			zap.String("upstream_model", upstreamModel),
			zap.Bool("compat_identity_restored", true),
		)
	}
	if account.UsesOpenAICodexProtocol() && promptCacheKey != "" && strings.TrimSpace(c.GetHeader("conversation_id")) == "" {
		upstreamReq.Header.Del("conversation_id")
	}
	if compatTurnState != "" && upstreamReq.Header.Get("x-codex-turn-state") == "" {
		upstreamReq.Header.Set("x-codex-turn-state", compatTurnState)
	}
	if err := s.applyOpenAICodexTicket(ctx, account, upstreamModel, upstreamReq.Header); err != nil {
		return nil, err
	}

	// 7. Send request
	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	// Grok may reject encrypted reasoning replayed under a different OAuth
	// account/cache identity. Match forwardGrokResponses: one strip+retry before
	// treating the 400 as a hard failure / failover trigger.
	var resp *http.Response
	for attempt := 0; ; attempt++ {
		if attempt > 0 {
			if account.Platform != PlatformGrok {
				break
			}
			upstreamCtxRetry, releaseRetry := detachUpstreamContext(ctx)
			upstreamReq, err = buildGrokResponsesRequest(upstreamCtxRetry, c, account, responsesBody, token, grokCacheIdentity, s.cfg, s.settingService)
			releaseRetry()
			if err != nil {
				return nil, fmt.Errorf("build grok retry request: %w", err)
			}
		}
		resp, err = s.doOpenAIUpstream(upstreamReq, proxyURL, account)
		if err != nil {
			return nil, s.handleOpenAIUpstreamTransportError(ctx, c, account, err, false)
		}
		if account.Platform != PlatformGrok || attempt > 0 || resp.StatusCode != http.StatusBadRequest {
			break
		}
		respBody := s.readUpstreamErrorBody(resp)
		if resp.Body != nil {
			_ = resp.Body.Close()
		}
		// Prefer explicit decrypt errors; also strip once on any 400 when the
		// outbound body still carries reasoning.encrypted_content (account
		// switch often returns opaque "Upstream error: 400").
		shouldStrip := isGrokInvalidEncryptedContentResponse(resp.StatusCode, respBody) ||
			requestHasGrokEncryptedReasoning(responsesBody)
		if !shouldStrip {
			resp.Body = io.NopCloser(bytes.NewReader(respBody))
			break
		}
		retryBody, changed, trimErr := trimGrokInvalidEncryptedContentRetryBody(responsesBody)
		if trimErr != nil {
			return nil, fmt.Errorf("prepare Grok invalid encrypted_content retry: %w", trimErr)
		}
		if !changed {
			resp.Body = io.NopCloser(bytes.NewReader(respBody))
			break
		}
		responsesBody = retryBody
		logger.L().Info("openai messages: retrying after stripping invalid Grok encrypted_content",
			zap.Int64("account_id", account.ID),
			zap.Bool("cache_identity_present", strings.TrimSpace(grokCacheIdentity) != ""),
			zap.String("upstream_error_preview", truncateOpenAIWSLogValue(string(respBody), 240)),
		)
	}
	defer func() { _ = resp.Body.Close() }()

	// 8. Handle error response with failover
	if resp.StatusCode >= 400 {
		respBody, upstreamMsg := s.readOpenAIUpstreamError(resp)
		if !agentIdentityTaskRecoveryWasTried(ctx) && s.isAgentIdentityAccount(ctx, account) && isAgentIdentityTaskInvalidHTTPResponse(resp.StatusCode, respBody) {
			expectedTaskID := account.GetCredential("task_id")
			if err := s.recoverAgentIdentityTask(ctx, account, expectedTaskID); err != nil {
				return nil, fmt.Errorf("agent identity task recovery failed: %w", err)
			}
			return s.ForwardAsAnthropic(markAgentIdentityTaskRecoveryTried(ctx), c, account, body, promptCacheKey, defaultMappedModel)
		}
		if previousResponseID != "" && (isOpenAICompatPreviousResponseNotFound(resp.StatusCode, upstreamMsg, respBody) || isOpenAICompatPreviousResponseUnsupported(resp.StatusCode, upstreamMsg, respBody)) {
			if isOpenAICompatPreviousResponseUnsupported(resp.StatusCode, upstreamMsg, respBody) {
				s.disableOpenAICompatSessionContinuation(ctx, c, account, promptCacheKey)
			} else {
				s.deleteOpenAICompatSessionResponseID(ctx, c, account, promptCacheKey)
			}
			logger.L().Info("openai messages: previous_response_id unavailable, retrying without continuation",
				zap.Int64("account_id", account.ID),
				zap.String("previous_response_id", truncateOpenAIWSLogValue(previousResponseID, openAIWSIDValueMaxLen)),
				zap.String("upstream_model", upstreamModel),
			)
			return s.ForwardAsAnthropic(ctx, c, account, body, promptCacheKey, defaultMappedModel)
		}
		// Grok account-switched history often fails decrypt; strip encrypted
		// reasoning once at the client-body level so failover accounts can accept
		// the multi-turn tool continuation instead of cascading 400s.
		if account.Platform == PlatformGrok &&
			isGrokInvalidEncryptedContentResponse(resp.StatusCode, respBody) &&
			!grokEncryptedContentStripRetried(ctx) {
			if strippedBody, ok := stripAnthropicThinkingSignatures(body); ok {
				logger.L().Info("openai messages: stripping thinking signatures for Grok failover retry",
					zap.Int64("account_id", account.ID),
				)
				return s.ForwardAsAnthropic(markGrokEncryptedContentStripRetried(ctx), c, account, strippedBody, promptCacheKey, defaultMappedModel)
			}
		}
		if foErr := s.failoverOpenAIUpstreamHTTPError(ctx, c, account, resp, respBody, upstreamMsg, upstreamModel); foErr != nil {
			return nil, foErr
		}
		// Non-failover error: return Anthropic-formatted error to client
		return s.handleAnthropicErrorResponse(resp, c, account, billingModel)
	}
	if account.Platform == PlatformGrok && account.Type == AccountTypeOAuth && !account.IsShadow() {
		s.updateGrokUsageFromResponse(withGrokTeamRateLimitModel(ctx, upstreamModel), account, resp.Header, resp.StatusCode)
	}

	if account.UsesOpenAICodexProtocol() && promptCacheKey != "" {
		if turnState := strings.TrimSpace(resp.Header.Get("x-codex-turn-state")); turnState != "" {
			s.bindOpenAICompatSessionTurnState(ctx, c, account, promptCacheKey, turnState)
		}
	}

	// 9. Handle normal response
	// Upstream is always streaming; choose response format based on client preference.
	// Keep the body that went upstream: a request that ends without a usage report is
	// billed for the input it really sent (see applyCompatIncompleteUsage).
	stashCompatUpstreamRequestBody(c, account, responsesBody)
	var result *OpenAIForwardResult
	var handleErr error
	if clientStream {
		result, handleErr = s.handleAnthropicStreamingResponse(resp, c, account, originalModel, billingModel, upstreamModel, startTime)
	} else {
		// Client wants JSON: buffer the streaming response and assemble a JSON reply.
		result, handleErr = s.handleAnthropicBufferedStreamingResponse(resp, c, account, originalModel, billingModel, upstreamModel, startTime)
	}

	// cyber_policy：标记已设、error 已按 Anthropic 格式发给客户端。丢弃 result、返回哨兵，
	// 使 handler 落入 tokens=0 免费用量行（对齐 /v1/responses），不计费、不 failover。
	if GetOpsCyberPolicy(c) != nil {
		if handleErr == nil {
			handleErr = errOpenAICyberPolicyForwarded
		}
		return nil, handleErr
	}

	// Propagate ServiceTier and ReasoningEffort to result for billing
	if handleErr == nil && result != nil {
		if compatContinuationEnabled && promptCacheKey != "" && result.ResponseID != "" {
			s.bindOpenAICompatSessionResponseID(ctx, c, account, promptCacheKey, result.ResponseID)
		}
		if promptCacheKey != "" && anthropicDigestChain != "" {
			s.bindOpenAICompatAnthropicDigestPromptCacheKey(account, apiKeyID, anthropicDigestChain, promptCacheKey, anthropicMatchedDigestChain)
		}
		// 计费 tier 优先采用上游回显值；上游未回显时回退到最终出站 body（经过
		// fast policy filter/force 之后）里的 tier。
		if tier := resolvedOpenAIUpstreamServiceTier(c, extractOpenAIServiceTierFromBody(responsesBody)); tier != nil {
			result.ServiceTier = tier
		}
		if responsesReq.Reasoning != nil && responsesReq.Reasoning.Effort != "" {
			re := responsesReq.Reasoning.Effort
			result.ReasoningEffort = &re
		}
	}

	// Extract and save Codex usage snapshot from response headers (for OAuth accounts).
	// 排除 spark 影子:其 codex_* 仅由 QueryUsage(/wham/usage bengalfox)更新(外审第7轮 P1)。
	if handleErr == nil && account.Type == AccountTypeOAuth && !account.IsShadow() && account.Platform != PlatformGrok {
		if snapshot := ParseCodexRateLimitHeaders(resp.Header); snapshot != nil {
			s.updateCodexUsageSnapshot(ctx, account.ID, snapshot)
		}
	} else if handleErr == nil && account.IsShadow() && account.ParentAccountID != nil {
		notifyOpenAIAutoReset(*account.ParentAccountID)
	}

	return result, handleErr
}

func ensureCodexOAuthInstructionsField(reqBody map[string]any) {
	if reqBody == nil {
		return
	}
	if value, ok := reqBody["instructions"]; !ok || value == nil {
		reqBody["instructions"] = ""
		return
	}
	if _, ok := reqBody["instructions"].(string); !ok {
		reqBody["instructions"] = ""
	}
}

// handleAnthropicErrorResponse reads an upstream error and returns it in
// Anthropic error format.
func (s *OpenAIGatewayService) handleAnthropicErrorResponse(
	resp *http.Response,
	c *gin.Context,
	account *Account,
	requestedModel ...string,
) (*OpenAIForwardResult, error) {
	return s.handleCompatErrorResponse(resp, c, account, writeAnthropicError, requestedModel...)
}

// handleAnthropicBufferedStreamingResponse reads all Responses SSE events from
// the upstream streaming response, finds the terminal event (response.completed
// / response.incomplete / response.failed), converts the complete response to
// Anthropic Messages JSON format, and writes it to the client.
// This is used when the client requested stream=false but the upstream is always
// streaming.
func (s *OpenAIGatewayService) handleAnthropicBufferedStreamingResponse(
	resp *http.Response,
	c *gin.Context,
	account *Account,
	originalModel string,
	billingModel string,
	upstreamModel string,
	startTime time.Time,
) (*OpenAIForwardResult, error) {
	requestID := resp.Header.Get("x-request-id")

	finalResponse, usage, acc, err := s.readOpenAICompatBufferedTerminal(resp, c, "openai messages buffered", requestID)
	if err != nil {
		var gone *compatClientGoneError
		if errors.As(err, &gone) {
			// The client left and the upstream is closed. Bill what was produced and
			// sent; nothing is written to a client that is no longer there.
			base := &OpenAIForwardResult{
				RequestID:                     requestID,
				UpstreamHeaders:               resp.Header,
				Model:                         originalModel,
				BillingModel:                  billingModel,
				UpstreamModel:                 upstreamModel,
				UpstreamResponseModel:         observedUpstreamResponseModel(c),
				UpstreamResponseModelConflict: observedUpstreamResponseModelConflict(c),
				UpstreamResponseServiceTier:   observedUpstreamResponseServiceTier(c),
				Stream:                        false,
				Duration:                      time.Since(startTime),
			}
			if result := compatBufferedGoneResult(c, account, "messages_buffered", base, usage, acc, gone.cause); result != nil {
				return result, nil
			}
			err = gone.asReadError()
		}
		var readErr *openAICompatBufferedReadError
		if errors.As(err, &readErr) && readErr != nil {
			return nil, readErr.cause
		}
		return nil, err
	}

	if finalResponse == nil {
		writeAnthropicError(c, http.StatusBadGateway, "api_error", "Upstream stream ended without a terminal response event")
		return nil, fmt.Errorf("upstream stream ended without terminal event")
	}
	observer := upstreamResponseModelObserverFromContext(c)
	if observer == nil {
		observer = beginUpstreamResponseModelObservation(c)
	}
	observer.Observe(finalResponse.Model, true)
	observer.ObserveServiceTier(finalResponse.ServiceTier, true)

	if strings.TrimSpace(finalResponse.Status) == "failed" {
		if refusalErr := s.writeGrokModerationRefusalProtocolError(resp, c, account, openAICompatBufferedTerminalPayload(c, finalResponse)); refusalErr != nil {
			return nil, refusalErr
		}
		payload, _ := json.Marshal(gin.H{"type": "response.failed", "response": finalResponse})
		if hit, code, msg := detectOpenAICyberPolicy(payload); hit {
			MarkOpsCyberPolicy(c, CyberPolicyMark{
				Code:           code,
				Message:        msg,
				Body:           truncateString(string(payload), 4096),
				UpstreamStatus: http.StatusOK,
				UpstreamInTok:  usage.InputTokens,
				UpstreamOutTok: usage.OutputTokens,
			})
			clientMsg := msg
			if clientMsg == "" {
				clientMsg = "Request blocked by upstream cyber-security policy"
			}
			writeAnthropicError(c, http.StatusBadRequest, "invalid_request_error", clientMsg)
			return nil, fmt.Errorf("openai cyber_policy: %s", msg)
		}
		message := openAICompatFailedResponseMessage(finalResponse)
		if openAIStreamFailedEventShouldFailover(payload, message) {
			return nil, s.newOpenAIStreamFailoverErrorWithModel(c, account, false, requestID, payload, message, upstreamModel, resp.Header)
		}
		message = s.recordOpenAIStreamUpstreamError(c, account, false, requestID, "http_error", payload, message)
		// 统一走语义状态推断 + body 归一化（与 /v1/responses 路径一致），
		// 使按错误码配置的透传规则可命中。
		if status, errType, errMsg, matched := applyOpenAIStreamFailedErrorPassthroughRule(
			c, account.Platform, payload, message,
		); matched {
			if errMsg == "" {
				errMsg = message
			}
			MarkResponseCommitted(c)
			writeAnthropicError(c, status, errType, errMsg)
			return nil, fmt.Errorf("upstream response failed (passthrough): %s", errMsg)
		}
		writeAnthropicError(c, http.StatusBadGateway, "api_error", message)
		return nil, fmt.Errorf("upstream response failed: %s", message)
	}
	if strings.TrimSpace(finalResponse.Status) == "completed" {
		logOpenAISuccessMissingUsage(c.Request.Context(), c, account, resp, &usage, "response.completed", false)
	}

	// When the terminal event has an empty output array, reconstruct from
	// accumulated delta events so the client receives the full content.
	acc.SupplementResponseOutput(finalResponse)

	anthropicResp := apicompat.ResponsesToAnthropicWithOptions(finalResponse, originalModel, account.Platform == PlatformDeepseek)

	if s.responseHeaderFilter != nil {
		responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	}
	c.Header("Content-Type", "application/json; charset=utf-8")
	c.JSON(http.StatusOK, anthropicResp)

	result := &OpenAIForwardResult{
		RequestID:                     requestID,
		UpstreamHeaders:               resp.Header,
		ResponseID:                    finalResponse.ID,
		Usage:                         usage,
		Model:                         originalModel,
		BillingModel:                  billingModel,
		UpstreamModel:                 upstreamModel,
		UpstreamResponseModel:         observedUpstreamResponseModel(c),
		UpstreamResponseModelConflict: observedUpstreamResponseModelConflict(c),
		UpstreamResponseServiceTier:   observedUpstreamResponseServiceTier(c),
		Stream:                        false,
		Duration:                      time.Since(startTime),
	}
	// Grok /v1/messages uses Responses upstream; count native search for surcharge.
	if account != nil && account.IsGrok() && finalResponse != nil {
		if body, err := json.Marshal(finalResponse); err == nil {
			if n := countGrokNativeSearchCallsFromJSONBytes(body); n > 0 {
				result.SearchCount = n
			}
		}
	}
	return result, nil
}

func isOpenAICompatResponsesTerminalEvent(eventType string) bool {
	switch strings.TrimSpace(eventType) {
	case "response.completed", "response.done", "response.incomplete", "response.failed", "response.cancelled", "response.canceled", "error":
		return true
	default:
		return false
	}
}

func (s *OpenAIGatewayService) recordOpenAIMessagesStreamUpstreamError(c *gin.Context, account *Account, upstreamRequestID, kind, message string) {
	if c == nil {
		return
	}
	message = sanitizeUpstreamErrorMessage(message)
	setOpsUpstreamError(c, http.StatusBadGateway, message, "")
	event := OpsUpstreamErrorEvent{
		ProxyID:            opsUpstreamProxyID(account),
		ProxyName:          opsUpstreamProxyName(account),
		Platform:           PlatformOpenAI,
		UpstreamStatusCode: http.StatusBadGateway,
		UpstreamRequestID:  strings.TrimSpace(upstreamRequestID),
		Kind:               kind,
		Message:            message,
	}
	if account != nil {
		event.Platform = account.Platform
		event.AccountID = account.ID
		event.AccountName = account.Name
	}
	appendOpsUpstreamError(c, event)
}

func isOpenAICompatDoneSentinelLine(line string) bool {
	payload, ok := extractOpenAISSEDataLine(line)
	return ok && strings.TrimSpace(payload) == "[DONE]"
}

// openAICompatBufferedReadError 只标记错误发生在上游响应体读取阶段；
// 具体端点自行决定是否允许重放请求，避免共享读取器扩大重试范围。
type openAICompatBufferedReadError struct {
	cause error
}

func (e *openAICompatBufferedReadError) Error() string { return e.cause.Error() }
func (e *openAICompatBufferedReadError) Unwrap() error { return e.cause }

// compatClientGoneGrace bounds how long a reader keeps draining frames the scanner had
// already queued after the client left and the upstream body was closed. It is a safety
// net for bodies whose Close does not unblock a pending Read.
const compatClientGoneGrace = time.Second

// compatClientGoneError reports that the client left while the gateway was still
// reading the upstream. The upstream is already closed. cause is the read error the
// closed body produced, when there was one.
type compatClientGoneError struct {
	cause error
}

func (e *compatClientGoneError) Error() string {
	if e.cause != nil {
		return "client disconnected: " + e.cause.Error()
	}
	return "client disconnected before the upstream terminal event"
}

func (e *compatClientGoneError) Unwrap() error { return e.cause }

// asReadError is the error the buffered readers returned before they watched the
// client; it is what a caller falls back to when nothing is left to bill.
func (e *compatClientGoneError) asReadError() error {
	if e.cause != nil {
		return &openAICompatBufferedReadError{cause: e.cause}
	}
	return errors.New(e.Error())
}

// compatClientWatch closes the upstream body the moment the client's request ends.
// The compat gateways detach the upstream request from the client context so that a
// departed client does not cancel work that still has to be billed; that left them
// reading the upstream to the end. Closing the body at once stops paying for output
// nobody will read, and what was produced so far is billed from an estimate
// (applyCompatIncompleteUsage). Frames the scanner already queued are still processed,
// so a terminal frame that had arrived keeps its real usage.
type compatClientWatch struct {
	ctx      context.Context
	body     io.Closer
	gone     atomic.Bool
	stop     chan struct{}
	stopOnce sync.Once
}

// newCompatClientWatch starts watching c's request context. A request without a
// cancelable context yields an inert watch. Stop must be called when the handler is
// done with the body.
func newCompatClientWatch(c *gin.Context, body io.Closer) *compatClientWatch {
	w := &compatClientWatch{body: body, stop: make(chan struct{})}
	if c != nil && c.Request != nil {
		w.ctx = c.Request.Context()
	}
	if w.ctx == nil || w.ctx.Done() == nil {
		return w
	}
	go func() {
		select {
		case <-w.ctx.Done():
			// Stop may have raced the cancellation. Once stopped, the handler owns the
			// body and the watch no longer touches it.
			select {
			case <-w.stop:
			default:
				w.Disconnect()
			}
		case <-w.stop:
		}
	}()
	return w
}

// Disconnect marks the client gone and closes the upstream body. It is idempotent and
// safe to call while another goroutine is blocked reading the body.
func (w *compatClientWatch) Disconnect() {
	if w == nil || w.gone.Swap(true) {
		return
	}
	if w.body != nil {
		_ = w.body.Close()
	}
}

// Gone reports whether the client left, either through Disconnect or because its
// request context ended.
func (w *compatClientWatch) Gone() bool {
	if w == nil {
		return false
	}
	return w.gone.Load() || (w.ctx != nil && w.ctx.Err() != nil)
}

// Done is closed when the client's request ends; nil for an inert watch.
func (w *compatClientWatch) Done() <-chan struct{} {
	if w == nil || w.ctx == nil {
		return nil
	}
	return w.ctx.Done()
}

// Stop ends the watching goroutine. It does not close the body.
func (w *compatClientWatch) Stop() {
	if w == nil {
		return
	}
	w.stopOnce.Do(func() { close(w.stop) })
}

// wrapResponse returns a shallow copy of resp whose body reports a read error caused
// by the watch closing it as context.Canceled, which the shared scanners already
// treat as the client leaving instead of an upstream fault.
func (w *compatClientWatch) wrapResponse(resp *http.Response) *http.Response {
	clone := *resp
	clone.Body = &compatWatchedBody{ReadCloser: resp.Body, watch: w}
	return &clone
}

type compatWatchedBody struct {
	io.ReadCloser
	watch *compatClientWatch
}

func (b *compatWatchedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil && !errors.Is(err, io.EOF) && b.watch.Gone() {
		return n, context.Canceled
	}
	return n, err
}

// compatRequestStash is the request body the gateway sent upstream, kept on the gin
// context so that a stream handler can estimate the input of a request that ends
// without a usage report. It remembers the account so that a failover attempt through
// a path that stashes nothing never reads the previous attempt's body.
type compatRequestStash struct {
	accountID int64
	body      []byte
}

const compatUpstreamRequestBodyKey = "openai_compat_upstream_request_body"

func stashCompatUpstreamRequestBody(c *gin.Context, account *Account, body []byte) {
	if c == nil {
		return
	}
	stash := compatRequestStash{body: body}
	if account != nil {
		stash.accountID = account.ID
	}
	c.Set(compatUpstreamRequestBodyKey, stash)
}

func compatUpstreamRequestBodyFor(c *gin.Context, account *Account) (body []byte, accountID int64) {
	if c == nil {
		return nil, 0
	}
	value, ok := c.Get(compatUpstreamRequestBodyKey)
	if !ok {
		return nil, 0
	}
	stash, ok := value.(compatRequestStash)
	if !ok || (account != nil && stash.accountID != account.ID) {
		return nil, 0
	}
	return stash.body, stash.accountID
}

// compatIncompleteUsage describes a request that ended without a usage report.
type compatIncompleteUsage struct {
	Path           string
	Account        *Account
	Model          string
	RequestID      string
	ClientGone     bool
	Cause          error
	OutputEstimate int
	// ReplaceOutputUpTo: an output count at or below it is a placeholder the upstream
	// sent before any content (Anthropic reports 1 in message_start) and is replaced
	// by a larger estimate. 0 fills only an output count of zero.
	ReplaceOutputUpTo int
}

// applyCompatIncompleteUsage bills a request that ended without a usage report: the
// output the upstream really produced and the input that was really sent, both
// estimated, with 95% of the input priced as cache read. Usage the upstream reported
// is never overwritten or re-split. Replayable failover errors are skipped (the retry
// is billed on its own), and an upstream break with the client still attached and
// nothing produced stays free. The Warn log mirrors openai_usage.incomplete_stream_estimated.
func applyCompatIncompleteUsage(c *gin.Context, usage *OpenAIUsage, in compatIncompleteUsage) {
	if usage == nil {
		return
	}
	var failoverErr *UpstreamFailoverError
	if in.Cause != nil && errors.As(in.Cause, &failoverErr) {
		return
	}
	if !in.ClientGone && in.OutputEstimate <= 0 {
		return
	}
	filledOutput, filledInput := 0, 0
	if in.OutputEstimate > usage.OutputTokens && usage.OutputTokens <= in.ReplaceOutputUpTo && usage.ImageOutputTokens == 0 {
		usage.OutputTokens = in.OutputEstimate
		filledOutput = in.OutputEstimate
	}
	requestBody, stashedAccountID := compatUpstreamRequestBodyFor(c, in.Account)
	if usage.InputTokens == 0 && usage.CacheReadInputTokens == 0 &&
		usage.CacheCreationInputTokens == 0 && usage.ImageInputTokens == 0 {
		if estimated := estimateRequestInputTokens(requestBody); estimated > 0 {
			split := splitIncompleteInput(estimated)
			usage.InputTokens = split.Total
			usage.CacheReadInputTokens = split.CacheRead
			usage.CacheReadEstimated = true
			filledInput = split.Total
		}
	}
	if filledOutput == 0 && filledInput == 0 {
		return
	}
	accountID := stashedAccountID
	if in.Account != nil {
		accountID = in.Account.ID
	}
	logger.L().With(
		zap.String("component", "service.openai_gateway"),
		zap.String("path", in.Path),
		zap.Int64("account_id", accountID),
		zap.String("model", strings.TrimSpace(in.Model)),
		zap.String("request_id", in.RequestID),
		zap.Bool("client_disconnect", in.ClientGone),
		zap.Int("estimated_output_tokens", filledOutput),
		zap.Int("estimated_input_tokens", filledInput),
		zap.Int("estimated_cache_read_tokens", incompleteCacheReadTokens(filledInput)),
		zap.Error(in.Cause),
	).Warn("openai_usage.incomplete_stream_estimated")
}

// compatBufferedGoneResult builds the result of a buffered (plain JSON) request whose
// client left before the reply was complete. base carries the identity of the request
// and usage what the upstream reported before the body was closed. It returns nil when
// there is nothing to bill, and the caller keeps its former error handling.
func compatBufferedGoneResult(
	c *gin.Context,
	account *Account,
	path string,
	base *OpenAIForwardResult,
	usage OpenAIUsage,
	acc *apicompat.BufferedResponseAccumulator,
	cause error,
) *OpenAIForwardResult {
	base.Usage = usage
	base.ClientDisconnect = true
	applyCompatIncompleteUsage(c, &base.Usage, compatIncompleteUsage{
		Path: path, Account: account, Model: base.Model, RequestID: base.RequestID,
		ClientGone: true, Cause: cause, OutputEstimate: estimateIncompleteStreamOutputTokens(acc),
	})
	if !openAIForwardResultHasObservedBillingUnits(base) {
		return nil
	}
	return base
}

func openAICompatTerminalResponse(event *apicompat.ResponsesStreamEvent, payload []byte) *apicompat.ResponsesResponse {
	if event == nil {
		return nil
	}
	if event.Response != nil {
		return event.Response
	}
	switch strings.TrimSpace(event.Type) {
	case "response.failed", "error":
		message := extractOpenAISSEErrorMessage(payload)
		if message == "" {
			message = "Upstream response failed"
		}
		return &apicompat.ResponsesResponse{
			Status: "failed",
			Error:  &apicompat.ResponsesError{Code: event.Code, Message: message},
		}
	default:
		return nil
	}
}

func (s *OpenAIGatewayService) readOpenAICompatBufferedTerminal(
	resp *http.Response,
	c *gin.Context,
	logPrefix string,
	requestID string,
) (*apicompat.ResponsesResponse, OpenAIUsage, *apicompat.BufferedResponseAccumulator, error) {
	acc := apicompat.NewBufferedResponseAccumulator()
	var usage OpenAIUsage
	if c != nil {
		c.Set(openAICompatBufferedTerminalRawKey, []byte(nil))
	}
	if resp == nil || resp.Body == nil {
		return nil, usage, acc, errors.New("upstream response body is nil")
	}

	scanner := s.newUpstreamSSEScanner(resp.Body)

	// The client leaving closes the upstream at once. Frames already queued are still
	// processed, so a terminal frame that had arrived keeps its real usage.
	watch := newCompatClientWatch(c, resp.Body)
	defer watch.Stop()
	clientCancelCh := watch.Done()
	var graceTimer *time.Timer
	var graceCh <-chan time.Time
	defer func() {
		if graceTimer != nil {
			graceTimer.Stop()
		}
	}()

	streamInterval := time.Duration(0)
	if s.cfg != nil && s.cfg.Gateway.StreamDataIntervalTimeout > 0 {
		streamInterval = time.Duration(s.cfg.Gateway.StreamDataIntervalTimeout) * time.Second
	}
	var timeoutCh <-chan time.Time
	var timeoutTimer *time.Timer
	resetTimeout := func() {
		if streamInterval <= 0 {
			return
		}
		if timeoutTimer == nil {
			timeoutTimer = time.NewTimer(streamInterval)
			timeoutCh = timeoutTimer.C
			return
		}
		if !timeoutTimer.Stop() {
			select {
			case <-timeoutTimer.C:
			default:
			}
		}
		timeoutTimer.Reset(streamInterval)
	}
	stopTimeout := func() {
		if timeoutTimer == nil {
			return
		}
		if !timeoutTimer.Stop() {
			select {
			case <-timeoutTimer.C:
			default:
			}
		}
	}
	resetTimeout()
	defer stopTimeout()

	type scanEvent struct {
		line string
		err  error
	}
	events := make(chan scanEvent, 16)
	done := make(chan struct{})
	go func() {
		defer close(events)
		for scanner.Scan() {
			select {
			case events <- scanEvent{line: scanner.Text()}:
			case <-done:
				return
			}
		}
		if err := scanner.Err(); err != nil {
			select {
			case events <- scanEvent{err: err}:
			case <-done:
			}
		}
	}()
	defer close(done)

	var parser openAICompatSSEFrameParser
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				if frame, ok := parser.Finish(); ok {
					payload := openAICompatPayloadWithEventType(frame.Data, frame.EventType)
					payload = string(restoreCodexToolNamesFromContext(c, []byte(payload)))
					var event apicompat.ResponsesStreamEvent
					if err := json.Unmarshal([]byte(payload), &event); err == nil {
						s.parseSSEUsageBytesWithType([]byte(payload), event.Type, &usage)
						acc.ProcessEvent(&event)
						if response := openAICompatTerminalResponse(&event, []byte(payload)); isOpenAICompatResponsesTerminalEvent(event.Type) && response != nil {
							if c != nil {
								c.Set(openAICompatBufferedTerminalRawKey, []byte(frame.Data))
							}
							if event.Usage != nil {
								usage = copyOpenAIUsageFromResponsesUsage(event.Usage)
								if response.Usage == nil {
									response.Usage = event.Usage
								}
							}
							if response.Usage != nil {
								usage = copyOpenAIUsageFromResponsesUsage(response.Usage)
							}
							return response, usage, acc, nil
						}
					}
				}
				if watch.Gone() {
					return nil, usage, acc, &compatClientGoneError{}
				}
				return nil, usage, acc, nil
			}
			resetTimeout()
			if ev.err != nil {
				if watch.Gone() {
					return nil, usage, acc, &compatClientGoneError{cause: ev.err}
				}
				if !errors.Is(ev.err, context.Canceled) && !errors.Is(ev.err, context.DeadlineExceeded) {
					logger.L().Warn(logPrefix+": read error",
						zap.Error(ev.err),
						zap.String("request_id", requestID),
					)
				}
				return nil, usage, acc, &openAICompatBufferedReadError{cause: ev.err}
			}

			if isOpenAICompatDoneSentinelLine(ev.line) {
				if watch.Gone() {
					return nil, usage, acc, &compatClientGoneError{}
				}
				return nil, usage, acc, nil
			}
			frame, ok := parser.AddLine(ev.line)
			if !ok {
				continue
			}
			payload := openAICompatPayloadWithEventType(frame.Data, frame.EventType)
			payload = string(restoreCodexToolNamesFromContext(c, []byte(payload)))

			var event apicompat.ResponsesStreamEvent
			if err := json.Unmarshal([]byte(payload), &event); err != nil {
				logger.L().Warn(logPrefix+": failed to parse event",
					zap.Error(err),
					zap.String("request_id", requestID),
				)
				continue
			}
			s.parseSSEUsageBytesWithType([]byte(payload), event.Type, &usage)

			acc.ProcessEvent(&event)

			if response := openAICompatTerminalResponse(&event, []byte(payload)); isOpenAICompatResponsesTerminalEvent(event.Type) && response != nil {
				if c != nil {
					c.Set(openAICompatBufferedTerminalRawKey, []byte(frame.Data))
				}
				if event.Usage != nil {
					usage = copyOpenAIUsageFromResponsesUsage(event.Usage)
					if response.Usage == nil {
						response.Usage = event.Usage
					}
				}
				if response.Usage != nil {
					usage = copyOpenAIUsageFromResponsesUsage(response.Usage)
				}
				return response, usage, acc, nil
			}

		case <-clientCancelCh:
			clientCancelCh = nil
			watch.Disconnect()
			if graceTimer == nil {
				graceTimer = time.NewTimer(compatClientGoneGrace)
				graceCh = graceTimer.C
			}

		case <-graceCh:
			return nil, usage, acc, &compatClientGoneError{}

		case <-timeoutCh:
			_ = resp.Body.Close()
			if watch.Gone() {
				return nil, usage, acc, &compatClientGoneError{}
			}
			logger.L().Warn(logPrefix+": data interval timeout",
				zap.String("request_id", requestID),
				zap.Duration("interval", streamInterval),
			)
			return nil, usage, acc, fmt.Errorf("stream data interval timeout")
		}
	}
}

const openAICompatBufferedTerminalRawKey = "openai_compat_buffered_terminal_raw"

// ResponsesError intentionally models only portable code/message fields. Keep
// the provider terminal separately so adapters do not discard status_code or
// other error details before deciding whether a Grok refusal must be settled.
func openAICompatBufferedTerminalPayload(c *gin.Context, response *apicompat.ResponsesResponse) []byte {
	if c != nil {
		if raw, exists := c.Get(openAICompatBufferedTerminalRawKey); exists {
			if payload, ok := raw.([]byte); ok && len(payload) > 0 {
				return payload
			}
		}
	}
	payload, _ := json.Marshal(gin.H{"type": "response.failed", "response": response})
	return payload
}

// handleAnthropicStreamingResponse reads Responses SSE events from upstream,
// converts each to Anthropic SSE events, and writes them to the client.
// When StreamKeepaliveInterval is configured, it uses a goroutine + channel
// pattern to send Anthropic ping events during periods of upstream silence,
// preventing proxy/client timeout disconnections.
func (s *OpenAIGatewayService) handleAnthropicStreamingResponse(
	resp *http.Response,
	c *gin.Context,
	account *Account,
	originalModel string,
	billingModel string,
	upstreamModel string,
	startTime time.Time,
) (*OpenAIForwardResult, error) {
	requestID := resp.Header.Get("x-request-id")
	// 上游已返回 200 + SSE，允许 handler 的 pre-header keepalive 写下游。
	ArmOpenAIStreamSSEKeepalive(c)
	writeStreamHeaders := s.newStreamHeaderWriter(c, resp.Header)

	state := apicompat.NewResponsesEventToAnthropicState()
	state.RequireReasoningContent = account.Platform == PlatformDeepseek
	state.Model = originalModel
	var usage OpenAIUsage
	responseID := ""
	var firstTokenMs *int
	firstChunk := true
	clientDisconnected := false
	clientOutputStarted := false
	var streamFailoverErr error
	var streamNonFailoverErr error
	terminalEventType := ""
	searchCount := 0
	streamSearchSeen := make(map[string]struct{})
	countSearch := account != nil && account.IsGrok()
	measureResponsesTTFT := account != nil && (account.Platform == PlatformOpenAI || countSearch)
	ttftMode := OpenAITTFTModeSemantic
	if measureResponsesTTFT {
		ttftMode = s.openAITTFTMode(c.Request.Context())
	}
	nonBillableUpstreamError := false

	scanner := s.newUpstreamSSEScanner(resp.Body)

	// The client leaving (its request ends, or a write to it fails) closes the upstream
	// at once. Frames the scanner already queued are still processed, so a terminal
	// frame that had arrived keeps its real usage; otherwise the output produced so far
	// (streamAcc) and the input sent are billed from an estimate.
	watch := newCompatClientWatch(c, resp.Body)
	defer watch.Stop()
	streamAcc := apicompat.NewBufferedResponseAccumulator()
	clientCancelCh := watch.Done()
	var graceTimer *time.Timer
	var graceCh <-chan time.Time
	defer func() {
		if graceTimer != nil {
			graceTimer.Stop()
		}
	}()
	markGone := func() {
		clientDisconnected = true
		watch.Disconnect()
		if graceTimer == nil {
			graceTimer = time.NewTimer(compatClientGoneGrace)
			graceCh = graceTimer.C
		}
	}

	streamInterval := time.Duration(0)
	if s.cfg != nil && s.cfg.Gateway.StreamDataIntervalTimeout > 0 {
		streamInterval = time.Duration(s.cfg.Gateway.StreamDataIntervalTimeout) * time.Second
	}
	var intervalTicker *time.Ticker
	if streamInterval > 0 {
		intervalTicker = time.NewTicker(streamInterval)
		defer intervalTicker.Stop()
	}
	var intervalCh <-chan time.Time
	if intervalTicker != nil {
		intervalCh = intervalTicker.C
	}
	observer := upstreamResponseModelObserverFromContext(c)
	if observer == nil {
		observer = beginUpstreamResponseModelObservation(c)
	}

	// resultWithUsage builds the final result snapshot.
	resultWithUsage := func() *OpenAIForwardResult {
		out := &OpenAIForwardResult{
			RequestID:                     requestID,
			UpstreamHeaders:               resp.Header,
			ResponseID:                    responseID,
			Usage:                         usage,
			Model:                         originalModel,
			BillingModel:                  billingModel,
			UpstreamModel:                 upstreamModel,
			UpstreamResponseModel:         observedUpstreamResponseModel(c),
			UpstreamResponseModelConflict: observedUpstreamResponseModelConflict(c),
			UpstreamResponseServiceTier:   observedUpstreamResponseServiceTier(c),
			Stream:                        true,
			Duration:                      time.Since(startTime),
			FirstTokenMs:                  firstTokenMs,
			ClientDisconnect:              clientDisconnected || watch.Gone(),
			NonBillableUpstreamError:      nonBillableUpstreamError,
		}
		if searchCount > 0 {
			out.SearchCount = searchCount
		}
		return out
	}

	// processDataLine handles a single "data: ..." SSE line from upstream.
	processDataLine := func(payload string) bool {
		rawPayload := []byte(payload)
		payload = string(restoreCodexToolNamesFromContext(c, []byte(payload)))
		if firstChunk && (!measureResponsesTTFT || (gjson.Valid(payload) && openAIStreamDataStartsTTFT(payload, "", false, ttftMode))) {
			firstChunk = false
			ms := int(time.Since(startTime).Milliseconds())
			firstTokenMs = &ms
		}
		if countSearch {
			searchCount += countGrokNativeSearchCallsInSSEDataDedup([]byte(payload), streamSearchSeen)
		}

		var event apicompat.ResponsesStreamEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			logger.L().Warn("openai messages stream: failed to parse event",
				zap.Error(err),
				zap.String("request_id", requestID),
			)
			return false
		}
		observer.ObserveOpenAI([]byte(payload), event.Type)
		s.parseSSEUsageBytesWithType([]byte(payload), event.Type, &usage)
		streamAcc.ProcessEvent(&event)

		eventType := strings.TrimSpace(event.Type)
		isBareErrorEvent := eventType == "error"
		isTerminalEvent := isOpenAICompatResponsesTerminalEvent(eventType) || isBareErrorEvent
		if isTerminalEvent {
			terminalEventType = eventType
			if event.Response != nil {
				if id := strings.TrimSpace(event.Response.ID); id != "" {
					responseID = id
				}
				if event.Response.Usage != nil {
					usage = copyOpenAIUsageFromResponsesUsage(event.Response.Usage)
				}
			}
			if event.Usage != nil {
				usage = copyOpenAIUsageFromResponsesUsage(event.Usage)
			}
			// cyber_policy 致命不可重试：标记供 handler 事后记录；以 Anthropic SSE error 事件
			// 回写让客户端感知并停止重试（F4），丢弃后续转换输出。
			if eventType == "response.failed" || isBareErrorEvent {
				payloadBytes := []byte(payload)
				if refusalErr := grokModerationRefusalProtocolError(account, rawPayload); refusalErr != nil {
					if !clientDisconnected {
						_ = s.writeGrokModerationRefusalProtocolError(resp, c, account, rawPayload)
					}
					streamNonFailoverErr = refusalErr
					return true
				}
				if hit, code, msg := detectOpenAICyberPolicy(payloadBytes); hit {
					MarkOpsCyberPolicy(c, CyberPolicyMark{
						Code:           code,
						Message:        msg,
						Body:           truncateString(payload, 4096),
						UpstreamStatus: http.StatusOK,
						UpstreamInTok:  usage.InputTokens,
						UpstreamOutTok: usage.OutputTokens,
					})
					if !clientDisconnected {
						writeStreamHeaders()
						clientMsg := msg
						if clientMsg == "" {
							clientMsg = "Request blocked by upstream cyber-security policy"
						}
						if _, err := fmt.Fprint(c.Writer, buildAnthropicStreamErrorSSE("invalid_request_error", clientMsg)); err == nil {
							c.Writer.Flush()
						}
						clientDisconnected = true
					}
					return true
				}
				message := extractOpenAISSEErrorMessage(payloadBytes)
				if !clientOutputStarted && isOpenAINonBillableRequestError(message, payloadBytes) {
					nonBillableUpstreamError = true
				}
				// Once Anthropic output has started, switching accounts would splice
				// two model streams together. Surface a proper Anthropic error event
				// instead of returning a failover error that the handler cannot retry.
				shouldFailover := openAIStreamFailedEventShouldFailover(payloadBytes, message)
				if isBareErrorEvent {
					shouldFailover = openAIStreamErrorEventShouldFailover(payloadBytes, message)
				}
				if !clientOutputStarted && shouldFailover {
					streamFailoverErr = s.newOpenAIStreamFailoverErrorWithModel(c, account, false, requestID, payloadBytes, message, upstreamModel, resp.Header)
					return true
				}
				message = s.recordOpenAIStreamUpstreamError(c, account, false, requestID, "http_error", payloadBytes, message)
				errStatus, errType, errMsg := http.StatusBadGateway, "api_error", message
				// 统一走语义状态推断 + body 归一化（与 /v1/responses 路径一致），
				// 使按错误码配置的透传规则可命中。
				if status, et, em, matched := applyOpenAIStreamFailedErrorPassthroughRule(
					c, account.Platform, payloadBytes, message,
				); matched {
					if em == "" {
						em = errMsg
					}
					errStatus, errType, errMsg = status, et, em
					MarkResponseCommitted(c)
				}
				if !clientDisconnected {
					if !clientOutputStarted {
						writeAnthropicError(c, errStatus, errType, errMsg)
						clientOutputStarted = true
					} else {
						writeStreamHeaders()
						if _, err := fmt.Fprint(c.Writer, buildAnthropicStreamErrorSSE(errType, errMsg)); err == nil {
							c.Writer.Flush()
						}
					}
				}
				streamNonFailoverErr = fmt.Errorf("upstream response failed: %s", errMsg)
				return true
			}
		}

		// Convert to Anthropic events
		events := apicompat.ResponsesEventToAnthropicEvents(&event, state)
		if !clientDisconnected {
			for _, evt := range events {
				sse, err := apicompat.ResponsesAnthropicEventToSSE(evt)
				if err != nil {
					logger.L().Warn("openai messages stream: failed to marshal event",
						zap.Error(err),
						zap.String("request_id", requestID),
					)
					continue
				}
				writeStreamHeaders()
				if _, err := fmt.Fprint(c.Writer, sse); err != nil {
					clientDisconnected = true
					logger.L().Info("openai messages stream: client disconnected, continuing to drain upstream for billing",
						zap.String("request_id", requestID),
					)
					break
				}
				clientOutputStarted = true
			}
		}
		if len(events) > 0 && !clientDisconnected {
			c.Writer.Flush()
		}
		return isTerminalEvent
	}

	// finalizeStream sends any remaining Anthropic events and returns the result.
	finalizeStream := func() (*OpenAIForwardResult, error) {
		if streamFailoverErr != nil {
			return resultWithUsage(), streamFailoverErr
		}
		if streamNonFailoverErr != nil {
			return resultWithUsage(), streamNonFailoverErr
		}
		if finalEvents := apicompat.FinalizeResponsesAnthropicStream(state); len(finalEvents) > 0 && !clientDisconnected {
			for _, evt := range finalEvents {
				sse, err := apicompat.ResponsesAnthropicEventToSSE(evt)
				if err != nil {
					continue
				}
				writeStreamHeaders()
				if _, err := fmt.Fprint(c.Writer, sse); err != nil {
					clientDisconnected = true
					logger.L().Info("openai messages stream: client disconnected during final flush",
						zap.String("request_id", requestID),
					)
					break
				}
				clientOutputStarted = true
			}
			if !clientDisconnected {
				c.Writer.Flush()
			}
		}
		logOpenAISuccessMissingUsage(c.Request.Context(), c, account, resp, &usage, terminalEventType, clientDisconnected)
		return resultWithUsage(), nil
	}

	// handleScanErr logs scanner errors if meaningful.
	// finishIncomplete ends a stream that has no terminal frame. When the client is
	// gone the request is complete from the gateway's side: what the upstream produced
	// and what was sent is billed from an estimate and no error is returned, so the
	// handler neither fails over nor counts the account as faulty. With the client
	// still attached the cause is returned as before, and output already produced is
	// still billed.
	finishIncomplete := func(cause error) (*OpenAIForwardResult, error) {
		gone := clientDisconnected || watch.Gone()
		applyCompatIncompleteUsage(c, &usage, compatIncompleteUsage{
			Path: "messages_stream", Account: account, Model: originalModel, RequestID: requestID,
			ClientGone: gone, Cause: cause, OutputEstimate: estimateIncompleteStreamOutputTokens(streamAcc),
		})
		result := resultWithUsage()
		if gone {
			return result, nil
		}
		return result, cause
	}
	// handleScanErr logs scanner errors if meaningful.
	handleScanErr := func(err error) {
		if err != nil && !watch.Gone() && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			logger.L().Warn("openai messages stream: read error",
				zap.Error(err),
				zap.String("request_id", requestID),
			)
		}
	}
	missingTerminalErr := func() (*OpenAIForwardResult, error) {
		if clientDisconnected || watch.Gone() {
			return finishIncomplete(fmt.Errorf("stream usage incomplete: missing terminal event"))
		}
		message := "OpenAI messages stream ended before a terminal event"
		if !clientOutputStarted {
			// Nothing reached the client yet: a replayable attempt, never estimated.
			return resultWithUsage(), s.newOpenAIStreamFailoverErrorWithModel(c, account, false, requestID, nil, message, upstreamModel)
		}
		s.recordOpenAIMessagesStreamUpstreamError(c, account, requestID, "stream_missing_terminal", message)
		return finishIncomplete(fmt.Errorf("stream usage incomplete: missing terminal event"))
	}
	processFrame := func(frame openAICompatSSEFrame) bool {
		payload := openAICompatPayloadWithEventType(frame.Data, frame.EventType)
		terminal := processDataLine(payload)
		if clientDisconnected {
			// A write to the client failed somewhere in the frame: stop the upstream.
			markGone()
		}
		return terminal
	}

	// ── Determine keepalive interval ──
	keepaliveInterval := time.Duration(0)
	if s.cfg != nil && s.cfg.Gateway.StreamKeepaliveInterval > 0 {
		keepaliveInterval = time.Duration(s.cfg.Gateway.StreamKeepaliveInterval) * time.Second
	}

	// ── No keepalive: fast synchronous path (no goroutine overhead) ──
	if streamInterval <= 0 && keepaliveInterval <= 0 {
		var parser openAICompatSSEFrameParser
		for scanner.Scan() {
			line := scanner.Text()
			if isOpenAICompatDoneSentinelLine(line) {
				return missingTerminalErr()
			}
			frame, ok := parser.AddLine(line)
			if !ok {
				continue
			}
			if processFrame(frame) {
				return finalizeStream()
			}
		}
		if err := scanner.Err(); err != nil {
			handleScanErr(err)
			return finishIncomplete(fmt.Errorf("stream usage incomplete: %w", err))
		}
		if frame, ok := parser.Finish(); ok {
			if strings.TrimSpace(frame.Data) == "[DONE]" {
				return missingTerminalErr()
			}
			if processFrame(frame) {
				return finalizeStream()
			}
		}
		return missingTerminalErr()
	}

	// ── With keepalive: goroutine + channel + select ──
	type scanEvent struct {
		line string
		err  error
	}
	events := make(chan scanEvent, 16)
	done := make(chan struct{})
	var lastReadAt int64
	atomic.StoreInt64(&lastReadAt, time.Now().UnixNano())
	sendEvent := func(ev scanEvent) bool {
		select {
		case events <- ev:
			return true
		case <-done:
			return false
		}
	}
	go func() {
		defer close(events)
		for scanner.Scan() {
			atomic.StoreInt64(&lastReadAt, time.Now().UnixNano())
			if !sendEvent(scanEvent{line: scanner.Text()}) {
				return
			}
		}
		if err := scanner.Err(); err != nil {
			_ = sendEvent(scanEvent{err: err})
		}
	}()
	defer close(done)

	var keepaliveTicker *time.Ticker
	if keepaliveInterval > 0 {
		keepaliveTicker = time.NewTicker(keepaliveInterval)
		defer keepaliveTicker.Stop()
	}
	var keepaliveCh <-chan time.Time
	if keepaliveTicker != nil {
		keepaliveCh = keepaliveTicker.C
	}
	lastDataAt := time.Now()
	var parser openAICompatSSEFrameParser

	for {
		select {
		case ev, ok := <-events:
			if !ok {
				// Upstream closed
				if frame, ok := parser.Finish(); ok {
					if strings.TrimSpace(frame.Data) == "[DONE]" {
						return missingTerminalErr()
					}
					if processFrame(frame) {
						return finalizeStream()
					}
				}
				return missingTerminalErr()
			}
			if ev.err != nil {
				handleScanErr(ev.err)
				return finishIncomplete(fmt.Errorf("stream usage incomplete: %w", ev.err))
			}
			lastDataAt = time.Now()
			line := ev.line
			if isOpenAICompatDoneSentinelLine(line) {
				return missingTerminalErr()
			}
			frame, ok := parser.AddLine(line)
			if !ok {
				continue
			}
			if processFrame(frame) {
				return finalizeStream()
			}

		case <-clientCancelCh:
			clientCancelCh = nil
			markGone()

		case <-graceCh:
			return finishIncomplete(fmt.Errorf("stream usage incomplete after disconnect"))

		case <-intervalCh:
			lastRead := time.Unix(0, atomic.LoadInt64(&lastReadAt))
			if time.Since(lastRead) < streamInterval {
				continue
			}
			if clientDisconnected || watch.Gone() {
				return finishIncomplete(fmt.Errorf("stream usage incomplete after timeout"))
			}
			logger.L().Warn("openai messages stream: data interval timeout",
				zap.String("request_id", requestID),
				zap.String("model", originalModel),
				zap.Duration("interval", streamInterval),
			)
			return finishIncomplete(fmt.Errorf("stream data interval timeout"))

		case <-keepaliveCh:
			if clientDisconnected {
				continue
			}
			if time.Since(lastDataAt) < keepaliveInterval {
				continue
			}
			// Send Anthropic-format ping event
			writeStreamHeaders()
			if _, err := fmt.Fprint(c.Writer, "event: ping\ndata: {\"type\":\"ping\"}\n\n"); err != nil {
				// Client disconnected
				logger.L().Info("openai messages stream: client disconnected during keepalive",
					zap.String("request_id", requestID),
				)
				markGone()
				continue
			}
			clientOutputStarted = true
			c.Writer.Flush()
		}
	}
}

// writeAnthropicError writes an error response in Anthropic Messages API format.
func writeAnthropicError(c *gin.Context, statusCode int, errType, message string) {
	if statusCode == http.StatusBadGateway && message == UpstreamUnavailableMessage {
		WriteUpstreamFinancialError(c, http.StatusPaymentRequired, nil)
		return
	}
	c.JSON(statusCode, gin.H{
		"type": "error",
		"error": gin.H{
			"type":    errType,
			"message": message,
		},
	})
}

// buildAnthropicStreamErrorSSE builds one Anthropic SSE `error` event so a
// streaming response can terminate with a visible error (e.g. upstream
// cyber_policy) and programmatic clients stop retrying.
// Marshal 失败的兜底仅保留固定提示。
func buildAnthropicStreamErrorSSE(errType, message string) string {
	payload, err := json.Marshal(gin.H{
		"type": "error",
		"error": gin.H{
			"type":    errType,
			"message": message,
		},
	})
	if err != nil {
		return "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"" + errType + "\",\"message\":\"upstream error\"}}\n\n"
	}
	return "event: error\ndata: " + string(payload) + "\n\n"
}

func copyOpenAIUsageFromResponsesUsage(usage *apicompat.ResponsesUsage) OpenAIUsage {
	if usage == nil {
		return OpenAIUsage{}
	}
	result := OpenAIUsage{
		InputTokens:              usage.InputTokens,
		OutputTokens:             usage.OutputTokens,
		CacheCreationInputTokens: usage.CacheCreationInputTokens,
	}
	if usage.InputTokensDetails != nil {
		result.CacheReadInputTokens = usage.InputTokensDetails.CachedTokens
	}
	return result
}
