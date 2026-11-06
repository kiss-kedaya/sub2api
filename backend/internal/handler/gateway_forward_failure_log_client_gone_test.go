//go:build unit

package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func newClientGoneLogContext(t *testing.T, canceled bool) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	ctx, cancel := context.WithCancel(context.Background())
	if canceled {
		cancel()
	} else {
		t.Cleanup(cancel)
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil).WithContext(ctx)
	return c
}

// The upstream is closed the moment the client leaves, so an aborted request ends
// with a cancellation error. That is the client's doing, not a gateway failure:
// it must not produce an ERROR line with a stack for every Esc press.
func TestLogGatewayForwardFailure_DepartedClientIsInfoNotError(t *testing.T) {
	core, observed := observer.New(zapcore.DebugLevel)
	log := zap.New(core, zap.AddStacktrace(zapcore.ErrorLevel))
	c := newClientGoneLogContext(t, true)

	logGatewayForwardFailure(log, c, "gateway.forward_failed",
		fmt.Errorf("stream usage incomplete after disconnect: %w", context.Canceled), zap.Int64("account_id", 7))

	entries := observed.All()
	require.Len(t, entries, 1)
	require.Equal(t, zapcore.InfoLevel, entries[0].Level)
	require.Equal(t, "gateway.forward_failed.client_gone", entries[0].Message)
	require.Empty(t, entries[0].Stack)
	require.EqualValues(t, 7, entries[0].ContextMap()["account_id"])
}

// A cancellation while the client is still there (for example the upstream lease
// ended) and a real upstream failure keep their ERROR level.
func TestLogGatewayForwardFailure_NonClientCancellationStaysError(t *testing.T) {
	for name, tc := range map[string]struct {
		canceled bool
		err      error
	}{
		"cancellation while the client is attached": {false, fmt.Errorf("stream usage incomplete: %w", context.Canceled)},
		"upstream fault after the client left":      {true, errors.New("upstream stream disconnected")},
		"failover error after the client left":      {true, &service.UpstreamFailoverError{StatusCode: http.StatusBadGateway}},
	} {
		t.Run(name, func(t *testing.T) {
			core, observed := observer.New(zapcore.DebugLevel)
			log := zap.New(core, zap.AddStacktrace(zapcore.ErrorLevel))

			logGatewayForwardFailure(log, newClientGoneLogContext(t, tc.canceled), "gateway.forward_failed", tc.err)

			entries := observed.All()
			require.Len(t, entries, 1)
			require.Equal(t, zapcore.ErrorLevel, entries[0].Level)
			require.Equal(t, "gateway.forward_failed", entries[0].Message)
		})
	}
}
