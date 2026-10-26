//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAdminCreateGroupReturnRequestedModelIsOptInForEveryPlatform(t *testing.T) {
	for _, platform := range []string{PlatformOpenAI, PlatformAnthropic, PlatformGemini, PlatformAntigravity, PlatformGrok, PlatformComposite} {
		t.Run(platform, func(t *testing.T) {
			for _, enabled := range []bool{false, true} {
				repo := &groupRepoStubForAdmin{}
				svc := &adminServiceImpl{groupRepo: repo}
				created, err := svc.CreateGroup(t.Context(), &CreateGroupInput{
					Name: "response-model-policy", Platform: platform, RateMultiplier: 1,
					ReturnRequestedModel: enabled,
				})
				require.NoError(t, err)
				require.NotNil(t, repo.created)
				require.Equal(t, enabled, created.ReturnRequestedModel)
				require.Equal(t, enabled, repo.created.ReturnRequestedModel)
			}
		})
	}
}

func TestAdminUpdateGroupReturnRequestedModelPreservesOmittedAndExplicitFalse(t *testing.T) {
	enabled, disabled := true, false
	for _, test := range []struct {
		name     string
		previous bool
		input    *bool
		want     bool
	}{
		{name: "enable", input: &enabled, want: true},
		{name: "explicitly disable", previous: true, input: &disabled},
		{name: "omitted stays enabled", previous: true, want: true},
		{name: "omitted stays disabled"},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := &groupRepoStubForAdmin{getByID: &Group{
				ID: 17, Name: "response-model-policy", Platform: PlatformOpenAI, Status: StatusActive,
				ReturnRequestedModel: test.previous,
			}}
			invalidator := &authCacheInvalidatorStub{}
			svc := &adminServiceImpl{groupRepo: repo, authCacheInvalidator: invalidator}
			updated, err := svc.UpdateGroup(context.Background(), 17, &UpdateGroupInput{ReturnRequestedModel: test.input})
			require.NoError(t, err)
			require.NotNil(t, repo.updated)
			require.Equal(t, test.want, updated.ReturnRequestedModel)
			require.Equal(t, test.want, repo.updated.ReturnRequestedModel)
			require.Equal(t, []int64{17}, invalidator.groupIDs)
		})
	}
}
