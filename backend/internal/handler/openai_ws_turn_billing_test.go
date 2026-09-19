//go:build unit

package handler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

func TestOpenAIWSTurnBillingGuardChecksEachTurnOnce(t *testing.T) {
	guard := newOpenAIWSTurnBillingGuard(time.Second)
	checks := 0
	check := func(context.Context) error {
		checks++
		return nil
	}

	require.NoError(t, guard.BeforeTurn(context.Background(), 1, check))
	guard.FinishTurn(context.Background(), 1, nil)
	require.NoError(t, guard.BeforeTurn(context.Background(), 2, check))
	require.NoError(t, guard.BeforeTurn(context.Background(), 2, check))
	require.Equal(t, 2, checks)
}

func TestOpenAIWSTurnBillingGuardFailsClosedAfterSettlementError(t *testing.T) {
	guard := newOpenAIWSTurnBillingGuard(time.Second)
	require.NoError(t, guard.BeforeTurn(context.Background(), 1, func(context.Context) error { return nil }))
	guard.FinishTurn(context.Background(), 1, func(context.Context) error { return errors.New("ledger unavailable") })

	err := guard.BeforeTurn(context.Background(), 2, func(context.Context) error {
		t.Fatal("eligibility must not run after settlement failure")
		return nil
	})
	require.Error(t, err)
	require.ErrorContains(t, err, "previous turn billing failed")
	var closeErr *service.OpenAIWSClientCloseError
	require.ErrorAs(t, err, &closeErr)
	require.Equal(t, coderws.StatusInternalError, closeErr.StatusCode())
}

func TestOpenAIWSTurnBillingGuardPreservesExplicitEligibilityRejection(t *testing.T) {
	guard := newOpenAIWSTurnBillingGuard(time.Second)
	want := service.NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "billing check failed", errors.New("balance exhausted"))

	err := guard.BeforeTurn(context.Background(), 1, func(context.Context) error { return want })
	require.ErrorIs(t, err, want)
	var closeErr *service.OpenAIWSClientCloseError
	require.ErrorAs(t, err, &closeErr)
	require.Equal(t, coderws.StatusPolicyViolation, closeErr.StatusCode())
	require.Equal(t, "billing check failed", closeErr.Reason())
}

func TestOpenAIWSTurnBillingGuardFailsClosedWhenSettlementTimesOut(t *testing.T) {
	guard := newOpenAIWSTurnBillingGuard(20 * time.Millisecond)
	require.NoError(t, guard.BeforeTurn(context.Background(), 1, func(context.Context) error { return nil }))
	guard.FinishTurn(context.Background(), 1, func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})

	err := guard.BeforeTurn(context.Background(), 2, func(context.Context) error { return nil })
	require.Error(t, err)
	var closeErr *service.OpenAIWSClientCloseError
	require.ErrorAs(t, err, &closeErr)
	require.Equal(t, coderws.StatusInternalError, closeErr.StatusCode())
}
