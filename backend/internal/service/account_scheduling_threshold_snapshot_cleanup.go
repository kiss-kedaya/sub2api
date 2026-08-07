package service

import (
	"context"
	"fmt"
)

type accountSchedulingThresholdSnapshotCleaner interface {
	ClearAccountSchedulingThresholdSnapshots(ctx context.Context, id int64) error
}

func clearAccountSchedulingThresholdSnapshots(ctx context.Context, repo AccountRepository, id int64) error {
	cleaner, ok := repo.(accountSchedulingThresholdSnapshotCleaner)
	if !ok {
		return fmt.Errorf("account repository does not support account scheduling threshold snapshot cleanup")
	}
	return cleaner.ClearAccountSchedulingThresholdSnapshots(ctx, id)
}
