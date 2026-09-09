package service

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/util/httputil"
)

// UpstreamFailureKind is the single taxonomy used by failover, cooldown,
// account punishment, and the ops error chip. Values are stable wire labels.
type UpstreamFailureKind string

const (
	UpstreamFailureNone         UpstreamFailureKind = ""
	UpstreamFailureClient       UpstreamFailureKind = "client"
	UpstreamFailureModelMissing UpstreamFailureKind = "model_not_found"
	UpstreamFailureCompat       UpstreamFailureKind = "compat-400"
	UpstreamFailureAuth         UpstreamFailureKind = "credential-forbidden"
	UpstreamFailureWAF          UpstreamFailureKind = "cloudflare-waf"
	UpstreamFailureCapacity     UpstreamFailureKind = "capacity-cooling"
	UpstreamFailureRateLimit    UpstreamFailureKind = "rate-limit"
	UpstreamFailureServer       UpstreamFailureKind = "server"
	UpstreamFailureNetwork      UpstreamFailureKind = "network"
	UpstreamFailureCanceled     UpstreamFailureKind = "canceled"
)

// UpstreamFailureClass is the action table for one upstream outcome.
type UpstreamFailureClass struct {
	Kind             UpstreamFailureKind
	Failover         bool
	SameAccountRetry bool
	PunishAccount    bool
}

// ClassifyUpstreamFailure maps status/headers/body/error onto one kind.
// WAF/Cloudflare 1010 is never capacity cooling and never a credential error.
func ClassifyUpstreamFailure(statusCode int, headers http.Header, body []byte, err error) UpstreamFailureClass {
	if err != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
		return UpstreamFailureClass{Kind: UpstreamFailureCanceled}
	}
	if httputil.IsCloudflareChallengeResponse(statusCode, headers, body) || IsUpstreamWAFBody(body) {
		return UpstreamFailureClass{Kind: UpstreamFailureWAF, Failover: true}
	}
	if IsUpstreamCapacityCoolingBody(body) {
		return UpstreamFailureClass{
			Kind:             UpstreamFailureCapacity,
			Failover:         true,
			SameAccountRetry: true,
		}
	}
	if err != nil && statusCode == 0 {
		return UpstreamFailureClass{Kind: UpstreamFailureNetwork, Failover: true, SameAccountRetry: true}
	}

	switch statusCode {
	case http.StatusUnauthorized:
		return UpstreamFailureClass{Kind: UpstreamFailureAuth, Failover: true, PunishAccount: true}
	case http.StatusPaymentRequired, http.StatusMethodNotAllowed:
		return UpstreamFailureClass{Kind: UpstreamFailureAuth, Failover: true, PunishAccount: true}
	case http.StatusForbidden:
		return UpstreamFailureClass{Kind: UpstreamFailureAuth, Failover: true, PunishAccount: true}
	case http.StatusTooManyRequests, 529:
		return UpstreamFailureClass{Kind: UpstreamFailureRateLimit, Failover: true, SameAccountRetry: true}
	case http.StatusBadRequest:
		if isOpenAICompatibleModelNotFound400(body) {
			return UpstreamFailureClass{Kind: UpstreamFailureModelMissing, Failover: true}
		}
		if isGoogleProjectConfigError(strings.ToLower(extractUpstreamErrorMessage(body))) {
			return UpstreamFailureClass{Kind: UpstreamFailureCompat, Failover: true, SameAccountRetry: true}
		}
		if isCompat400Body(body) {
			return UpstreamFailureClass{Kind: UpstreamFailureCompat, Failover: true}
		}
		return UpstreamFailureClass{Kind: UpstreamFailureClient}
	case http.StatusNotFound, http.StatusUnprocessableEntity:
		if isOpenAICompatibleModelNotFound400(body) {
			return UpstreamFailureClass{Kind: UpstreamFailureModelMissing, Failover: true}
		}
		return UpstreamFailureClass{Kind: UpstreamFailureClient}
	default:
		if statusCode >= 500 {
			return UpstreamFailureClass{Kind: UpstreamFailureServer, Failover: true, SameAccountRetry: true}
		}
		if statusCode >= 400 {
			return UpstreamFailureClass{Kind: UpstreamFailureClient}
		}
		return UpstreamFailureClass{Kind: UpstreamFailureNone}
	}
}

// ShouldFailoverUpstream reports whether the outcome should rotate accounts.
func ShouldFailoverUpstream(statusCode int, headers http.Header, body []byte, err error) bool {
	return ClassifyUpstreamFailure(statusCode, headers, body, err).Failover
}

// ShouldFailoverUpstreamResponse reports whether to rotate accounts for an HTTP
// outcome. Compat-400 (thinking / tool_use / anthropic-beta) stays behind
// failoverOn400; model_not_found failovers without that flag.
func ShouldFailoverUpstreamResponse(statusCode int, headers http.Header, body []byte, failoverOn400 bool) bool {
	class := ClassifyUpstreamFailure(statusCode, headers, body, nil)
	if class.Kind == UpstreamFailureCompat {
		return failoverOn400
	}
	return class.Failover
}

// PoolModeSameAccountRetry reports whether pool_mode may retry the same account.
func PoolModeSameAccountRetry(account *Account, statusCode int, headers http.Header, body []byte) bool {
	if account == nil || !account.IsPoolMode() || !account.IsPoolModeRetryableStatus(statusCode) {
		return false
	}
	return ClassifyUpstreamFailure(statusCode, headers, body, nil).SameAccountRetry
}

func isCompat400Body(body []byte) bool {
	msg := strings.ToLower(strings.TrimSpace(extractUpstreamErrorMessage(body)))
	if msg == "" {
		return false
	}
	if strings.Contains(msg, "anthropic-beta") ||
		strings.Contains(msg, "beta feature") ||
		strings.Contains(msg, "requires beta") {
		return true
	}
	if strings.Contains(msg, "thinking") || strings.Contains(msg, "thought_signature") {
		return true
	}
	if strings.Contains(msg, "tool_use") || strings.Contains(msg, "tool_result") {
		return true
	}
	return false
}
