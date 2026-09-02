package handler

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

func TestOpenAIWSIngressEndedByClient_BareNormalClosure(t *testing.T) {
	err := coderws.CloseError{Code: coderws.StatusNormalClosure, Reason: "client done"}
	require.True(t, openAIWSIngressEndedByClient(err))
}

func TestOpenAIWSIngressEndedByClient_WrappedCancellation(t *testing.T) {
	err := service.NewOpenAIWSClientCloseError(
		coderws.StatusGoingAway, "client disconnected", context.Canceled,
	)
	require.True(t, openAIWSIngressEndedByClient(fmt.Errorf("read failed: %w", err)))
}

func TestOpenAIWSIngressEndedByClient_DoesNotHideUpstreamFailure(t *testing.T) {
	err := service.NewOpenAIWSClientCloseError(
		coderws.StatusGoingAway, "upstream closed", errors.New("upstream failure"),
	)
	require.False(t, openAIWSIngressEndedByClient(err))
	require.True(t, shouldReportOpenAIWSProxyAccountFailure(err))
}
