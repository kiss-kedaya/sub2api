package handler

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/wallet"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"

	"github.com/gin-gonic/gin"
)

// cryptoAuthMethodContextKey is where the admin auth middleware records how the
// caller authenticated (jwt / scoped admin API key). Kept local to avoid
// widening the middleware surface for one audit field.
const cryptoAuthMethodContextKey = "auth_method"

// cryptoAuditContext attaches the authenticated admin identity and request
// provenance to the context passed to the wallet audit writer. Attribution is
// best-effort: any field that cannot be resolved is stored as NULL, and a
// missing identity never fails a signer operation.
func cryptoAuditContext(c *gin.Context) context.Context {
	requestContext := c.Request.Context()
	actor := wallet.AuditActor{
		Email:      c.GetString(middleware.ContextKeyAuthEmail),
		ClientIP:   middleware.SecurityClientIP(c),
		UserAgent:  c.Request.UserAgent(),
		RequestID:  cryptoRequestID(requestContext),
		AuthMethod: c.GetString(cryptoAuthMethodContextKey),
	}
	if subject, ok := middleware.GetAuthSubjectFromContext(c); ok && subject.UserID > 0 {
		userID := subject.UserID
		actor.UserID = &userID
	}
	return wallet.WithAuditActor(requestContext, actor)
}

func cryptoRequestID(ctx context.Context) string {
	requestID, _ := ctx.Value(ctxkey.RequestID).(string)
	return requestID
}
