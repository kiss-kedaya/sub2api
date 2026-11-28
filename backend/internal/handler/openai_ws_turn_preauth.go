package handler

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/Wei-Shaw/sub2api/internal/service"
	coderws "github.com/coder/websocket"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// openAIWSTurnPreauth owns per-turn live_wallet preauthorization guards for one
// Responses WebSocket connection. Each admission attempt gets a unique request
// ID (`ws:<connID>:turn:<turn>:<seq>`) so failover retries after a turn refund
// never collide with a Refunded billing_balance_settlements row.
type openAIWSTurnPreauth struct {
	mu     sync.Mutex
	connID string
	seq    uint64
	guards map[int]*service.BalancePreauthorizationGuard
}

func newOpenAIWSTurnPreauth(connID string) *openAIWSTurnPreauth {
	connID = strings.TrimSpace(connID)
	if connID == "" {
		connID = uuid.NewString()
	}
	return &openAIWSTurnPreauth{
		connID: connID,
		guards: make(map[int]*service.BalancePreauthorizationGuard, 2),
	}
}

func (m *openAIWSTurnPreauth) nextRequestID(turn int) string {
	if m == nil {
		return fmt.Sprintf("ws:%s:turn:%d:1", uuid.NewString(), turn)
	}
	m.mu.Lock()
	m.seq++
	seq := m.seq
	connID := m.connID
	m.mu.Unlock()
	return fmt.Sprintf("ws:%s:turn:%d:%d", connID, turn, seq)
}

func (m *openAIWSTurnPreauth) has(turn int) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	g := m.guards[turn]
	m.mu.Unlock()
	return g != nil && g.IsCurrentOwner()
}

func (m *openAIWSTurnPreauth) set(reqLog *zap.Logger, turn int, guard *service.BalancePreauthorizationGuard) {
	if m == nil {
		if guard != nil {
			deferBalancePreauthorizationRefund(reqLog, guard)
		}
		return
	}
	var prev *service.BalancePreauthorizationGuard
	m.mu.Lock()
	if existing := m.guards[turn]; existing != nil && existing != guard {
		prev = existing
	}
	if guard == nil {
		delete(m.guards, turn)
	} else {
		m.guards[turn] = guard
	}
	m.mu.Unlock()
	if prev != nil {
		deferBalancePreauthorizationRefund(reqLog, prev)
	}
}

func (m *openAIWSTurnPreauth) contextForTurn(turn int, base context.Context) context.Context {
	if m == nil {
		return service.ContextWithoutBalancePreauthorizationGuard(base)
	}
	m.mu.Lock()
	guard := m.guards[turn]
	m.mu.Unlock()
	if guard == nil {
		return service.ContextWithoutBalancePreauthorizationGuard(base)
	}
	return service.ContextWithBalancePreauthorizationGuard(base, guard)
}

// refund deletes turn's entry and refunds its guard if ownership has not been
// transferred to a settlement task. After TransferToWorker, Refund is a no-op.
func (m *openAIWSTurnPreauth) refund(reqLog *zap.Logger, turn int) {
	if m == nil {
		return
	}
	m.mu.Lock()
	guard := m.guards[turn]
	delete(m.guards, turn)
	m.mu.Unlock()
	if guard != nil {
		deferBalancePreauthorizationRefund(reqLog, guard)
	}
}

func (m *openAIWSTurnPreauth) refundAll(reqLog *zap.Logger) {
	if m == nil {
		return
	}
	m.mu.Lock()
	guards := make([]*service.BalancePreauthorizationGuard, 0, len(m.guards))
	for turn, guard := range m.guards {
		if guard != nil {
			guards = append(guards, guard)
		}
		delete(m.guards, turn)
	}
	m.mu.Unlock()
	for _, guard := range guards {
		deferBalancePreauthorizationRefund(reqLog, guard)
	}
}

func openAIWSPreauthorizationCloseStatusAndReason(err error) (coderws.StatusCode, string) {
	if errors.Is(err, service.ErrBalanceWithholdingFailed) {
		return coderws.StatusPolicyViolation, "insufficient balance"
	}
	return coderws.StatusInternalError, "billing check failed"
}

func openAIWSBalanceAbortCloseStatusAndReason(err error) (coderws.StatusCode, string) {
	if errors.Is(err, service.ErrBalanceWithholdingFailed) {
		return coderws.StatusPolicyViolation, "insufficient balance"
	}
	return coderws.StatusTryAgainLater, "billing service temporarily unavailable"
}
