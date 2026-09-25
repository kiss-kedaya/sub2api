//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountCustomUsageStaleEditDoesNotUndoRotation(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	repo := newAccountRepositoryWithSQL(tx.Client(), tx, nil)
	account := mustCreateAccount(t, tx.Client(), &service.Account{
		Name: "custom-usage-rotation", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "gateway-key", "base_url": "https://upstream.example.com", service.CustomUsageCredentialsKey: map[string]any{"api_key": "old-private-key"}},
		Extra:       map[string]any{service.CustomUsageExtraKey: map[string]any{"enabled": false}},
		Status:      service.StatusActive, Schedulable: true, Concurrency: 1, Priority: 1,
	})
	stale, err := repo.GetByID(ctx, account.ID)
	require.NoError(t, err)
	expected := &service.AccountCustomUsageExpected{Credentials: stale.Credentials, Config: stale.Extra[service.CustomUsageExtraKey]}
	updatedSecrets := map[string]any{"api_key": "rotated-private-key"}
	updatedConfig := map[string]any{"enabled": true, "template": "general"}
	count, err := repo.BulkUpdate(ctx, []int64{account.ID}, service.AccountBulkUpdate{
		Credentials: map[string]any{service.CustomUsageCredentialsKey: updatedSecrets},
		Extra:       map[string]any{service.CustomUsageExtraKey: updatedConfig},
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), count)
	count, err = repo.BulkUpdate(ctx, []int64{account.ID}, service.AccountBulkUpdate{
		Credentials:         map[string]any{service.CustomUsageCredentialsKey: map[string]any{"api_key": "stale-private-key"}},
		CustomUsageExpected: expected,
	})
	require.NoError(t, err)
	require.Zero(t, count)
	stale.Name = "renamed-after-rotation"
	require.NoError(t, repo.Update(ctx, stale))
	current, err := repo.GetByID(ctx, account.ID)
	require.NoError(t, err)
	require.Equal(t, stale.Name, current.Name)
	require.Equal(t, updatedSecrets, current.Credentials[service.CustomUsageCredentialsKey])
	require.Equal(t, updatedConfig, current.Extra[service.CustomUsageExtraKey])
	require.NoError(t, repo.UpdateCredentials(ctx, account.ID, map[string]any{"api_key": "refreshed-gateway-key", service.CustomUsageCredentialsKey: map[string]any{"api_key": "stale-private-key"}}))
	current, err = repo.GetByID(ctx, account.ID)
	require.NoError(t, err)
	require.Equal(t, "refreshed-gateway-key", current.Credentials["api_key"])
	require.Equal(t, updatedSecrets, current.Credentials[service.CustomUsageCredentialsKey])
	require.Equal(t, updatedConfig, current.Extra[service.CustomUsageExtraKey])
}
