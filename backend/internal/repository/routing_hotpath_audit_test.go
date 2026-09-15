//go:build unit

package repository

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestRoutingHotpathAudit_ModelCandidatesKeepGroupScope(t *testing.T) {
	counter := &countingQueryMatcher{}
	repo, mock := newModelAvailabilityCandidateRepo(t, counter)
	groupA, groupB, groupZero := int64(42), int64(43), int64(0)
	cases := []struct {
		group          *int64
		includeGrouped bool
		accountID      int64
	}{
		{&groupA, false, 7},
		{&groupB, false, 8},
		{&groupZero, false, 9},
		{nil, false, 10},
		{nil, true, 11},
	}
	for _, tc := range cases {
		rows := modelAvailabilityCandidateRow().AddRow(tc.accountID, "openai", "api_key", 2, 80,
			nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
		query := mock.ExpectQuery("model availability candidates")
		if tc.group != nil {
			query.WithArgs(*tc.group, service.StatusActive, service.PlatformOpenAI)
		} else {
			query.WithArgs(service.StatusActive, service.PlatformOpenAI)
		}
		query.WillReturnRows(rows)
		accounts, err := repo.ListModelAvailabilityCandidates(context.Background(), tc.group, []string{service.PlatformOpenAI}, tc.includeGrouped)
		require.NoError(t, err)
		require.Len(t, accounts, 1)
		require.Equal(t, tc.accountID, accounts[0].ID)
	}
	for _, tc := range cases {
		accounts, err := repo.ListModelAvailabilityCandidates(context.Background(), tc.group, []string{service.PlatformOpenAI}, tc.includeGrouped)
		require.NoError(t, err)
		require.Len(t, accounts, 1)
		require.Equal(t, tc.accountID, accounts[0].ID, "cached candidates must retain their group scope")
	}
	require.Equal(t, len(cases), counter.Count())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRoutingHotpathAudit_ModelCandidateFailureIsRetried(t *testing.T) {
	counter := &countingQueryMatcher{}
	repo, mock := newModelAvailabilityCandidateRepo(t, counter)
	failure := errors.New("temporary candidate lookup failure")
	mock.ExpectQuery("model availability candidates").WillReturnError(failure)
	mock.ExpectQuery("model availability candidates").WillReturnRows(addOAuthCandidateRow(modelAvailabilityCandidateRow()))
	group := int64(42)
	_, err := repo.ListModelAvailabilityCandidates(context.Background(), &group, []string{service.PlatformOpenAI}, false)
	require.ErrorIs(t, err, failure)
	accounts, err := repo.ListModelAvailabilityCandidates(context.Background(), &group, []string{service.PlatformOpenAI}, false)
	require.NoError(t, err)
	require.Len(t, accounts, 1)
	require.Equal(t, int64(7), accounts[0].ID)
	require.Equal(t, 2, counter.Count())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRoutingHotpathAudit_ModelCandidateInvalidationRejectsOldPublication(t *testing.T) {
	first, second := newModelAvailabilityCandidateCache(), newModelAvailabilityCandidateCache()
	group := int64(42)
	key := makeModelAvailabilityCandidateCacheKey(&group, []string{service.PlatformOpenAI}, false)
	old := []service.Account{{ID: 7}}
	first.set(key, old)
	second.set(key, old)
	generation := first.currentGeneration()
	first.clear()
	_, hit := second.get(key)
	require.False(t, hit, "invalidation must reach other repository instances")
	first.setIfGeneration(key, old, generation)
	_, hit = first.get(key)
	require.False(t, hit, "a pre-invalidation query must not repopulate the cache")
	first.setIfGeneration(key, []service.Account{{ID: 9}}, first.currentGeneration())
	accounts, hit := first.get(key)
	require.True(t, hit)
	require.Equal(t, int64(9), accounts[0].ID)
}
