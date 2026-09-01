package handler

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpsErrorLogConfigBoundsPerProcessDatabaseWriters(t *testing.T) {
	workers, queueSize := opsErrorLogConfig()
	require.GreaterOrEqual(t, workers, opsErrorLogMinWorkerCount)
	require.LessOrEqual(t, workers, opsErrorLogMaxWorkerCount)
	require.GreaterOrEqual(t, queueSize, opsErrorLogMinQueueSize)
	require.LessOrEqual(t, queueSize, opsErrorLogMaxQueueSize)
	require.GreaterOrEqual(t, runtime.GOMAXPROCS(0), 1)
}
