package service

import "context"

type balancePreauthorizationContextKey struct{}

func ContextWithBalancePreauthorizationGuard(ctx context.Context, guard *BalancePreauthorizationGuard) context.Context {
	ctx = nonNilContext(ctx)
	if guard == nil {
		return ctx
	}
	return context.WithValue(ctx, balancePreauthorizationContextKey{}, guard)
}

func BalancePreauthorizationGuardFromContext(ctx context.Context) (*BalancePreauthorizationGuard, bool) {
	if ctx == nil {
		return nil, false
	}
	guard, ok := ctx.Value(balancePreauthorizationContextKey{}).(*BalancePreauthorizationGuard)
	return guard, ok && guard != nil
}

func nonNilContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
