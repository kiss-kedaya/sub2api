//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestGroupReturnRequestedModelPersistsAndReachesAuthProjection(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	client := tx.Client()
	groupRepo := newGroupRepositoryWithSQL(client, tx)
	group := &service.Group{Name: "response-model-projection", Platform: service.PlatformOpenAI,
		Status: service.StatusActive, SubscriptionType: service.SubscriptionTypeStandard,
		RateMultiplier: 1, ReturnRequestedModel: true}
	require.NoError(t, groupRepo.Create(ctx, group))
	created, err := groupRepo.GetByIDLite(ctx, group.ID)
	require.NoError(t, err)
	require.True(t, created.ReturnRequestedModel, "create must persist opt-in before any update")
	user := mustCreateUser(t, client, &service.User{Email: "response-model-projection@example.com", Concurrency: 5})
	apiKeyRepo := newAPIKeyRepositoryWithSQL(client, tx)
	key := &service.APIKey{UserID: user.ID, GroupID: &group.ID, Key: "sk-model-fixture", Name: "fixture", Status: service.StatusActive}
	require.NoError(t, apiKeyRepo.Create(ctx, key))

	for _, enabled := range []bool{true, false} {
		group.ReturnRequestedModel = enabled
		require.NoError(t, groupRepo.Update(ctx, group))
		lite, err := groupRepo.GetByIDLite(ctx, group.ID)
		require.NoError(t, err)
		require.Equal(t, enabled, lite.ReturnRequestedModel)
		loaded, err := groupRepo.GetByID(ctx, group.ID)
		require.NoError(t, err)
		require.Equal(t, enabled, loaded.ReturnRequestedModel)
		auth, err := apiKeyRepo.GetByKeyForAuth(ctx, key.Key)
		require.NoError(t, err)
		require.NotNil(t, auth.Group)
		require.True(t, auth.Group.Hydrated)
		require.Equal(t, enabled, auth.Group.ReturnRequestedModel)
		var stored bool
		require.NoError(t, scanSingleRow(ctx, tx, "SELECT return_requested_model FROM groups WHERE id=$1", []any{group.ID}, &stored))
		require.Equal(t, enabled, stored)
	}
}

func TestGroupReturnRequestedModelInvalidatesDurableAuthSnapshotsOnlyOnChange(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	client := tx.Client()
	group, err := client.Group.Create().SetName("response-model-invalidation").Save(ctx)
	require.NoError(t, err)
	user := mustCreateUser(t, client, &service.User{Email: "response-model-invalidation@example.com", Concurrency: 5})
	key := &service.APIKey{UserID: user.ID, GroupID: &group.ID, Key: "sk-model-invalidate", Name: "fixture", Status: service.StatusActive}
	require.NoError(t, newAPIKeyRepositoryWithSQL(client, tx).Create(ctx, key))
	countInvalidations := func() int {
		var count int
		require.NoError(t, scanSingleRow(ctx, tx,
			"SELECT COUNT(*) FROM auth_cache_invalidation_outbox WHERE cache_key=encode(sha256(convert_to($1,'UTF8')),'hex')",
			[]any{key.Key}, &count))
		return count
	}
	require.Zero(t, countInvalidations())
	for _, enabled := range []bool{true, false} {
		_, err := client.Group.UpdateOneID(group.ID).SetReturnRequestedModel(enabled).Save(ctx)
		require.NoError(t, err)
		require.Equal(t, 1, countInvalidations(), "changing the response policy must enqueue durable invalidation")
		_, err = tx.ExecContext(ctx,
			"DELETE FROM auth_cache_invalidation_outbox WHERE cache_key=encode(sha256(convert_to($1,'UTF8')),'hex')", key.Key)
		require.NoError(t, err)
		_, err = client.Group.UpdateOneID(group.ID).SetReturnRequestedModel(enabled).Save(ctx)
		require.NoError(t, err)
		require.Zero(t, countInvalidations(), "same-value policy updates must retain existing no-op behavior")
	}
	_, err = client.Group.UpdateOneID(group.ID).SetName("response-model-cosmetic").Save(ctx)
	require.NoError(t, err)
	require.Zero(t, countInvalidations(), "cosmetic updates must retain existing no-op behavior")
}

func TestGroupReturnRequestedModelDefaultsFalse(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	schemaTx := testTx(t)
	requireColumn(t, schemaTx, "groups", "return_requested_model", "boolean", 0, false)
	requireColumnDefaultContains(t, schemaTx, "groups", "return_requested_model", "false")
	group, err := tx.Client().Group.Create().SetName("response-model-default").Save(ctx)
	require.NoError(t, err)
	require.False(t, group.ReturnRequestedModel)
	var stored bool
	require.NoError(t, scanSingleRow(ctx, tx, "SELECT return_requested_model FROM groups WHERE id=$1", []any{group.ID}, &stored))
	require.False(t, stored)
}
