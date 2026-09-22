//go:build unit

package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type busyAccountConcurrencyCache struct {
	fakeConcurrencyCache
	busy map[int64]bool
	err  error
}

func (c *busyAccountConcurrencyCache) AcquireAccountSlot(_ context.Context, accountID int64, _ int, _ string) (bool, error) {
	if c.err != nil {
		return false, c.err
	}
	if c.busy[accountID] {
		return false, nil
	}
	return true, nil
}

func newBusySlotHandler(t *testing.T, cache service.ConcurrencyCache) *OpenAIGatewayHandler {
	t.Helper()
	gin.SetMode(gin.TestMode)
	return &OpenAIGatewayHandler{
		gatewayService:    &service.OpenAIGatewayService{},
		concurrencyHelper: NewConcurrencyHelper(service.NewConcurrencyService(cache), SSEPingFormatClaude, 0),
	}
}

func busySlotSelection(id int64) *service.AccountSelectionResult {
	return &service.AccountSelectionResult{
		Account:  &service.Account{ID: id, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 1},
		Acquired: false,
		WaitPlan: &service.AccountWaitPlan{AccountID: id, MaxConcurrency: 1, Timeout: time.Second, MaxWaiting: 1},
	}
}

func TestAcquireOpenAIAccountSlotBusyDoesNotWriteResponse(t *testing.T) {
	h := newBusySlotHandler(t, &busyAccountConcurrencyCache{busy: map[int64]bool{11: true}})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	streamStarted := false
	groupID := int64(7)

	release, result := h.acquireResponsesAccountSlot(c, &groupID, "sess", busySlotSelection(11), false, &streamStarted, zap.NewNop())
	require.Equal(t, openAISlotAcquireBusy, result)
	require.Nil(t, release)
	require.Zero(t, w.Body.Len())
	require.False(t, c.Writer.Written())
}

func TestOpenAISlotLoopActionBusyRotatesThenExhausts(t *testing.T) {
	h := newBusySlotHandler(t, &fakeConcurrencyCache{})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	groupID := int64(7)
	failed := map[int64]struct{}{}
	veto := 0
	busySeen := false

	action := h.openAISlotLoopAction(c, openAISlotAcquireBusy, 11, &groupID, "sess", failed, &veto, false, zap.NewNop(), &busySeen)
	require.Equal(t, openAISlotLoopRotate, action)
	require.True(t, busySeen)
	require.Contains(t, failed, int64(11))
	require.Zero(t, w.Body.Len())

	action = h.openAISlotLoopAction(c, openAISlotAcquireBusy, 11, &groupID, "sess", failed, &veto, false, zap.NewNop(), &busySeen)
	require.Equal(t, openAISlotLoopStop, action)
	require.Equal(t, http.StatusTooManyRequests, w.Code)
	require.Contains(t, w.Body.String(), gatewayConcurrencyLimitCode)
	require.Contains(t, w.Body.String(), "Concurrency limit exceeded for account")
}

func TestAcquireOpenAIAccountSlotCanceledDoesNotRotate(t *testing.T) {
	h := newBusySlotHandler(t, &busyAccountConcurrencyCache{err: context.Canceled})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	streamStarted := false
	groupID := int64(7)

	release, result := h.acquireResponsesAccountSlot(c, &groupID, "sess", busySlotSelection(12), false, &streamStarted, zap.NewNop())
	require.Equal(t, openAISlotAcquireFailed, result)
	require.Nil(t, release)
	require.NotZero(t, w.Body.Len())
	require.NotContains(t, w.Body.String(), gatewayConcurrencyLimitCode)
}

func TestAcquireOpenAIAccountSlotSecondAccountSucceedsAfterBusy(t *testing.T) {
	h := newBusySlotHandler(t, &busyAccountConcurrencyCache{busy: map[int64]bool{11: true}})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	streamStarted := false
	groupID := int64(7)

	release, result := h.acquireResponsesAccountSlot(c, &groupID, "sess", busySlotSelection(11), false, &streamStarted, zap.NewNop())
	require.Equal(t, openAISlotAcquireBusy, result)
	require.Nil(t, release)
	require.Zero(t, w.Body.Len())

	release, result = h.acquireResponsesAccountSlot(c, &groupID, "sess", busySlotSelection(12), false, &streamStarted, zap.NewNop())
	require.Equal(t, openAISlotAcquireOK, result)
	require.NotNil(t, release)
	release()
	require.Zero(t, w.Body.Len())
}
