package handler

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	coderws "github.com/coder/websocket"
)

const openAIWSTurnSettlementTimeout = 10 * time.Second

type openAIWSTurnBillingGuard struct {
	mu            sync.Mutex
	settleTimeout time.Duration
	admittedTurn  int
	finishedTurn  int
	settlementErr error
}

func newOpenAIWSTurnBillingGuard(settleTimeout time.Duration) *openAIWSTurnBillingGuard {
	if settleTimeout <= 0 {
		settleTimeout = openAIWSTurnSettlementTimeout
	}
	return &openAIWSTurnBillingGuard{settleTimeout: settleTimeout}
}

func (g *openAIWSTurnBillingGuard) BeforeTurn(ctx context.Context, turn int, admit func(context.Context) error) error {
	if g == nil {
		return service.NewOpenAIWSClientCloseError(coderws.StatusInternalError, "websocket billing guard is unavailable", nil)
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.settlementErr != nil {
		return service.NewOpenAIWSClientCloseError(
			coderws.StatusInternalError,
			"previous turn billing failed; please reconnect later",
			g.settlementErr,
		)
	}
	if turn <= g.admittedTurn {
		return nil
	}
	if turn > 1 && g.finishedTurn < turn-1 {
		return service.NewOpenAIWSClientCloseError(
			coderws.StatusInternalError,
			"previous turn billing is incomplete; please reconnect later",
			fmt.Errorf("turn %d started before turn %d billing finished", turn, turn-1),
		)
	}
	if admit != nil {
		if err := admit(ctx); err != nil {
			return err
		}
	}
	g.admittedTurn = turn
	return nil
}

func (g *openAIWSTurnBillingGuard) FinishTurn(parent context.Context, turn int, settle func(context.Context) error) error {
	if g == nil {
		return errors.New("websocket billing guard is unavailable")
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	if turn <= g.finishedTurn {
		return g.settlementErr
	}
	if g.settlementErr != nil {
		return g.settlementErr
	}
	if settle == nil {
		g.finishedTurn = turn
		return nil
	}

	base := context.Background()
	if parent != nil {
		base = context.WithoutCancel(parent)
	}
	settleCtx, cancel := context.WithTimeout(base, g.settleTimeout)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				done <- fmt.Errorf("websocket turn billing panic: %v", recovered)
			}
		}()
		done <- settle(settleCtx)
	}()

	select {
	case err := <-done:
		if err != nil {
			g.settlementErr = err
			return err
		}
		g.finishedTurn = turn
		return nil
	case <-settleCtx.Done():
		g.settlementErr = fmt.Errorf("websocket turn billing timed out: %w", settleCtx.Err())
		return g.settlementErr
	}
}
