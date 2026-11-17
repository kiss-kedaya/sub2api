package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
)

// deepSeekUpstreamWaitLimit bounds how long a DeepSeek chat-style request waits
// for the upstream to answer at all. The OpenAI transport profile it shares has
// no response-header timeout, and the gateway's own SSE heartbeat keeps the
// client, nginx and the CDN from giving up, so a connection the provider had
// abandoned was held for hours.
//
// The limit only ends requests that can no longer succeed (owner decision
// 2026-11-18: user experience first, necessary measures only). Over six hours
// of production traffic the latest successful answer arrived after 59 minutes
// of silence, and the provider itself answers with an error at 60 minutes;
// anything still silent after that is a dead connection. A stream that has
// started is never cut: the limit ends once the upstream call returns its
// response, which is when the headers arrive, or the first body bytes if the
// response is compressed (the transport reads those to set up decompression).
//
// It is a variable only so tests can shorten it.
var deepSeekUpstreamWaitLimit = 65 * time.Minute

// errOpenAIUpstreamWaitLimit marks a request ended by the limit above.
var errOpenAIUpstreamWaitLimit = errors.New("upstream sent no response within the wait limit")

const upstreamWaitLimitReason = GatewayFailureReason("upstream_wait_limit")

// IsUpstreamWaitLimit reports a request ended by the upstream wait limit. The
// provider went silent on this request; that says nothing about the account,
// so it keeps its session affinity and its scheduler health.
func (e *UpstreamFailoverError) IsUpstreamWaitLimit() bool {
	return e != nil && e.Reason == upstreamWaitLimitReason
}

var openAIUpstreamWaitLimitFailoverBody = []byte(`{"error":{"type":"upstream_error","message":"Upstream did not respond in time"}}`)

// openAIUpstreamWaitLimitFor returns the limit for this request, or 0 for none.
// Only DeepSeek forwards built with the OpenAI profile qualify: that profile
// is the one without a response-header timeout. The native Anthropic endpoint
// and the search and fetch tools use the default profile, which already times
// out at the transport; the media tools carry their own deadlines.
func openAIUpstreamWaitLimitFor(request *http.Request, account *Account) time.Duration {
	if request == nil || account == nil || account.Platform != PlatformDeepseek {
		return 0
	}
	if HTTPUpstreamProfileFromContext(request.Context()) != HTTPUpstreamProfileOpenAI {
		return 0
	}
	return deepSeekUpstreamWaitLimit
}

// doWithUpstreamWaitLimit cancels send when no response headers arrive within
// limit. Once headers are in, the timer is disarmed and the body keeps reading
// under the same context, which is released when the body is closed.
func doWithUpstreamWaitLimit(request *http.Request, limit time.Duration, send func(*http.Request) (*http.Response, error)) (*http.Response, error) {
	ctx, cancel := context.WithCancelCause(request.Context())
	timer := time.AfterFunc(limit, func() { cancel(errOpenAIUpstreamWaitLimit) })
	resp, err := send(request.WithContext(ctx))
	if !timer.Stop() {
		// The callback has started; wait for it so the decision below is settled
		// and cannot cancel a body that was already handed to the caller.
		<-ctx.Done()
	}
	if errors.Is(context.Cause(ctx), errOpenAIUpstreamWaitLimit) {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		return nil, fmt.Errorf("%w (%s)", errOpenAIUpstreamWaitLimit, limit)
	}
	if err != nil || resp == nil || resp.Body == nil {
		cancel(nil)
		return resp, err
	}
	resp.Body = &openAIRequestContextReadCloser{ReadCloser: resp.Body, cleanup: func() { cancel(nil) }}
	return resp, nil
}

// newOpenAIUpstreamWaitLimitError records the timeout for operators and returns
// a failover error that ends the request: retrying the same payload on another
// account would only hold it for another full limit per account. The account is
// not unscheduled; a silent provider is not a credential or network fault.
func (s *OpenAIGatewayService) newOpenAIUpstreamWaitLimitError(c *gin.Context, account *Account, err error, passthrough bool) *UpstreamFailoverError {
	message := sanitizeUpstreamErrorMessage(err.Error())
	logger.LegacyPrintf("service.openai_gateway", "OpenAI upstream wait limit: account=%d platform=%s %s", account.ID, account.Platform, message)
	setOpsUpstreamError(c, http.StatusGatewayTimeout, message, "")
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		ProxyID:            opsUpstreamProxyID(account),
		ProxyName:          opsUpstreamProxyName(account),
		Platform:           account.Platform,
		AccountID:          account.ID,
		AccountName:        account.Name,
		UpstreamStatusCode: http.StatusGatewayTimeout,
		Passthrough:        passthrough,
		Kind:               "response_wait_limit",
		Message:            message,
	})
	return &UpstreamFailoverError{
		StatusCode:        http.StatusGatewayTimeout,
		ResponseBody:      openAIUpstreamWaitLimitFailoverBody,
		Scope:             GatewayFailureScopeRequest,
		Reason:            upstreamWaitLimitReason,
		NextAccountAction: NextAccountStop,
	}
}
