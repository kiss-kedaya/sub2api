package service

import "context"

type balancePreauthorizationContextKey struct{}
type balancePreauthorizationStreamHintContextKey struct{}

func ContextWithBalancePreauthorizationGuard(ctx context.Context, guard *BalancePreauthorizationGuard) context.Context {
	ctx = nonNilContext(ctx)
	if guard == nil {
		return ctx
	}
	return context.WithValue(ctx, balancePreauthorizationContextKey{}, guard)
}

func ContextWithoutBalancePreauthorizationGuard(ctx context.Context) context.Context {
	ctx = nonNilContext(ctx)
	return context.WithValue(ctx, balancePreauthorizationContextKey{}, (*BalancePreauthorizationGuard)(nil))
}

func BalancePreauthorizationGuardFromContext(ctx context.Context) (*BalancePreauthorizationGuard, bool) {
	if ctx == nil {
		return nil, false
	}
	guard, ok := ctx.Value(balancePreauthorizationContextKey{}).(*BalancePreauthorizationGuard)
	return guard, ok && guard != nil
}

// WithBalancePreauthorizationStreamHint attaches the transport-level streaming
// mode (e.g. Gemini :streamGenerateContent vs :generateContent, where the JSON
// body does not carry a "stream" field) so preauthorization selects the proper
// default output window when the caller omits an explicit output token limit.
func WithBalancePreauthorizationStreamHint(ctx context.Context, isStream bool) context.Context {
	ctx = nonNilContext(ctx)
	return context.WithValue(ctx, balancePreauthorizationStreamHintContextKey{}, isStream)
}

// BalancePreauthorizationStreamHintFromContext returns the streaming hint and
// whether one was explicitly attached to ctx.
func BalancePreauthorizationStreamHintFromContext(ctx context.Context) (bool, bool) {
	if ctx == nil {
		return false, false
	}
	isStream, ok := ctx.Value(balancePreauthorizationStreamHintContextKey{}).(bool)
	return isStream, ok
}

func nonNilContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
