package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

var (
	grokRealtimePreauthTopUpInterval = 10 * time.Second
	grokRealtimePreauthTopUpLead     = 15 * time.Second
	grokRealtimeNow                  = time.Now
)

func resolveConcurrencyUserSubject(c *gin.Context, apiKey *service.APIKey) (userID int64, concurrency int) {
	if subject, ok := middleware2.GetAuthSubjectFromContext(c); ok {
		userID = subject.UserID
		concurrency = subject.Concurrency
	}
	if apiKey != nil {
		if userID <= 0 {
			userID = apiKey.UserID
		}
		if userID <= 0 && apiKey.User != nil {
			userID = apiKey.User.ID
		}
		if concurrency <= 0 && apiKey.User != nil {
			concurrency = apiKey.User.Concurrency
		}
	}
	return userID, concurrency
}

// GrokRealtime exposes xAI's native Voice Realtime WebSocket.
// Only Grok-platform API keys may use this endpoint.
func (h *OpenAIGatewayHandler) GrokRealtime(c *gin.Context) {
	if c == nil || c.Request == nil || !isOpenAIWSUpgradeRequest(c.Request) {
		h.errorResponse(c, http.StatusUpgradeRequired, "invalid_request_error", "WebSocket upgrade required (Upgrade: websocket)")
		return
	}
	c.Request = c.Request.WithContext(h.gatewayService.PrepareSchedulerRequestContext(c.Request.Context()))
	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok || apiKey.Group == nil || apiKey.Group.Platform != service.PlatformGrok {
		h.errorResponse(c, http.StatusNotFound, "not_found_error", "Realtime API is not supported for this platform")
		return
	}
	reqLog := requestLogger(c, "handler.openai_gateway.grok_realtime")
	if !h.ensureResponsesDependencies(c, reqLog) {
		return
	}
	userID, userConcurrency := resolveConcurrencyUserSubject(c, apiKey)
	if userID > 0 {
		var streamStarted bool
		userRelease, acquired := h.acquireResponsesUserSlot(c, userID, userConcurrency, false, &streamStarted, reqLog)
		if !acquired {
			return
		}
		if userRelease != nil {
			defer userRelease()
		}
	} else {
		keyRelease, keyErr := h.concurrencyHelper.AcquireAPIKeySlot(c.Request.Context(), apiKey.ID, apiKey.ConcurrencyLimit)
		if keyErr != nil {
			h.handleConcurrencyError(c, keyErr, "API key", false)
			return
		}
		if keyRelease != nil {
			defer keyRelease()
		}
	}
	subscription, _ := middleware2.GetSubscriptionFromContext(c)
	if err := h.billingCacheService.CheckBillingEligibility(c.Request.Context(), apiKey.User, apiKey, apiKey.Group, subscription, service.QuotaPlatform(c.Request.Context(), apiKey)); err != nil {
		status, code, message, retryAfter := billingErrorDetails(err)
		if retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		h.errorResponse(c, status, code, message)
		return
	}

	model := c.Query("model")
	if strings.TrimSpace(model) == "" {
		model = "grok-voice-latest"
	}
	// 余额模式在途预留（与计费同口径估算；计费任务扣减余额缓存后才释放）。
	inflightDone, inflightErr := reserveInflightBalance(c, h.billingCacheService, h.gatewayService, apiKey, subscription, service.InflightEstimateRequest{Model: model, Kind: service.InflightEstimateAudio, AudioMode: "realtime", AudioUnits: 1})
	if inflightErr != nil {
		status, code, message, retryAfter := billingErrorDetails(inflightErr)
		if retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		h.errorResponse(c, status, code, message)
		return
	}
	defer inflightDone()

	realtimeRequestID := service.StableGrokRealtimeBillingRequestID("")
	estimateRealtimeHold := func(routeCtx context.Context, key *service.APIKey) (float64, error) {
		return h.gatewayService.EstimateAudioPreauthorizationCost(routeCtx, key, model, "realtime", 1.0), nil
	}
	initialRealtimeHold, _ := estimateRealtimeHold(c.Request.Context(), apiKey)
	balanceGuard, preauthErr := preauthorizeFixedAmountGatewayRequest(
		c.Request.Context(), h.balancePreauthorizer, apiKey, subscription, realtimeRequestID, []byte(model), initialRealtimeHold,
	)
	if h.handlePreauthorizationError(c, preauthErr, false) {
		return
	}
	defer func() { deferBalancePreauthorizationRefund(reqLog, balanceGuard) }()
	if balanceGuard != nil {
		c.Request = c.Request.WithContext(service.ContextWithBalancePreauthorizationGuard(c.Request.Context(), balanceGuard))
	}

	// Keep the HTTP response uncommitted while selecting and probing an account.
	// Realtime is not an HTTP streaming response; using reqStream=true here would
	// let the wait queue flush an SSE ping before the WebSocket handshake succeeds.
	failed := map[int64]struct{}{}
	profitVetoCount := 0
	accountSlotBusySeen := false
	var selection *service.AccountSelectionResult
	var release func()
	var token string
	var upstream *service.GrokRealtimeUpstream
	var candidateSeen bool
	for attempts := 0; attempts < 4; attempts++ {
		// Realtime's voice model is not a text-model capability. Passing a
		// concrete text model here would reject accounts mapped only to an
		// older/default text model before the upstream handshake can decide.
		// An empty requested model keeps account selection capability-based;
		// the actual voice model remains in the upstream WS query below.
		candidate, _, routedKey, selectErr := h.gatewayService.SelectAccountWithSchedulerForCapabilityAlongKeyRoutes(
			c.Request.Context(), apiKey, "", "", "", failed,
			service.OpenAIUpstreamTransportHTTPSSE,
			service.OpenAIEndpointCapabilityChatCompletions,
			false, false, false, service.PlatformGrok,
		)
		if selectErr == nil && routedKey != nil {
			if bindErr := h.bindSelectedKeyRoute(c, keyRouteBinding{
				Previous: apiKey, Selected: routedKey, Subscription: &subscription,
				Guard: &balanceGuard, Body: []byte(model), Model: model,
				FixedRequestID: realtimeRequestID, FixedAmount: estimateRealtimeHold,
			}); bindErr != nil {
				releaseRejectedKeyRouteSelection(candidate)
				h.handlePreauthorizationError(c, bindErr, false)
				return
			}
			apiKey = routedKey
		}
		if selectErr != nil || candidate == nil || candidate.Account == nil {
			break
		}
		candidateSeen = true
		account := candidate.Account
		var streamStarted bool
		var slotStatus openAISlotAcquireResult
		release, slotStatus = h.acquireResponsesAccountSlot(c, apiKey.GroupID, "", candidate, false, &streamStarted, reqLog)
		switch h.openAISlotLoopAction(c, slotStatus, account.ID, apiKey.GroupID, "", failed, &profitVetoCount, streamStarted, reqLog, &accountSlotBusySeen) {
		case openAISlotLoopStop:
			return
		case openAISlotLoopRotate:
			continue
		}
		var credErr error
		token, _, credErr = h.gatewayService.GetRequestCredential(c.Request.Context(), c, account)
		if credErr != nil {
			release()
			release = nil
			failed[account.ID] = struct{}{}
			continue
		}
		probeCtx, cancelProbe := context.WithTimeout(c.Request.Context(), service.DefaultGrokRealtimeDialTimeout)
		candidateUpstream, openErr := h.gatewayService.OpenGrokRealtime(probeCtx, account, token, model)
		cancelProbe()
		if openErr != nil {
			reqLog.Warn("grok_realtime.pre_accept_failed", zap.Int64("account_id", account.ID), zap.Error(openErr))
			statusCode := http.StatusBadGateway
			var dialErr *service.GrokRealtimeDialError
			if errors.As(openErr, &dialErr) && dialErr.StatusCode > 0 {
				statusCode = dialErr.StatusCode
			}
			h.gatewayService.HandleGrokRealtimeUpstreamError(c.Request.Context(), account, statusCode, []byte(openErr.Error()))
			release()
			release = nil
			failed[account.ID] = struct{}{}
			continue
		}
		selection, upstream = candidate, candidateUpstream
		break
	}
	if selection == nil || selection.Account == nil || release == nil || upstream == nil {
		if !candidateSeen {
			h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "No available Grok accounts")
		} else if accountSlotBusySeen {
			h.handleOpenAIAccountSlotBusyExhausted(c, false, reqLog)
		} else {
			h.errorResponse(c, http.StatusBadGateway, "upstream_error", "Grok realtime upstream unavailable")
		}
		return
	}
	defer release()
	defer func() { _ = upstream.Close() }()

	conn, err := coderws.Accept(c.Writer, c.Request, &coderws.AcceptOptions{CompressionMode: coderws.CompressionContextTakeover})
	if err != nil {
		return
	}
	defer func() { _ = conn.CloseNow() }()

	started := grokRealtimeNow()
	proxyCtx, proxyCancel := context.WithCancel(c.Request.Context())
	var topUpAbortErr atomic.Pointer[error]
	topUpDone := make(chan struct{})
	perMinCost := h.gatewayService.EstimateAudioPreauthorizationCost(c.Request.Context(), apiKey, model, "realtime", 1.0)
	if balanceGuard != nil &&
		service.BalancePreauthorizationBillingType(apiKey, subscription) == service.BillingTypeBalance &&
		perMinCost > 0 &&
		grokRealtimePreauthTopUpInterval > 0 {
		go func() {
			defer close(topUpDone)
			ticker := time.NewTicker(grokRealtimePreauthTopUpInterval)
			defer ticker.Stop()
			lead := grokRealtimePreauthTopUpLead
			if lead < 0 {
				lead = 0
			}
			for {
				select {
				case <-proxyCtx.Done():
					return
				case <-ticker.C:
					if proxyCtx.Err() != nil {
						return
					}
					effectiveElapsed := grokRealtimeNow().Sub(started) + lead
					if effectiveElapsed < 0 {
						effectiveElapsed = 0
					}
					neededMinutes := math.Max(1.0, math.Floor(effectiveElapsed.Minutes())+1.0)
					target := perMinCost * neededMinutes
					if target <= balanceGuard.HoldAmount() {
						continue
					}
					topUpCtx, cancelTopUp := context.WithTimeout(context.WithoutCancel(proxyCtx), 5*time.Second)
					err := balanceGuard.TopUpFixedAmount(topUpCtx, target)
					cancelTopUp()
					if err != nil {
						if proxyCtx.Err() != nil {
							return
						}
						errCopy := err
						topUpAbortErr.Store(&errCopy)
						// Send the explicit WebSocket Close frame BEFORE canceling proxyCtx;
						// canceling the context of an active coder/websocket Read tears down
						// the underlying TCP connection immediately and delivers raw EOF.
						closeStatus, closeReason := openAIWSPreauthorizationCloseStatusAndReason(err)
						_ = conn.Close(closeStatus, closeReason)
						proxyCancel()
						return
					}
				}
			}
		}()
	} else {
		close(topUpDone)
	}
	billableObserved, proxyErr := h.gatewayService.ProxyGrokRealtimeConn(proxyCtx, c, conn, upstream, model)
	elapsed := grokRealtimeNow().Sub(started)
	proxyCancel()
	<-topUpDone
	if abortErrPtr := topUpAbortErr.Load(); abortErrPtr != nil && *abortErrPtr != nil {
		reqLog.Info("grok_realtime.preauth_topup_aborted", zap.Error(*abortErrPtr))
		closeStatus, closeReason := openAIWSPreauthorizationCloseStatusAndReason(*abortErrPtr)
		_ = conn.Close(closeStatus, closeReason)
	} else if proxyErr != nil {
		reqLog.Info("grok_realtime.proxy_failed", zap.Error(proxyErr))
		if !isExpectedGrokRealtimeClose(proxyErr) {
			_ = conn.Close(coderws.StatusInternalError, "upstream realtime websocket failed")
		}
	}
	if result := grokRealtimeBillingResult(model, elapsed, billableObserved); result != nil {
		if realtimeRequestID != "" {
			result.RequestID = service.StableGrokRealtimeBillingRequestID(realtimeRequestID)
		}
		h.recordGrokVoiceUsage(c, apiKey, selection.Account, subscription, "realtime", nil, result)
	}
}

func grokRealtimeBillingResult(model string, elapsed time.Duration, audioObserved bool) *service.OpenAIForwardResult {
	if !audioObserved || elapsed <= 0 {
		return nil
	}
	return &service.OpenAIForwardResult{
		RequestID:  service.StableGrokRealtimeBillingRequestID(""),
		Model:      model,
		Duration:   elapsed,
		AudioUsage: &service.AudioUsage{Mode: "realtime", DurationOrUnits: elapsed.Minutes()},
	}
}

func isExpectedGrokRealtimeClose(err error) bool {
	if err == nil {
		return true
	}
	switch coderws.CloseStatus(err) {
	case coderws.StatusNormalClosure, coderws.StatusGoingAway,
		coderws.StatusNoStatusRcvd, coderws.StatusAbnormalClosure:
		return true
	default:
		return false
	}
}

// GrokVoice handles xAI Voice HTTP endpoints. endpoint is "tts", "stt", or "custom-voices".
func (h *OpenAIGatewayHandler) GrokVoice(c *gin.Context, endpoint string) {
	c.Request = c.Request.WithContext(h.gatewayService.PrepareSchedulerRequestContext(c.Request.Context()))
	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok || apiKey.Group == nil || apiKey.Group.Platform != service.PlatformGrok {
		h.errorResponse(c, http.StatusNotFound, "not_found_error", "Voice API is not supported for this platform")
		return
	}
	reqLog := requestLogger(c, "handler.openai_gateway.grok_voice", zap.String("endpoint", endpoint))
	if !h.ensureResponsesDependencies(c, reqLog) {
		return
	}
	userID, userConcurrency := resolveConcurrencyUserSubject(c, apiKey)
	if userID > 0 {
		var streamStarted bool
		userRelease, acquired := h.acquireResponsesUserSlot(c, userID, userConcurrency, false, &streamStarted, reqLog)
		if !acquired {
			return
		}
		if userRelease != nil {
			defer userRelease()
		}
	} else {
		keyRelease, keyErr := h.concurrencyHelper.AcquireAPIKeySlot(c.Request.Context(), apiKey.ID, apiKey.ConcurrencyLimit)
		if keyErr != nil {
			h.handleConcurrencyError(c, keyErr, "API key", false)
			return
		}
		if keyRelease != nil {
			defer keyRelease()
		}
	}
	subscription, _ := middleware2.GetSubscriptionFromContext(c)
	if err := h.billingCacheService.CheckBillingEligibility(c.Request.Context(), apiKey.User, apiKey, apiKey.Group, subscription, service.QuotaPlatform(c.Request.Context(), apiKey)); err != nil {
		status, code, message, retryAfter := billingErrorDetails(err)
		if retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		h.errorResponse(c, status, code, message)
		return
	}

	body, err := readGrokVoiceGatewayBody(c)
	if err != nil {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	if endpoint == "tts" {
		subject, _ := middleware2.GetAuthSubjectFromContext(c)
		// TTS bodies use {"input":"..."} (and variants). Normalize to chat messages so
		// content moderation extractors see the spoken text.
		auditBody := body
		if input := extractGrokTTSInputText(body); input != "" {
			if b, err := json.Marshal(map[string]any{
				"messages": []map[string]any{{"role": "user", "content": input}},
			}); err == nil {
				auditBody = b
			}
		}
		if decision := h.checkSecurityAudit(c, reqLog, apiKey, subject, service.ContentModerationProtocolOpenAIChat, "grok-4.5", auditBody); decision != nil && !decision.AllowNextStage {
			h.openAISecurityAuditError(c, decision)
			return
		}
	}
	// 余额模式在途预留（与计费同口径估算；计费任务扣减余额缓存后才释放）。
	inflightDone, inflightErr := reserveInflightBalance(c, h.billingCacheService, h.gatewayService, apiKey, subscription, grokVoiceInflightEstimate(endpoint, body))
	if inflightErr != nil {
		status, code, message, retryAfter := billingErrorDetails(inflightErr)
		if retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		h.errorResponse(c, status, code, message)
		return
	}
	defer inflightDone()

	var balanceGuard *service.BalancePreauthorizationGuard
	var voiceRequestID string
	var estimateVoiceHold func(context.Context, *service.APIKey) (float64, error)
	if voiceUnits := grokVoicePreauthUnits(endpoint, body); voiceUnits > 0 {
		voiceRequestID = service.StableGrokAudioBillingRequestID("")
		estimateVoiceHold = func(routeCtx context.Context, key *service.APIKey) (float64, error) {
			return h.gatewayService.EstimateAudioPreauthorizationCost(routeCtx, key, endpoint, endpoint, voiceUnits), nil
		}
		voiceHoldAmount, _ := estimateVoiceHold(c.Request.Context(), apiKey)
		var preauthErr error
		balanceGuard, preauthErr = preauthorizeFixedAmountGatewayRequest(
			c.Request.Context(), h.balancePreauthorizer, apiKey, subscription, voiceRequestID, body, voiceHoldAmount,
		)
		if h.handlePreauthorizationError(c, preauthErr, false) {
			return
		}
		if balanceGuard != nil {
			c.Request = c.Request.WithContext(service.ContextWithBalancePreauthorizationGuard(c.Request.Context(), balanceGuard))
		}
	}
	defer func() { deferBalancePreauthorizationRefund(reqLog, balanceGuard) }()

	contentType := c.GetHeader("Content-Type")
	if strings.TrimSpace(contentType) == "" {
		contentType = "application/json"
	}

	failed := map[int64]struct{}{}
	var last *service.UpstreamFailoverError
	profitVetoCount := 0
	accountSlotBusySeen := false
	selectionModel := "grok-4.5"

	for attempts := 0; attempts < 4; attempts++ {
		selection, _, routedKey, selectErr := h.gatewayService.SelectAccountWithSchedulerForCapabilityAlongKeyRoutes(
			c.Request.Context(),
			apiKey,
			"",
			"",
			selectionModel,
			failed,
			service.OpenAIUpstreamTransportHTTPSSE,
			service.OpenAIEndpointCapabilityChatCompletions,
			false,
			false,
			false,
			service.PlatformGrok,
		)
		if selectErr == nil && routedKey != nil {
			binding := keyRouteBinding{Previous: apiKey, Selected: routedKey, Subscription: &subscription}
			if estimateVoiceHold != nil {
				binding.Guard = &balanceGuard
				binding.Body = body
				binding.Model = endpoint
				binding.FixedRequestID = voiceRequestID
				binding.FixedAmount = estimateVoiceHold
			}
			if bindErr := h.bindSelectedKeyRoute(c, binding); bindErr != nil {
				releaseRejectedKeyRouteSelection(selection)
				h.handlePreauthorizationError(c, bindErr, false)
				return
			}
			apiKey = routedKey
		}
		if selectErr != nil || selection == nil || selection.Account == nil {
			if last != nil {
				h.handleFailoverExhausted(c, last, false)
			} else if accountSlotBusySeen {
				h.handleOpenAIAccountSlotBusyExhausted(c, false, reqLog)
			} else {
				h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "No available Grok accounts")
			}
			return
		}
		account := selection.Account
		var started bool
		release, status := h.acquireResponsesAccountSlot(c, apiKey.GroupID, "", selection, false, &started, reqLog)
		switch h.openAISlotLoopAction(c, status, account.ID, apiKey.GroupID, "", failed, &profitVetoCount, started, reqLog, &accountSlotBusySeen) {
		case openAISlotLoopStop:
			return
		case openAISlotLoopRotate:
			continue
		}
		result, forwardErr := func() (*service.OpenAIForwardResult, error) {
			defer release()
			return h.gatewayService.ForwardGrokVoice(c.Request.Context(), c, account, endpoint, body, contentType)
		}()
		if forwardErr == nil {
			h.recordGrokVoiceUsage(c, apiKey, account, subscription, endpoint, body, result)
			return
		}
		var failoverErr *service.UpstreamFailoverError
		if errors.As(forwardErr, &failoverErr) && failoverErr.ShouldRetryNextAccount() {
			failed[account.ID] = struct{}{}
			last = failoverErr
			continue
		}
		// Non-failover errors: handleGrokMediaErrorResponse / transport already wrote response.
		return
	}
	if last != nil {
		h.handleFailoverExhausted(c, last, false)
	}
}

// recordGrokVoiceUsage bills TTS/STT/realtime via group audio prices when AudioUsage is set.
func (h *OpenAIGatewayHandler) recordGrokVoiceUsage(
	c *gin.Context,
	apiKey *service.APIKey,
	account *service.Account,
	subscription *service.UserSubscription,
	endpoint string,
	body []byte,
	result *service.OpenAIForwardResult,
) {
	if h == nil || c == nil || apiKey == nil || account == nil || result == nil {
		return
	}
	if result.AudioUsage == nil {
		return
	}
	// Ensure forced durable request ids even if callers forget (realtime/tts/stt money path).
	if mode := strings.TrimSpace(result.AudioUsage.Mode); mode == "realtime" {
		result.RequestID = service.StableGrokRealtimeBillingRequestID(result.RequestID)
	} else {
		result.RequestID = service.StableGrokAudioBillingRequestID(result.RequestID)
	}
	userAgent := c.GetHeader("User-Agent")
	clientIP := ip.GetClientIP(c)
	sessionID := service.ExtractClientSessionID(c)
	requestPayloadHash := service.HashUsageRequestPayload(body)
	if requestPayloadHash == "" {
		requestPayloadHash = service.HashUsageRequestPayload([]byte(endpoint))
	}
	inboundEndpoint := GetInboundEndpoint(c)
	upstreamEndpoint := GetUpstreamEndpoint(c, account.Platform)
	quotaPlatform := service.QuotaPlatform(c.Request.Context(), apiKey)
	model := strings.TrimSpace(result.Model)
	if model == "" {
		model = endpoint
	}
	// worker 在请求结束后才跑：闭包里不能再带着 gin.Context（它连着整份请求体）。
	channelUsageFields := clientRequestedUsageFields(c, service.ChannelMappingResult{}, model, result.UpstreamModel).Detached()
	result.DetachRequestStrings()

	h.submitMandatoryUsageRecordTask(c.Request.Context(), func(ctx context.Context) {
		if err := h.gatewayService.RecordUsage(ctx, &service.OpenAIRecordUsageInput{
			Result:             result,
			APIKey:             apiKey,
			User:               apiKey.User,
			Account:            account,
			Subscription:       subscription,
			InboundEndpoint:    inboundEndpoint,
			UpstreamEndpoint:   upstreamEndpoint,
			UserAgent:          userAgent,
			IPAddress:          clientIP,
			RequestPayloadHash: requestPayloadHash,
			APIKeyService:      h.apiKeyService,
			QuotaPlatform:      quotaPlatform,
			SessionID:          sessionID,
			ChannelUsageFields: channelUsageFields,
		}); err != nil {
			logger.L().With(
				zap.String("component", "handler.openai_gateway.grok_voice"),
				zap.Int64("user_id", apiKey.User.ID),
				zap.Int64("api_key_id", apiKey.ID),
				zap.Any("group_id", apiKey.GroupID),
				zap.String("endpoint", endpoint),
				zap.Int64("account_id", account.ID),
			).Error("grok_voice.record_usage_failed", zap.Error(err))
		}
	})
}

func readGrokVoiceGatewayBody(c *gin.Context) ([]byte, error) {
	if c == nil || c.Request == nil {
		return nil, errors.New("request body is required")
	}
	if c.Request.Body == nil {
		if c.Request.Method == http.MethodGet || c.Request.Method == http.MethodDelete {
			return nil, nil
		}
		return nil, errors.New("request body is required")
	}
	return io.ReadAll(c.Request.Body)
}

// extractGrokTTSInputText pulls the text xAI actually speaks from a TTS JSON
// body, so moderation and in-flight reservation use the same field as billing.
func extractGrokTTSInputText(body []byte) string {
	return service.GrokTTSText(body)
}

// grokVoicePreauthUnits mirrors service.estimateGrokVoiceAudioUsage so TTS and
// STT preauthorization holds match post-usage billing units. Non-billable
// endpoints (custom-voices) return 0 and skip preauthorization.
func grokVoicePreauthUnits(endpoint string, body []byte) float64 {
	switch strings.TrimSpace(endpoint) {
	case "tts":
		chars := len([]rune(extractGrokTTSInputText(body)))
		if chars <= 0 {
			chars = len(body)
		}
		if chars <= 0 {
			return 0
		}
		return float64(chars) / 1_000_000.0
	case "stt":
		secs := 0.0
		if len(body) > 0 {
			secs = float64(len(body)) / grokVoiceSTTBytesPerSecond
		}
		if gjson.ValidBytes(body) {
			if v := gjson.GetBytes(body, "duration_seconds"); v.Exists() && v.Type == gjson.Number {
				if clientSecs := v.Float(); !math.IsNaN(clientSecs) && !math.IsInf(clientSecs, 0) && clientSecs > secs {
					secs = clientSecs
				}
			}
		}
		const maxFallbackSeconds = 24 * 3600.0
		if secs > maxFallbackSeconds {
			secs = maxFallbackSeconds
		}
		if secs <= 0 {
			return 0
		}
		return secs / 3600.0
	default:
		return 0
	}
}
