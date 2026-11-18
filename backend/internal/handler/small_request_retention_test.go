package handler

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/requestcapture"
	"github.com/stretchr/testify/require"
)

func TestSmallRetentionHandleSurvivesDetachedUsageContext(t *testing.T) {
	h := &requestcapture.SmallHandle{}
	parent, cancel := context.WithCancel(requestcapture.WithSmallHandle(context.Background(), h))
	cancel()
	ctx := usageRecordContext(parent, context.Background())
	require.Same(t, h, requestcapture.SmallFromContext(ctx))
	require.NoError(t, ctx.Err())
	plain := context.Background()
	require.Nil(t, requestcapture.SmallFromContext(usageRecordContext(plain, plain)))
}
