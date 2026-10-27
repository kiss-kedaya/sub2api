// Adapted from ranxi2001/sub2api @ 9620d315, LGPL-3.0-or-later.
package service

import "context"

// Stateful SDK turns must see downstream cancellation so that the next turn
// cannot race a still-running agent. Ordinary providers retain usage draining.
func openAIPassthroughContext(ctx context.Context, account *Account) (context.Context, context.CancelFunc) {
	if account.IsCopilotSDKEnabled() && ctx != nil {
		return ctx, func() {}
	}
	return detachUpstreamContext(ctx)
}
