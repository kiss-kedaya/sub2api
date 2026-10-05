package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

type failedAffinityCacheStub struct {
	GatewayCache
	ids         []int64
	invalidated []string
	canceled    bool
}

func (s *failedAffinityCacheStub) FailedUserAccounts(context.Context, int64, int64) ([]int64, error) {
	return s.ids, nil
}
func (s *failedAffinityCacheStub) InvalidateFailedSession(ctx context.Context, _, _ int64, key string, _ int64) error {
	s.invalidated = append(s.invalidated, key)
	s.canceled = ctx.Err() != nil
	return nil
}

func TestUpstreamPreferencesTriesUnusedBeforeBoundedFallback(t *testing.T) {
	ctx := withUpstreamAttempts(context.Background())
	state, ok := ctx.Value(upstreamAttemptContextKey{}).(*upstreamAttempts)
	require.True(t, ok)
	state.failed[1] = true
	excluded := map[int64]struct{}{1: {}, 3: {}}
	var attempts []int64
	selectOne := func(ids map[int64]struct{}) (*AccountSelectionResult, OpenAIAccountScheduleDecision, *APIKey, error) {
		for _, id := range []int64{1, 2, 3} {
			if _, no := ids[id]; !no {
				attempts = append(attempts, id)
				return &AccountSelectionResult{Account: &Account{ID: id}}, OpenAIAccountScheduleDecision{}, nil, nil
			}
		}
		return nil, OpenAIAccountScheduleDecision{}, nil, ErrNoAvailableAccounts
	}
	r, _, _, err := selectWithUpstreamPreferences(ctx, nil, nil, excluded, selectOne)
	require.NoError(t, err)
	require.Equal(t, int64(2), r.Account.ID)
	state.failed[2] = false
	excluded[2] = struct{}{}
	r, _, _, err = selectWithUpstreamPreferences(ctx, nil, nil, excluded, selectOne)
	require.NoError(t, err)
	require.Equal(t, int64(1), r.Account.ID)
	_, _, _, err = selectWithUpstreamPreferences(ctx, nil, nil, excluded, selectOne)
	require.ErrorIs(t, err, ErrNoAvailableAccounts)
	require.Equal(t, []int64{2, 1}, attempts)
	require.Contains(t, excluded, int64(3), "profit/busy exclusions must not be changed")
}

func TestUpstreamPreferencesHistoricalFailureIsSoft(t *testing.T) {
	ctx := withUpstreamAttempts(context.WithValue(context.Background(), ctxkey.UserID, int64(8)))
	cache := &failedAffinityCacheStub{ids: []int64{1}}
	gid := int64(7)
	key := &APIKey{GroupID: &gid}
	for _, available := range [][]int64{{1, 2}, {1}} {
		r, _, _, err := selectWithUpstreamPreferences(ctx, cache, key, nil, func(ids map[int64]struct{}) (*AccountSelectionResult, OpenAIAccountScheduleDecision, *APIKey, error) {
			for _, id := range available {
				if _, no := ids[id]; !no {
					return &AccountSelectionResult{Account: &Account{ID: id}}, OpenAIAccountScheduleDecision{}, key, nil
				}
			}
			return nil, OpenAIAccountScheduleDecision{}, key, ErrNoAvailableAccounts
		})
		require.NoError(t, err)
		require.Equal(t, available[len(available)-1], r.Account.ID)
	}
}

func TestUpstreamAffinityCleanupSurvivesCanceledRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(withUpstreamAttempts(context.Background()))
	ctx = withOpenAILegacySessionHash(ctx, "legacy")
	cancel()
	cache := &failedAffinityCacheStub{}
	svc := &OpenAIGatewayService{cache: cache}
	svc.InvalidateUpstreamAffinity(ctx, nil, "current", &Account{ID: 1}, errors.New("stream failed"))
	require.Equal(t, []string{"openai:current", "openai:legacy"}, cache.invalidated)
	require.False(t, cache.canceled)
	state, ok := ctx.Value(upstreamAttemptContextKey{}).(*upstreamAttempts)
	require.True(t, ok)
	require.Contains(t, state.failed, int64(1))
}
