package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCandidateGroupIDs(t *testing.T) {
	id := int64(7)
	key := &APIKey{GroupID: &id, RouteGroupIDs: []int64{7, 8, 8, 0, 9}}
	require.Equal(t, []int64{7, 8, 9}, key.CandidateGroupIDs())

	plain := &APIKey{GroupID: &id}
	require.Equal(t, []int64{7}, plain.CandidateGroupIDs())
}

func TestGroupAllowsRequestedModel(t *testing.T) {
	require.True(t, groupAllowsRequestedModel(nil, "gpt-5"))
	require.True(t, groupAllowsRequestedModel(&Group{}, "gpt-5"))

	restricted := &Group{ModelsListConfig: GroupModelsListConfig{Enabled: true, Models: []string{"gpt-5", "codex-mini"}}}
	require.True(t, groupAllowsRequestedModel(restricted, "gpt-5"))
	require.True(t, groupAllowsRequestedModel(restricted, "GPT-5"))
	require.False(t, groupAllowsRequestedModel(restricted, "claude-opus-4"))
}

func TestIsOpenAICompatibleUpstreamPlatform(t *testing.T) {
	require.True(t, isOpenAICompatibleUpstreamPlatform("openai"))
	require.True(t, isOpenAICompatibleUpstreamPlatform("grok"))
	require.True(t, isOpenAICompatibleUpstreamPlatform("kimi"))
	require.False(t, isOpenAICompatibleUpstreamPlatform("anthropic"))
	require.False(t, isOpenAICompatibleUpstreamPlatform("gemini"))
}

func TestNormalizeAPIKeyGroupIDs(t *testing.T) {
	first := int64(1)
	ids, primary, err := normalizeAPIKeyGroupIDs(&first, []int64{1, 2, 3})
	require.NoError(t, err)
	require.Equal(t, []int64{1, 2, 3}, ids)
	require.Equal(t, int64(1), *primary)

	_, _, err = normalizeAPIKeyGroupIDs(&first, []int64{2, 1})
	require.Error(t, err)

	_, _, err = normalizeAPIKeyGroupIDs(nil, []int64{1, 1})
	require.Error(t, err)

	tooMany := make([]int64, maxAPIKeyGroupRoutes+1)
	for i := range tooMany {
		tooMany[i] = int64(i + 1)
	}
	_, _, err = normalizeAPIKeyGroupIDs(nil, tooMany)
	require.Error(t, err)
}

func TestShouldContinueAlongKeyRoutes(t *testing.T) {
	require.True(t, shouldContinueAlongKeyRoutes(ErrNoAvailableAccounts))
	require.True(t, shouldContinueAlongKeyRoutes(ErrSchedulerCacheNotReady))
	require.True(t, shouldContinueAlongKeyRoutes(ErrClaudeCodeOnly))
	require.True(t, shouldContinueAlongKeyRoutes(ErrNoAvailableCompactAccounts))
	require.True(t, shouldContinueAlongKeyRoutes(ErrGroupNotFound))
	require.False(t, shouldContinueAlongKeyRoutes(nil))
	require.False(t, shouldContinueAlongKeyRoutes(ErrAPIKeyNotFound))
}

func TestHydrateAPIKeyGroupRequiresFullGroup(t *testing.T) {
	id := int64(1)
	key := &APIKey{GroupID: &id, Group: &Group{ID: 1, RateMultiplier: 1}}

	out, err := hydrateAPIKeyGroup(context.Background(), key, 1, nil)
	require.NoError(t, err)
	require.Equal(t, key, out)

	_, err = hydrateAPIKeyGroup(context.Background(), key, 2, nil)
	require.ErrorIs(t, err, ErrSchedulerCacheNotReady)

	out, err = hydrateAPIKeyGroup(context.Background(), key, 2, func(_ context.Context, gid int64) (*Group, error) {
		return &Group{ID: gid, RateMultiplier: 2}, nil
	})
	require.NoError(t, err)
	require.Equal(t, int64(2), *out.GroupID)
	require.Equal(t, 2.0, out.Group.RateMultiplier)
	require.Equal(t, 1.0, key.Group.RateMultiplier)
}

func TestGroupPlatformFitsRequest(t *testing.T) {
	require.True(t, groupPlatformFitsRequest("anthropic", "anthropic"))
	require.False(t, groupPlatformFitsRequest("openai", "anthropic"))
	require.False(t, groupPlatformFitsRequest("anthropic", "openai"))
	require.True(t, groupPlatformFitsRequest("antigravity", "anthropic"))
	require.True(t, groupPlatformFitsRequest("", "openai"))
}
