package service

import (
	"context"
	"io"

	"github.com/gin-gonic/gin"
)

// Client disconnect on the OpenAI passthrough and image paths (owner decision
// 2026-11-06): the upstream connection is closed the moment the client leaves,
// stream or not. What the upstream already reported stays authoritative; what it
// produced without reporting is billed from an estimate where the protocol allows
// one (see incomplete_request_usage.go and openai_stream_incomplete_usage.go), and
// never invented where it does not (images).
//
// These requests run on a context detached from the client so that token refresh,
// billing and failover outlive it. That also means nothing cancels a quiet upstream
// when the client leaves, so the helpers below watch the client's own context.

// openAIRequestContext returns the client's request context, or nil when the
// gin context carries no request.
func openAIRequestContext(c *gin.Context) context.Context {
	if c == nil || c.Request == nil {
		return nil
	}
	return c.Request.Context()
}

// openAIClientContext returns the context whose end means the client left: the
// request context when there is one, otherwise ctx.
func openAIClientContext(ctx context.Context, c *gin.Context) context.Context {
	if requestCtx := openAIRequestContext(c); requestCtx != nil {
		return requestCtx
	}
	return ctx
}

// closeOpenAIBodyOnClientGone closes body as soon as any of the contexts ends and
// returns a function that stops the watch. The close unblocks a Read that waits on
// a quiet upstream; frames a scanner already buffered are still delivered first.
// Closing is idempotent, so overlapping watches (and the caller's own close) are
// harmless.
func closeOpenAIBodyOnClientGone(body io.Closer, contexts ...context.Context) (stop func()) {
	if body == nil {
		return func() {}
	}
	stops := make([]func() bool, 0, len(contexts))
	for _, ctx := range contexts {
		if ctx == nil || ctx.Done() == nil {
			continue
		}
		stops = append(stops, context.AfterFunc(ctx, func() { _ = body.Close() }))
	}
	return func() {
		for _, stopWatch := range stops {
			stopWatch()
		}
	}
}

// openAIClientGone reports whether any of the contexts has ended.
func openAIClientGone(contexts ...context.Context) bool {
	for _, ctx := range contexts {
		if ctx != nil && ctx.Err() != nil {
			return true
		}
	}
	return false
}
