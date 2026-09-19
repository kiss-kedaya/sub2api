package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestMediaSettlementContextRequiresRecordedSuccess(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	require.False(t, mediaSettlementRequired(c))
	require.ErrorIs(t, mediaSettlementResult(c), errMediaSettlementNotRecorded)

	requireMediaSettlement(c)
	require.True(t, mediaSettlementRequired(c))
	require.ErrorIs(t, mediaSettlementResult(c), errMediaSettlementNotRecorded)

	recordMediaSettlementResult(c, nil)
	require.NoError(t, mediaSettlementResult(c))
}

func TestMediaSettlementContextPreservesFailure(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	requireMediaSettlement(c)
	want := errors.New("settlement failed")

	recordMediaSettlementResult(c, want)

	require.ErrorIs(t, mediaSettlementResult(c), want)
}

func TestDispatchMediaSettlementUsesExistingWorkerForOrdinaryRequests(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	syncCalls := 0
	asyncCalls := 0

	dispatchMediaSettlement(c, func(context.Context) error {
		syncCalls++
		return nil
	}, func() {
		asyncCalls++
	})

	require.Zero(t, syncCalls)
	require.Equal(t, 1, asyncCalls)
}

func TestDispatchMediaSettlementRecordsSynchronousResultWhenRequired(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	requireMediaSettlement(c)
	want := errors.New("durable settlement pending")
	syncCalls := 0
	asyncCalls := 0

	dispatchMediaSettlement(c, func(context.Context) error {
		syncCalls++
		return want
	}, func() {
		asyncCalls++
	})

	require.Equal(t, 1, syncCalls)
	require.Zero(t, asyncCalls)
	require.ErrorIs(t, mediaSettlementResult(c), want)
}
