package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildOpenAIFillSelectionOrderUsesPriorityLayers(t *testing.T) {
	candidates := []openAIAccountCandidateScore{
		{account: &Account{ID: 20, Priority: 2}, score: 100},
		{account: &Account{ID: 11, Priority: 1}, score: 1},
		{account: &Account{ID: 12, Priority: 1}, score: 50},
		{account: &Account{ID: 30, Priority: 3}, score: 1000},
	}
	ordered := buildOpenAIFillSelectionOrder(candidates, OpenAIAccountScheduleRequest{FillScheduling: true})
	require.Len(t, ordered, len(candidates))
	require.Equal(t, 1, ordered[0].account.Priority)
	require.Equal(t, 1, ordered[1].account.Priority)
	require.Equal(t, 2, ordered[2].account.Priority)
	require.Equal(t, 3, ordered[3].account.Priority)
}
