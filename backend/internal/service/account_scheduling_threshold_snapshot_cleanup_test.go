//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type missingThresholdSnapshotCleanerRepo struct {
	AccountRepository
}

func TestClearAccountSchedulingThresholdSnapshots_RequiresRepositorySupport(t *testing.T) {
	err := clearAccountSchedulingThresholdSnapshots(context.Background(), missingThresholdSnapshotCleanerRepo{}, 1)

	require.Error(t, err)
	require.Contains(t, err.Error(), "does not support account scheduling threshold snapshot cleanup")
}
