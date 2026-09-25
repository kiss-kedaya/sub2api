package service

import (
	"context"
	"testing"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

type crossPausedCustomUsageBulkRepo struct {
	*customUsageTestRepo
	entered chan struct{}
	release chan struct{}
}

func (repo *crossPausedCustomUsageBulkRepo) BulkUpdate(ctx context.Context, ids []int64, update AccountBulkUpdate) (int64, error) {
	close(repo.entered)
	select {
	case <-repo.release:
	case <-ctx.Done():
		return 0, ctx.Err()
	}
	return repo.customUsageTestRepo.BulkUpdate(ctx, ids, update)
}

func TestCrossCustomUsageTwoInstancesPreserveLatestSecret(t *testing.T) {
	for _, clearSecret := range []bool{false, true} {
		name := "rotation"
		if clearSecret {
			name = "explicit_clear"
		}
		t.Run(name, func(t *testing.T) {
			initialService, storage, config := customUsageFixture(t)
			config.APIKey = "dummy-original-secret"
			_, err := initialService.PutConfig(t.Context(), 1, config)
			require.NoError(t, err)
			pausedRepo := &crossPausedCustomUsageBulkRepo{customUsageTestRepo: storage, entered: make(chan struct{}), release: make(chan struct{})}
			instanceA := NewCustomUsageService(pausedRepo)
			instanceB := NewCustomUsageService(storage)
			intervalEdit := config
			intervalEdit.APIKey = ""
			intervalEdit.IntervalMinutes = 15
			finished := make(chan error, 1)
			go func() {
				_, err := instanceA.PutConfig(t.Context(), 1, intervalEdit)
				finished <- err
			}()
			select {
			case <-pausedRepo.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("instance A did not reach atomic bulk write")
			}
			secretEdit := config
			secretEdit.APIKey = "dummy-rotated-secret"
			if clearSecret {
				secretEdit.APIKey = ""
				secretEdit.ClearAPIKey = true
			}
			_, err = instanceB.PutConfig(t.Context(), 1, secretEdit)
			close(pausedRepo.release)
			require.NoError(t, err)
			conflict := <-finished
			require.Equal(t, 409, infraerrors.Code(conflict))
			require.Equal(t, "CUSTOM_USAGE_CONFIG_CHANGED", infraerrors.Reason(conflict))
			account, err := storage.GetByID(t.Context(), 1)
			require.NoError(t, err)
			_, secret, _ := readCustomUsage(account)
			require.Equal(t, secretEdit.APIKey, secret.APIKey, "blank-secret save on another instance must not undo rotation or explicit clearing")
		})
	}
}
