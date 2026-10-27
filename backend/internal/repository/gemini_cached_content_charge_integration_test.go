//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestGeminiCachedContentDurableChargeVisibilityAndMutation(t *testing.T) {
	ctx := context.Background()
	repo := &geminiCachedContentRepository{sql: testTx(t)}
	baseline := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	intent := &service.MediaBillingIntent{Command: service.UsageBillingCommand{RequestID: "gcache:create:test", UserID: 1, APIKeyID: 910055, AccountID: 99}, Usage: service.UsageLog{RequestID: "gcache:create:test", UserID: 1, APIKeyID: 910055, AccountID: 99}}
	record := &service.GeminiCachedContent{PublicID: "charged" + time.Now().Format("150405000000"), UserID: 1, APIKeyID: 910055, GroupID: 42, AccountID: 99, UpstreamName: "cachedContents/synthetic", Model: "gemini-2.5-pro", UpstreamModel: "models/gemini-2.5-pro", ExpireTime: baseline, BillingPending: true, ChargeIntent: intent, ChargeTargetExpire: &baseline}
	require.NoError(t, repo.Create(ctx, record))
	loaded, err := repo.GetForOwner(ctx, record.APIKeyID, record.PublicID)
	require.NoError(t, err)
	require.True(t, loaded.BillingPending)
	require.Equal(t, intent.Command.RequestID, loaded.ChargeIntent.Command.RequestID)
	list, err := repo.ListForOwner(ctx, record.APIKeyID, record.GroupID, time.Now(), 0, 10)
	require.NoError(t, err)
	require.Empty(t, list, "unsettled creation is absent from public listing")
	acquired, err := repo.BeginMutation(ctx, record.ID, baseline, "cannot-change", time.Minute)
	require.NoError(t, err)
	require.False(t, acquired)
	require.ErrorIs(t, repo.CompleteCharge(ctx, record.ID, "foreign-charge"), service.ErrGeminiCachedContentMutationConflict)
	require.NoError(t, repo.CompleteCharge(ctx, record.ID, intent.Command.RequestID))
	require.NoError(t, repo.CompleteCharge(ctx, record.ID, intent.Command.RequestID), "concurrent recovery completion is idempotent")
	loaded, err = repo.GetForOwner(ctx, record.APIKeyID, record.PublicID)
	require.NoError(t, err)
	require.False(t, loaded.BillingPending)
	require.Nil(t, loaded.ChargeIntent)
	acquired, err = repo.BeginMutation(ctx, record.ID, baseline, "patch-token", time.Minute)
	require.NoError(t, err)
	require.True(t, acquired)
	target := baseline.Add(time.Hour)
	patchIntent := *intent
	patchIntent.Command.RequestID = "gcache:patch:test:unique"
	patchIntent.Usage.RequestID = patchIntent.Command.RequestID
	require.NoError(t, repo.CommitChargedMutation(ctx, record.ID, "patch-token", target, &patchIntent))
	loaded, err = repo.GetForOwner(ctx, record.APIKeyID, record.PublicID)
	require.NoError(t, err)
	require.True(t, loaded.ExpireTime.Equal(baseline), "target and intent are saved together while the old expiration remains committed")
	require.True(t, loaded.ChargeTargetExpire.Equal(target))
	require.True(t, loaded.BillingPending)
	require.Equal(t, patchIntent.Command.RequestID, loaded.ChargeIntent.Command.RequestID)
	require.ErrorIs(t, repo.CompleteCharge(ctx, record.ID, intent.Command.RequestID), service.ErrGeminiCachedContentMutationConflict, "old create completion cannot unlock a newer unpaid extension")
	require.NoError(t, repo.CompleteCharge(ctx, record.ID, patchIntent.Command.RequestID))
	loaded, err = repo.GetForOwner(ctx, record.APIKeyID, record.PublicID)
	require.NoError(t, err)
	require.True(t, loaded.ExpireTime.Equal(target))
	require.False(t, loaded.BillingPending)
	list, err = repo.ListForOwner(ctx, record.APIKeyID, record.GroupID, time.Now(), 0, 10)
	require.NoError(t, err)
	require.Len(t, list, 1)
}
