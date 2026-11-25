//go:build unit

package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAdminService_CreateGroup_StoresOpenAITierMultipliers(t *testing.T) {
	for _, tt := range []struct {
		name     string
		platform string
		want     bool
	}{
		{name: "openai", platform: PlatformOpenAI, want: true},
		{name: "composite", platform: PlatformComposite, want: true},
		{name: "anthropic drops them", platform: PlatformAnthropic, want: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			repo := &groupRepoStubForAdmin{}
			svc := &adminServiceImpl{groupRepo: repo}

			_, err := svc.CreateGroup(context.Background(), &CreateGroupInput{
				Name: "tier-" + tt.name, Platform: tt.platform, RateMultiplier: 1,
				OpenAIFastMultiplier: ptrTierFloat(1), OpenAIUltrafastMultiplier: ptrTierFloat(2.5),
			})

			require.NoError(t, err)
			if !tt.want {
				require.Nil(t, repo.created.OpenAIFastMultiplier)
				require.Nil(t, repo.created.OpenAIUltrafastMultiplier)
				return
			}
			require.NotNil(t, repo.created.OpenAIFastMultiplier)
			require.Equal(t, 1.0, *repo.created.OpenAIFastMultiplier)
			require.NotNil(t, repo.created.OpenAIUltrafastMultiplier)
			require.Equal(t, 2.5, *repo.created.OpenAIUltrafastMultiplier)
		})
	}
}

func TestAdminService_CreateGroup_RejectsInvalidOpenAITierMultiplier(t *testing.T) {
	for _, bad := range []float64{-1, 100.5} {
		repo := &groupRepoStubForAdmin{}
		svc := &adminServiceImpl{groupRepo: repo}
		_, err := svc.CreateGroup(context.Background(), &CreateGroupInput{
			Name: "bad", Platform: PlatformOpenAI, RateMultiplier: 1, OpenAIFastMultiplier: ptrTierFloat(bad),
		})
		require.Error(t, err, bad)
		require.Nil(t, repo.created, "nothing is stored when validation fails")
	}
}

func TestAdminService_UpdateGroup_OpenAITierMultiplierSemantics(t *testing.T) {
	newGroup := func() *Group {
		return &Group{
			ID: 1, Name: "tier", Platform: PlatformOpenAI, Status: StatusActive,
			OpenAIFastMultiplier: ptrTierFloat(2), OpenAIUltrafastMultiplier: ptrTierFloat(6),
		}
	}

	t.Run("omitted keeps what is stored", func(t *testing.T) {
		repo := &groupRepoStubForAdmin{getByID: newGroup()}
		svc := &adminServiceImpl{groupRepo: repo}
		_, err := svc.UpdateGroup(context.Background(), 1, &UpdateGroupInput{})
		require.NoError(t, err)
		require.Equal(t, 2.0, *repo.updated.OpenAIFastMultiplier)
		require.Equal(t, 6.0, *repo.updated.OpenAIUltrafastMultiplier)
	})
	t.Run("a value sets one and leaves the other alone", func(t *testing.T) {
		repo := &groupRepoStubForAdmin{getByID: newGroup()}
		svc := &adminServiceImpl{groupRepo: repo}
		_, err := svc.UpdateGroup(context.Background(), 1, &UpdateGroupInput{OpenAIFastMultiplier: ptrTierFloat(1)})
		require.NoError(t, err)
		require.Equal(t, 1.0, *repo.updated.OpenAIFastMultiplier)
		require.Equal(t, 6.0, *repo.updated.OpenAIUltrafastMultiplier)
	})
	t.Run("zero clears back to the official price", func(t *testing.T) {
		repo := &groupRepoStubForAdmin{getByID: newGroup()}
		svc := &adminServiceImpl{groupRepo: repo}
		_, err := svc.UpdateGroup(context.Background(), 1, &UpdateGroupInput{OpenAIFastMultiplier: ptrTierFloat(0), OpenAIUltrafastMultiplier: ptrTierFloat(0)})
		require.NoError(t, err)
		require.Nil(t, repo.updated.OpenAIFastMultiplier)
		require.Nil(t, repo.updated.OpenAIUltrafastMultiplier)
	})
	t.Run("an invalid value changes nothing", func(t *testing.T) {
		repo := &groupRepoStubForAdmin{getByID: newGroup()}
		svc := &adminServiceImpl{groupRepo: repo}
		_, err := svc.UpdateGroup(context.Background(), 1, &UpdateGroupInput{OpenAIUltrafastMultiplier: ptrTierFloat(1000)})
		require.Error(t, err)
		require.Nil(t, repo.updated)
	})
	t.Run("switching to a platform without Fast drops them", func(t *testing.T) {
		repo := &groupRepoStubForAdmin{getByID: newGroup()}
		svc := &adminServiceImpl{groupRepo: repo}
		_, err := svc.UpdateGroup(context.Background(), 1, &UpdateGroupInput{Platform: PlatformAnthropic})
		require.NoError(t, err)
		require.Nil(t, repo.updated.OpenAIFastMultiplier)
		require.Nil(t, repo.updated.OpenAIUltrafastMultiplier)
	})
	t.Run("an edit invalidates the key auth cache", func(t *testing.T) {
		repo := &groupRepoStubForAdmin{getByID: newGroup()}
		invalidator := &authCacheInvalidatorStub{}
		svc := &adminServiceImpl{groupRepo: repo, authCacheInvalidator: invalidator}
		_, err := svc.UpdateGroup(context.Background(), 1, &UpdateGroupInput{OpenAIFastMultiplier: ptrTierFloat(1)})
		require.NoError(t, err)
		require.Equal(t, []int64{1}, invalidator.groupIDs)
	})
}

func TestAPIKeyAuthSnapshotGroupOpenAITierMultipliersRoundtrip(t *testing.T) {
	groupID := int64(50)
	apiKey := &APIKey{
		ID: 82, UserID: 40, GroupID: &groupID, Key: "sk-tier-roundtrip", Status: StatusActive,
		User: &User{ID: 40, Status: StatusActive},
		Group: &Group{
			ID: groupID, Name: "tier-roundtrip", Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true,
			OpenAIFastMultiplier: ptrTierFloat(1.5), OpenAIUltrafastMultiplier: nil,
		},
	}
	svc := &APIKeyService{}

	payload, err := json.Marshal(&APIKeyAuthCacheEntry{Snapshot: svc.snapshotFromAPIKey(context.Background(), apiKey)})
	require.NoError(t, err)
	var cached APIKeyAuthCacheEntry
	require.NoError(t, json.Unmarshal(payload, &cached))

	materialized, used, err := svc.applyAuthCacheEntry(apiKey.Key, &cached)
	require.NoError(t, err)
	require.True(t, used)
	require.NotNil(t, materialized.Group.OpenAIFastMultiplier)
	require.Equal(t, 1.5, *materialized.Group.OpenAIFastMultiplier)
	require.Nil(t, materialized.Group.OpenAIUltrafastMultiplier)
}

func TestGroupDuplicateCopiesOpenAITierMultipliersWithoutSharingThem(t *testing.T) {
	source := &Group{Platform: PlatformOpenAI, OpenAIFastMultiplier: ptrTierFloat(1), OpenAIUltrafastMultiplier: ptrTierFloat(3)}
	copied := cloneGroupValuePointer(source.OpenAIFastMultiplier)
	require.NotNil(t, copied)
	*copied = 9
	require.Equal(t, 1.0, *source.OpenAIFastMultiplier, "the duplicate must not alias the source value")
}
