package handler

import (
	"context"
	"errors"
	"log/slog"
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

	body, err := pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
	if err != nil {
		if maxErr, ok := extractMaxBytesError(err); ok {
			h.errorResponse(c, http.StatusRequestEntityTooLarge, "invalid_request_error", buildBodyTooLargeMessage(maxErr.Limit))
			return
		}
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to read request body")
		return
	}
	if len(body) == 0 {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Request body is empty")
		return
	}
	if !gjson.ValidBytes(body) {
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
	if strings.TrimSpace(gjson.GetBytes(body, requiredField).String()) == "" {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", requiredField+" is required")
		return
	}

	setOpsRequestContext(c, modelLabel, false)
	setOpsEndpointContext(c, "", int16(service.RequestTypeSync))
	if decision := h.checkSecurityAudit(c, reqLog, apiKey, subject, service.ContentModerationProtocolOpenAIChat, modelLabel, body); decision != nil && !decision.AllowNextStage {
		h.openAISecurityAuditError(c, decision)
		return
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

	// 余额模式在途预留：上游按次计费，这里同样按一次调用估算。
	inflightDone, inflightErr := reserveInflightBalance(c, h.billingCacheService, h.gatewayService, apiKey, subscription, service.InflightEstimateRequest{
		Model: modelLabel, BodyBytes: len(body), Kind: service.InflightEstimatePerRequest, SearchCalls: 1,
	})
	if inflightErr != nil {
		status, code, message, retryAfter := billingErrorDetails(inflightErr)
		if retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		h.errorResponse(c, status, code, message)
		return
	}
	defer inflightDone()

	sessionHash := h.gatewayService.GenerateSessionHashWithFallback(c, nil, "")
	failedAccountIDs := make(map[int64]struct{})
	var lastFailoverErr *service.UpstreamFailoverError
	profitVetoCount := 0
	accountSlotBusySeen := false
	switchCount := 0
	routingStart := time.Now()
	channelMapping := service.ChannelMappingResult{}

	// 选号必须按真实模型名匹配（分组白名单 / 账号 model_mapping 都是按模型名配的），
	// 但计费标签用工具名，两者不能混用。
	routingModel := strings.TrimSpace(gjson.GetBytes(body, "model").String())
	if routingModel == "" {
		routingModel = strings.TrimSpace(c.Query("model"))
	}
	// Search/Fetch has no required model. Use this key's authorized DeepSeek
	// groups and their current catalog rather than a stale hard-coded alias.
	toolsKey, routingModels, routingErr := h.gatewayService.PrepareDeepSeekToolsRouting(c.Request.Context(), apiKey, routingModel)
	if routingErr != nil {
		h.errorResponse(c, http.StatusBadGateway, "upstream_error", "No available DeepSeek search accounts")
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
				Mapping: &channelMapping, Body: body, Model: routingModel, PricingAt: asPricingAt,
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
			return h.gatewayService.ForwardDeepSeekTools(c.Request.Context(), c, account, apiKey.Group, toolPath, modelLabel, body)
		}()
		service.SetOpsLatencyMs(c, service.OpsResponseLatencyMsKey, time.Since(forwardStart).Milliseconds())
		independentProvider := service.HasIndependentDeepSeekSearchProvider(apiKey.Group)

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
			if !c.Writer.Written() && !failoverClientGone(c) {
				h.errorResponse(c, http.StatusBadGateway, "upstream_error", "DeepSeek search provider request failed")
			}
			return
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

// recordDeepSeekToolsUsage 为一次成功的 Search/Fetch 落按次计费用量行。
// 上游不返回 usage/token 字段，WebSearchCalls=1 按分组搜索单价计费。
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
	quotaPlatform := service.QuotaPlatform(c.Request.Context(), apiKey)

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
