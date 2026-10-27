package localreasoning

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRandomQuestionsHaveComputedAnswersAndUniqueSeeds(t *testing.T) {
	seen := map[string]bool{}
	families := map[string]bool{}
	for i := byte(1); i <= 40; i++ {
		seed := bytes.Repeat([]byte{i}, 16)
		q, err := Generate(bytes.NewReader(seed))
		require.NoError(t, err)
		require.NotEmpty(t, q.Prompt)
		require.True(t, Matches(q, q.Expected))
		require.False(t, Matches(q, `{"wrong":1}`))
		require.False(t, seen[q.ID])
		seen[q.ID] = true
		families[q.Family] = true
		var value any
		require.NoError(t, json.Unmarshal([]byte(q.Expected), &value))
	}
	require.Len(t, families, 2)
}
func TestRandomQuestionsDoNotFallbackWhenEntropyFails(t *testing.T) {
	_, err := Generate(bytes.NewReader(nil))
	require.Error(t, err)
}
