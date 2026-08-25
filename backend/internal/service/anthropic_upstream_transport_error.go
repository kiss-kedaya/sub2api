package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// anthropicTransportErrorTempUnschedDuration is the cooldown applied to an
// Anthropic account when the transport failure identifies a durable endpoint
// or proxy fault. Keep this in line with the OpenAI transport path.
const anthropicTransportErrorTempUnschedDuration = 10 * time.Minute

// anthropicTransportFailoverBody is retained for the final, failover-exhausted
// response. The request handler owns writing the response; the gateway only
// carries this body in the typed failover error.
var anthropicTransportFailoverBody = []byte(`{"type":"error","error":{"type":"upstream_error","message":"Upstream request failed"}}`)

// handleAnthropicUpstreamTransportError records a transport-level upstream
// failure and returns a typed failover signal for the request handler. A
// transport error means no HTTP status was received (DNS/TCP/TLS/proxy path),
// so writing a 502 here would prevent same-request account failover.
//
// Client cancellation is deliberately not failover-eligible: the caller is
// already gone and evicting the account would penalize a healthy upstream.
// Durable network/proxy failures are temporarily unscheduled, while transient
// errors still fail over without changing account health state.
func (s *GatewayService) handleAnthropicUpstreamTransportError(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	upstreamURL string,
	passthrough bool,
	err error,
) error {
	if err == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	safeErr := sanitizeUpstreamErrorMessage(err.Error())
	if account != nil {
		setOpsUpstreamError(c, 0, safeErr, "")
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
			Platform:           account.Platform,
			AccountID:          account.ID,
			AccountName:        account.Name,
			UpstreamStatusCode: 0,
			UpstreamURL:        upstreamURL,
			Passthrough:        passthrough,
			Kind:               "request_error",
			Message:            safeErr,
		})
	} else {
		setOpsUpstreamError(c, 0, safeErr, "")
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
			UpstreamStatusCode: 0,
			UpstreamURL:        upstreamURL,
			Passthrough:        passthrough,
			Kind:               "request_error",
			Message:            safeErr,
		})
	}

	// A client that has gone away must not trigger account failover or health
	// penalties. Keep the legacy error text for callers that surface it in logs.
	if errors.Is(err, context.Canceled) ||
		(errors.Is(err, context.DeadlineExceeded) && errors.Is(ctx.Err(), context.DeadlineExceeded)) {
		return fmt.Errorf("upstream request failed: %s", safeErr)
	}

	if account != nil {
		// Transport attempt reached the network path; retain Ollama Cloud activity
		// accounting from the previous inline implementation.
		scheduleOllamaCloudUsageActivity(s.deferredService, account)

		if classifyOpenAITransportError(err).Persistent {
			s.tempUnscheduleAnthropicTransportError(ctx, account, safeErr)
		}
	}

	return &UpstreamFailoverError{
		StatusCode:   http.StatusBadGateway,
		ResponseBody: anthropicTransportFailoverBody,
	}
}

// tempUnscheduleAnthropicTransportError applies both the immediate scheduler
// block (when a runtime blocker is configured) and the durable DB state. The
// persistence write uses a detached, bounded context so a canceled client
// request cannot leave account health state half-updated.
func (s *GatewayService) tempUnscheduleAnthropicTransportError(ctx context.Context, account *Account, safeErr string) {
	if s == nil || account == nil {
		return
	}

	until := time.Now().Add(anthropicTransportErrorTempUnschedDuration)
	reason := "upstream transport error (proxy/network): " + safeErr

	// Runtime block is best effort and intentionally independent of the DB
	// write. This prevents another request from selecting the account while the
	// account cache is waiting for the persisted state to propagate.
	if s.rateLimitService != nil {
		s.rateLimitService.notifyAccountSchedulingBlocked(account, until, "transport_error")
	}

	if s.accountRepo == nil {
		logger.L().With(zap.String("component", "service.gateway")).Warn(
			"anthropic.account_temp_unscheduled_transport_memory_only",
			zap.Int64("account_id", account.ID),
			zap.String("account_name", account.Name),
			zap.String("platform", account.Platform),
			zap.Time("until", until),
			zap.String("reason", reason),
		)
		return
	}

	bgCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), openAIAccountStateUpdateTimeout)
	defer cancel()
	if err := s.accountRepo.SetTempUnschedulable(bgCtx, account.ID, until, reason); err != nil {
		logger.L().With(zap.String("component", "service.gateway")).Warn(
			"anthropic.account_temp_unscheduled_transport_failed",
			zap.Int64("account_id", account.ID),
			zap.Error(err),
		)
		return
	}

	logger.L().With(zap.String("component", "service.gateway")).Warn(
		"anthropic.account_temp_unscheduled_transport",
		zap.Int64("account_id", account.ID),
		zap.String("account_name", account.Name),
		zap.String("platform", account.Platform),
		zap.Time("until", until),
		zap.String("reason", reason),
	)
}
