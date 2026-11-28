package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

// DeepSeekToolsSearch / DeepSeekToolsFetch proxy DeepSeek's standalone tool
// endpoints (/v1/tools/search, /v1/tools/fetch). They are raw JSON endpoints
// with Bearer auth, so the client body is forwarded as-is and the upstream
// response is passed through. HTTP 200 with actual results is billed once via
// WebSearchCalls, using the group's per-1k search price when configured.
func (h *OpenAIGatewayHandler) DeepSeekToolsSearch(c *gin.Context) {
	h.deepSeekTools(c, service.DeepSeekToolsPathSearch, service.DeepSeekToolsLabelSearch)
}

func (h *OpenAIGatewayHandler) DeepSeekToolsFetch(c *gin.Context) {
	h.deepSeekTools(c, service.DeepSeekToolsPathFetch, service.DeepSeekToolsLabelFetch)
}

// DeepSeekToolsASR forwards native speech recognition uploads.
func (h *OpenAIGatewayHandler) DeepSeekToolsASR(c *gin.Context) {
	h.deepSeekTools(c, service.DeepSeekToolsPathASR, service.DeepSeekToolsLabelASR)
}

// DeepSeekToolsTTS forwards native streaming speech synthesis.
func (h *OpenAIGatewayHandler) DeepSeekToolsTTS(c *gin.Context) {
	h.deepSeekTools(c, service.DeepSeekToolsPathTTS, service.DeepSeekToolsLabelTTS)
}

// DeepSeekToolsOCR forwards native document recognition uploads.
func (h *OpenAIGatewayHandler) DeepSeekToolsOCR(c *gin.Context) {
	h.deepSeekTools(c, service.DeepSeekToolsPathOCR, service.DeepSeekToolsLabelOCR)
}

// All standalone tools share authentication, eligibility, slots, selection and settlement.
func (h *OpenAIGatewayHandler) deepSeekTools(c *gin.Context, toolPath string, modelLabel string) {
	streamStarted := false
	defer h.recoverResponsesPanic(c, &streamStarted)
	setOpenAIClientTransportHTTP(c)
	requestStart := time.Now()
	c.Request = c.Request.WithContext(h.gatewayService.PrepareSchedulerRequestContext(c.Request.Context()))

	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok || apiKey.Group == nil {
		h.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}
	if apiKey.Group.Platform != service.PlatformDeepseek {
		h.errorResponse(c, http.StatusNotFound, "not_found_error", "DeepSeek tools are only available for DeepSeek groups")
		return
	}
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusInternalServerError, "api_error", "User context not found")
		return
	}
	reqLog := requestLogger(
		c,
		"handler.openai_gateway.deepseek_tools",
		zap.Int64("user_id", subject.UserID),
		zap.Int64("api_key_id", apiKey.ID),
		zap.Any("group_id", apiKey.GroupID),
		zap.String("tool_path", toolPath),
	)
	if !h.ensureResponsesDependencies(c, reqLog) {
		slog.Warn("deepseek_tools.dependencies_missing", "tool_path", toolPath)
		return
	}

	mediaTool := toolPath == service.DeepSeekToolsPathASR || toolPath == service.DeepSeekToolsPathTTS || toolPath == service.DeepSeekToolsPathOCR
	body, err := readDeepSeekToolBody(c, toolPath)
	if err != nil {
		if maxErr, ok := extractMaxBytesError(err); ok {
			h.errorResponse(c, http.StatusRequestEntityTooLarge, "invalid_request_error", buildBodyTooLargeMessage(maxErr.Limit))
			return
		}
		// Validation failures carry a client-facing message; transport read
		// errors keep the generic text instead of exposing internals.
		message := "Failed to read request body"
		var invalid *deepSeekToolBodyError
		if errors.As(err, &invalid) {
			message = invalid.message
		}
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", message)
		return
	}
	if len(body) == 0 {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Request body is empty")
		return
	}
	if !mediaTool && !gjson.ValidBytes(body) {
		logRequestBodyParseFailure(reqLog, body, nil)
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return
	}
	// 上游对两个端点各自有必填字段（search: query；fetch: url）。这里只做最小
	// 校验，把结构演进留给上游，避免绑定变化的 schema。
	requiredField := "query"
	if toolPath == service.DeepSeekToolsPathFetch {
		requiredField = "url"
	}
	if !mediaTool && strings.TrimSpace(gjson.GetBytes(body, requiredField).String()) == "" {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", requiredField+" is required")
		return
	}

	setOpsRequestContext(c, modelLabel, false)
	setOpsEndpointContext(c, "", int16(service.RequestTypeSync))
	auditBody := body
	if toolPath == service.DeepSeekToolsPathTTS {
		auditBody, err = json.Marshal(map[string]any{"messages": []map[string]string{{"role": "user", "content": gjson.GetBytes(body, "text").String()}}})
		if err != nil {
			h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to prepare text moderation")
			return
		}
	}
	if !mediaTool || toolPath == service.DeepSeekToolsPathTTS {
		if decision := h.checkSecurityAudit(c, reqLog, apiKey, subject, service.ContentModerationProtocolOpenAIChat, modelLabel, auditBody); decision != nil && !decision.AllowNextStage {
			h.openAISecurityAuditError(c, decision)
			return
		}
	}

	if toolPath == service.DeepSeekToolsPathASR || toolPath == service.DeepSeekToolsPathOCR {
		// Uploaded recognition continues after disconnect. Slot leases must live
		// with that work too, not be released early by the client context.
		processingCtx, cancel := context.WithCancel(context.WithoutCancel(c.Request.Context()))
		defer cancel()
		c.Request = c.Request.WithContext(processingCtx)
	}
	subscription, _ := middleware2.GetSubscriptionFromContext(c)
	service.SetOpsLatencyMs(c, service.OpsAuthLatencyMsKey, time.Since(requestStart).Milliseconds())
	asPricingCtx, asPricingAt := h.attachTextPricingContext(c, apiKey.GroupID)
	c.Request = c.Request.WithContext(asPricingCtx)

	userRelease, acquired := h.acquireResponsesUserSlot(c, subject.UserID, subject.Concurrency, false, &streamStarted, reqLog)
	if !acquired {
		return
	}
	if userRelease != nil {
		defer userRelease()
	}

	if err := h.billingCacheService.CheckBillingEligibility(asPricingCtx, apiKey.User, apiKey, apiKey.Group, subscription, service.QuotaPlatform(asPricingCtx, apiKey)); err != nil {
		status, code, message, retryAfter := billingErrorDetails(err)
		if retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		h.errorResponse(c, status, code, message)
		return
	}

	// Recognition reserves the official maximum units; synthesis uses the original text.
	estimate := service.InflightEstimateRequest{Model: modelLabel, BodyBytes: len(body), Kind: service.InflightEstimatePerRequest, SearchCalls: 1}
	toolUnits := 1.0
	switch toolPath {
	case service.DeepSeekToolsPathASR:
		toolUnits = 120.0 / 3600
		estimate.Kind, estimate.SearchCalls, estimate.AudioMode, estimate.AudioUnits = service.InflightEstimateAudio, 0, "stt", toolUnits
	case service.DeepSeekToolsPathTTS:
		toolUnits = float64(service.DeepSeekTTSBillableCharacters(gjson.GetBytes(body, "text").String())) / 1_000_000
		estimate.Kind, estimate.SearchCalls, estimate.AudioMode, estimate.AudioUnits = service.InflightEstimateAudio, 0, "tts", toolUnits
	case service.DeepSeekToolsPathOCR:
		ocrPages := service.EstimateDeepSeekOCRPages(c.GetHeader("Content-Type"), body)
		toolUnits = float64(ocrPages)
		estimate.SearchCalls, estimate.Units = 0, ocrPages
	}
	inflightDone, inflightErr := reserveInflightBalance(c, h.billingCacheService, h.gatewayService, apiKey, subscription, estimate)
	if inflightErr != nil {
		status, code, message, retryAfter := billingErrorDetails(inflightErr)
		if retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		h.errorResponse(c, status, code, message)
		return
	}
	defer inflightDone()

	estimateToolHold := func(routeCtx context.Context, key *service.APIKey) (float64, error) {
		return h.gatewayService.EstimateDeepSeekToolPreauthorizationCost(routeCtx, key, modelLabel, toolUnits), nil
	}
	toolHoldAmount, _ := estimateToolHold(c.Request.Context(), apiKey)
	balanceGuard, preauthErr := preauthorizeFixedAmountGatewayRequest(
		c.Request.Context(), h.balancePreauthorizer, apiKey, subscription, "", body, toolHoldAmount,
	)
	if h.handlePreauthorizationError(c, preauthErr, false) {
		return
	}
	defer func() { deferBalancePreauthorizationRefund(reqLog, balanceGuard) }()
	if balanceGuard != nil {
		c.Request = c.Request.WithContext(service.ContextWithBalancePreauthorizationGuard(c.Request.Context(), balanceGuard))
	}

	sessionHash := h.gatewayService.GenerateSessionHashWithFallback(c, nil, "")
	failedAccountIDs := make(map[int64]struct{})
	sameAccountRetries := make(map[int64]int)
	var lastFailoverErr *service.UpstreamFailoverError
	profitVetoCount := 0
	accountSlotBusySeen := false
	switchCount := 0
	budget := newFailoverBudget() // 单请求重试硬上限：总尝试次数 + 总耗时
	routingStart := time.Now()
	channelMapping := service.ChannelMappingResult{}

	// 选号必须按真实模型名匹配（分组白名单 / 账号 model_mapping 都是按模型名配的），
	// 但计费标签用工具名，两者不能混用。
	routingModel := ""
	if toolPath != service.DeepSeekToolsPathASR && toolPath != service.DeepSeekToolsPathOCR {
		routingModel = strings.TrimSpace(gjson.GetBytes(body, "model").String())
	}
	if routingModel == "" {
		routingModel = strings.TrimSpace(c.Query("model"))
	}
	// Search/Fetch has no required model. Use this key's authorized DeepSeek
	// groups and their current catalog rather than a stale hard-coded alias.
	toolsKey, routingModels, routingErr := h.gatewayService.PrepareDeepSeekToolsRouting(c.Request.Context(), apiKey, routingModel)
	if routingErr != nil {
		h.errorResponse(c, http.StatusBadGateway, "upstream_error", "No available DeepSeek tool accounts")
		return
	}
	modelIndex := 0
	routingModel = routingModels[modelIndex]

	for {
		selection, _, routedKey, err := h.gatewayService.SelectAccountWithSchedulerForCapabilityAlongKeyRoutes(
			c.Request.Context(),
			toolsKey,
			"",
			sessionHash,
			routingModel,
			failedAccountIDs,
			service.OpenAIUpstreamTransportHTTPSSE,
			service.OpenAIEndpointCapabilityChatCompletions,
			false,
			false,
			false,
			service.PlatformDeepseek,
		)
		if err == nil && routedKey != nil {
			if bindErr := h.bindSelectedKeyRoute(c, keyRouteBinding{
				Previous: apiKey, Selected: routedKey, Subscription: &subscription,
				Mapping: &channelMapping, Guard: &balanceGuard, Body: body, Model: routingModel, PricingAt: asPricingAt,
				FixedAmount: estimateToolHold,
			}); bindErr != nil {
				releaseRejectedKeyRouteSelection(selection)
				h.handlePreauthorizationError(c, bindErr, false)
				return
			}
			apiKey = routedKey
		}
		if err != nil || selection == nil || selection.Account == nil {
			if failoverClientGone(c) {
				reqLog.Info("deepseek_tools.account_select_aborted_client_disconnected", zap.Error(err))
				return
			}
			if lastFailoverErr == nil && modelIndex+1 < len(routingModels) &&
				(err == nil || errors.Is(err, service.ErrNoAvailableAccounts)) {
				modelIndex++
				routingModel = routingModels[modelIndex]
				continue
			}
			reqLog.Warn("deepseek_tools.select_failed",
				zap.String("tool_path", toolPath),
				zap.String("routing_model", routingModel),
				zap.Int("failed_accounts", len(failedAccountIDs)),
				zap.Error(err),
			)
			if lastFailoverErr != nil {
				h.handleFailoverExhausted(c, lastFailoverErr, false)
			} else if accountSlotBusySeen {
				h.handleOpenAIAccountSlotBusyExhausted(c, false, reqLog)
			} else {
				h.errorResponse(c, http.StatusBadGateway, "upstream_error", "Upstream request failed")
			}
			return
		}

		account := selection.Account
		setOpsSelectedAccount(c, account.ID, account.Platform, account.Name)
		accountRelease, slotResult := h.acquireResponsesAccountSlot(c, apiKey.GroupID, sessionHash, selection, false, &streamStarted, reqLog)
		switch h.openAISlotLoopAction(c, slotResult, account.ID, apiKey.GroupID, sessionHash, failedAccountIDs, &profitVetoCount, streamStarted, reqLog, &accountSlotBusySeen) {
		case openAISlotLoopStop:
			return
		case openAISlotLoopRotate:
			continue
		}
		service.SetOpsLatencyMs(c, service.OpsRoutingLatencyMsKey, time.Since(routingStart).Milliseconds())
		forwardStart := time.Now()
		var result *service.OpenAIForwardResult
		result, err = func() (*service.OpenAIForwardResult, error) {
			if accountRelease != nil {
				defer accountRelease()
			}
			if mediaTool {
				return h.gatewayService.ForwardDeepSeekMedia(c.Request.Context(), c, account, toolPath, modelLabel, body)
			}
			return h.gatewayService.ForwardDeepSeekTools(c.Request.Context(), c, account, apiKey.Group, toolPath, modelLabel, body)
		}()
		service.SetOpsLatencyMs(c, service.OpsResponseLatencyMsKey, time.Since(forwardStart).Milliseconds())
		independentProvider := !mediaTool && service.HasIndependentDeepSeekSearchProvider(apiKey.Group)

		if err == nil {
			if !independentProvider {
				h.gatewayService.ReportOpenAIAccountScheduleResult(account, openAIAccountScheduleModel(c, account, modelLabel, false, result), true, nil)
			}
			if result != nil {
				h.recordDeepSeekToolsUsage(c, reqLog, apiKey, account, subscription, body, result, subject.UserID, asPricingAt)
			}
			return
		}

		var failoverErr *service.UpstreamFailoverError
		if !errors.As(err, &failoverErr) {
			if !independentProvider {
				h.gatewayService.ReportOpenAIAccountScheduleResult(account, openAIAccountScheduleModel(c, account, modelLabel, false, result), false, nil, err)
			}
			reqLog.Warn("deepseek_tools.forward_failed", zap.Int64("account_id", account.ID), zap.Error(err))
			if mediaTool && c.Writer.Written() && !failoverClientGone(c) {
				_ = c.Error(http.ErrAbortHandler) // Recovery closes raw streams without logging a platform panic
				c.Abort()
				return
			}
			if !c.Writer.Written() && !failoverClientGone(c) {
				h.errorResponse(c, http.StatusBadGateway, "upstream_error", "DeepSeek tool provider request failed")
			}
			return
		}
		if c.Writer.Written() || failoverClientGone(c) {
			return // response bytes must never be replayed on another account
		}
		if reason := budget.spend(time.Now()); reason != "" {
			logFailoverBudgetExhausted(reqLog, reason, account.ID, failoverErr.StatusCode, &budget)
			markFailoverBudgetExhausted(failoverErr)
			h.gatewayService.ReportOpenAIAccountScheduleResult(account, openAIAccountScheduleModel(c, account, modelLabel, false, result), false, nil, err)
			h.handleFailoverExhausted(c, failoverErr, false)
			return
		}
		if mediaTool {
			if count, delay, retry := poolModeSameAccountRetry(account, failoverErr, sameAccountRetries[account.ID]); retry && !h.gatewayService.AccountCircuitOpen(account.ID) {
				sameAccountRetries[account.ID] = count
				select {
				case <-c.Request.Context().Done():
					return
				case <-time.After(delay):
				}
				continue
			}
		}
		h.gatewayService.ReportOpenAIAccountScheduleResult(account, openAIAccountScheduleModel(c, account, modelLabel, false, result), false, nil, err)
		h.gatewayService.RecordOpenAIAccountSwitch()
		failedAccountIDs[account.ID] = struct{}{}
		lastFailoverErr = failoverErr
		if !fillSchedulingSwitchAllowed(switchCount, h.maxAccountSwitches) {
			h.handleFailoverExhausted(c, failoverErr, false)
			return
		}
		switchCount++
		reqLog.Warn("deepseek_tools.upstream_failover_switching",
			zap.Int64("account_id", account.ID),
			zap.Int("upstream_status", failoverErr.StatusCode),
			zap.Int("switch_count", switchCount),
		)
	}
}

// deepSeekToolBodyError is a request validation failure whose message is
// returned to the client verbatim.
type deepSeekToolBodyError struct{ message string }

func (e *deepSeekToolBodyError) Error() string { return e.message }

func readDeepSeekToolBody(c *gin.Context, path string) ([]byte, error) {
	limit := int64(0)
	switch path {
	case service.DeepSeekToolsPathASR, service.DeepSeekToolsPathOCR:
		mediaType, params, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
		if err != nil || mediaType != "multipart/form-data" || params["boundary"] == "" {
			return nil, &deepSeekToolBodyError{"Content-Type must be multipart/form-data with a boundary"}
		}
		limit = 9 << 20 // 8 MiB file plus 1 MiB multipart envelope
		if path == service.DeepSeekToolsPathOCR {
			limit = 101 << 20
		}
	case service.DeepSeekToolsPathTTS:
		mediaType, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
		if err != nil || mediaType != "application/json" {
			return nil, &deepSeekToolBodyError{"Content-Type must be application/json"}
		}
		limit = 64 << 10
	}
	var body []byte
	var err error
	if limit > 0 {
		body, err = io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, limit))
	} else {
		body, err = pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
	}
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, &deepSeekToolBodyError{"Request body is empty"}
	}
	if path == service.DeepSeekToolsPathTTS {
		if !gjson.ValidBytes(body) {
			return nil, &deepSeekToolBodyError{"Invalid JSON request body"}
		}
		textFields := 0
		gjson.ParseBytes(body).ForEach(func(key, _ gjson.Result) bool {
			if strings.EqualFold(key.String(), "text") {
				textFields++
			}
			return true
		})
		if textFields != 1 {
			return nil, &deepSeekToolBodyError{"text must occur exactly once"}
		}
		text := gjson.GetBytes(body, "text")
		if text.Type != gjson.String || strings.TrimSpace(text.String()) == "" {
			return nil, &deepSeekToolBodyError{"text is required and must not be empty"}
		}
		if service.DeepSeekTTSBillableCharacters(text.String()) > 10_000 {
			return nil, &deepSeekToolBodyError{"text exceeds the 10,000 billable character limit"}
		}
	}
	return body, nil
}

// recordDeepSeekToolsUsage settles only provider-confirmed standalone tool units.
func (h *OpenAIGatewayHandler) recordDeepSeekToolsUsage(
	c *gin.Context,
	reqLog *zap.Logger,
	apiKey *service.APIKey,
	account *service.Account,
	subscription *service.UserSubscription,
	body []byte,
	result *service.OpenAIForwardResult,
	userID int64,
	pricingAt time.Time,
) {
	userAgent := c.GetHeader("User-Agent")
	clientIP := ip.GetClientIP(c)
	sessionID := service.ExtractClientSessionID(c)
	requestPayloadHash := service.HashUsageRequestPayload(body)
	inboundEndpoint := GetInboundEndpoint(c)
	upstreamEndpoint := GetUpstreamEndpoint(c, account.Platform)
	if result.UpstreamEndpoint != "" {
		upstreamEndpoint = result.UpstreamEndpoint
	}
	quotaPlatform := service.QuotaPlatform(c.Request.Context(), apiKey)
	// 结果里的字符串可能是请求体的子串：复制后，排队的计费任务不再钉住请求体。
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
			PricingAt:          pricingAt,
		}); err != nil {
			reqLog.Error("deepseek_tools.record_usage_failed",
				zap.Int64("user_id", userID),
				zap.Int64("api_key_id", apiKey.ID),
				zap.Any("group_id", apiKey.GroupID),
				zap.Int64("account_id", account.ID),
				zap.Error(err),
			)
		}
	})
}
