package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/ent/group"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUserSubscriptionRepository_ListHydratesGroupAndDailyResetMode_SQLite(t *testing.T) {
	apiKeyRepo, client := newAPIKeyRepoSQLite(t)
	subRepo := NewUserSubscriptionRepository(client)
	ctx := context.Background()

	user1 := mustCreateAPIKeyRepoUser(t, ctx, client, "sub-list-hydrate-1@test.com")
	user2 := mustCreateAPIKeyRepoUser(t, ctx, client, "sub-list-hydrate-2@test.com")
	user3 := mustCreateAPIKeyRepoUser(t, ctx, client, "sub-list-hydrate-3@test.com")

	anchoredGroup, err := client.Group.Create().
		SetName("g-anchored-24h-list").
		SetPlatform(service.PlatformAnthropic).
		SetStatus(service.StatusActive).
		SetSubscriptionType(service.SubscriptionTypeSubscription).
		SetDailyResetMode(service.DailyResetModeAnchored24h).
		SetRateMultiplier(1).
		Save(ctx)
	require.NoError(t, err)

	softDeletedGroup, err := client.Group.Create().
		SetName("g-anchored-24h-soft-deleted").
		SetPlatform(service.PlatformOpenAI).
		SetStatus(service.StatusActive).
		SetSubscriptionType(service.SubscriptionTypeSubscription).
		SetDailyResetMode(service.DailyResetModeAnchored24h).
		SetRateMultiplier(1).
		Save(ctx)
	require.NoError(t, err)

	now := time.Now().UTC().Truncate(time.Second)
	startsAt := now.Add(-2 * time.Hour)
	expiresAt := startsAt.Add(48 * time.Hour)

	activeSub := &service.UserSubscription{
		UserID:           user1.ID,
		GroupID:          anchoredGroup.ID,
		StartsAt:         startsAt,
		ExpiresAt:        expiresAt,
		Status:           service.SubscriptionStatusActive,
		DailyWindowStart: &startsAt,
	}
	require.NoError(t, subRepo.Create(ctx, activeSub))

	revokedSub := &service.UserSubscription{
		UserID:           user2.ID,
		GroupID:          softDeletedGroup.ID,
		StartsAt:         startsAt,
		ExpiresAt:        expiresAt,
		Status:           service.SubscriptionStatusActive,
		DailyWindowStart: &startsAt,
	}
	require.NoError(t, subRepo.Create(ctx, revokedSub))
	require.NoError(t, subRepo.Delete(ctx, revokedSub.ID))

	expiredSubOnDeletedGroup := &service.UserSubscription{
		UserID:           user3.ID,
		GroupID:          softDeletedGroup.ID,
		StartsAt:         startsAt.Add(-72 * time.Hour),
		ExpiresAt:        startsAt.Add(-24 * time.Hour),
		Status:           service.SubscriptionStatusExpired,
		DailyWindowStart: &startsAt,
	}
	require.NoError(t, subRepo.Create(ctx, expiredSubOnDeletedGroup))

	// Soft-delete the second group after creating subscriptions.
	_, err = client.Group.Delete().Where(group.IDEQ(softDeletedGroup.ID)).Exec(ctx)
	require.NoError(t, err)

	params := pagination.PaginationParams{Page: 1, PageSize: 20}

	// 1. Admin list with empty status (includeSoftDeleted == true): every subscription must have Group hydrated.
	allSubs, pag, err := subRepo.List(ctx, params, nil, nil, "", "", "", "")
	require.NoError(t, err)
	require.Equal(t, int64(3), pag.Total)
	byID := make(map[int64]*service.UserSubscription, len(allSubs))
	for i := range allSubs {
		byID[allSubs[i].ID] = &allSubs[i]
	}
	require.NotNil(t, byID[activeSub.ID].Group)
	require.Equal(t, service.DailyResetModeAnchored24h, byID[activeSub.ID].Group.DailyResetMode)
	nextReset := byID[activeSub.ID].NextDailyResetAtAt(now)
	require.NotNil(t, nextReset)
	require.True(t, startsAt.Add(24*time.Hour).Equal(*nextReset), "admin list must preserve anchored_24h next reset, not calendar midnight")

	require.NotNil(t, byID[revokedSub.ID].Group, "revoked subscription on soft-deleted group must still hydrate Group")
	require.Equal(t, service.DailyResetModeAnchored24h, byID[revokedSub.ID].Group.DailyResetMode)
	require.Nil(t, byID[revokedSub.ID].NextDailyResetAtAt(now), "revoked subscription must omit next daily reset")

	// 2. Admin list with revoked status (includeSoftDeleted == true): must hydrate soft-deleted Group.
	revokedSubs, _, err := subRepo.List(ctx, params, nil, nil, service.SubscriptionStatusRevoked, "", "", "")
	require.NoError(t, err)
	require.Len(t, revokedSubs, 1)
	require.NotNil(t, revokedSubs[0].Group)
	require.Equal(t, service.DailyResetModeAnchored24h, revokedSubs[0].Group.DailyResetMode)

	// 3. Admin list with expired status (includeSoftDeleted == false): attachMissingGroups hydrates soft-deleted Group.
	expiredSubs, _, err := subRepo.List(ctx, params, nil, nil, service.SubscriptionStatusExpired, "", "", "")
	require.NoError(t, err)
	require.Len(t, expiredSubs, 1)
	require.NotNil(t, expiredSubs[0].Group)
	require.Equal(t, service.DailyResetModeAnchored24h, expiredSubs[0].Group.DailyResetMode)
	require.Nil(t, expiredSubs[0].NextDailyResetAtAt(now))

	// 4. GetByKeyForAuth preserves DailyResetMode on the projected Group.
	gid := anchoredGroup.ID
	key := &service.APIKey{
		UserID:  user1.ID,
		GroupID: &gid,
		Key:     "test-anchored-list-key-fixture",
		Name:    "anchored-auth-key",
		Status:  service.StatusActive,
	}
	require.NoError(t, apiKeyRepo.Create(ctx, key))
	loadedKey, err := apiKeyRepo.GetByKeyForAuth(ctx, key.Key)
	require.NoError(t, err)
	require.NotNil(t, loadedKey.Group)
	require.Equal(t, service.DailyResetModeAnchored24h, loadedKey.Group.DailyResetMode)
}
