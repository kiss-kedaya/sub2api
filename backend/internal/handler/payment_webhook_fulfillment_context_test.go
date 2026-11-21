//go:build unit

package handler

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestDetachWebhookFulfillmentContextSurvivesRequestCancellation locks in the
// handler-side half of the order-11380 fix: once the callback has been verified the
// money movement runs on a context that is not canceled when the provider/browser
// closes the connection.
func TestDetachWebhookFulfillmentContextSurvivesRequestCancellation(t *testing.T) {
	type ctxKey struct{}
	requestCtx, cancelRequest := context.WithCancel(context.WithValue(context.Background(), ctxKey{}, "request-value"))
	cancelRequest() // provider or browser gave up waiting

	fulfillmentCtx, cancelFulfillment := detachWebhookFulfillmentContext(requestCtx)
	defer cancelFulfillment()

	require.NoError(t, fulfillmentCtx.Err(), "money movement must not inherit request cancellation")
	require.Equal(t, "request-value", fulfillmentCtx.Value(ctxKey{}))

	deadline, ok := fulfillmentCtx.Deadline()
	require.True(t, ok, "detached context must carry its own timeout")
	require.LessOrEqual(t, time.Until(deadline), paymentWebhookFulfillmentTimeout)
}
