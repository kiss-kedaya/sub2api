package service

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOpsAutomationDefaultsAreOptInAndValidateWeights(t *testing.T) {
	c := DefaultOpsAutomationConfig()
	require.NoError(t, ValidateOpsAutomationConfig(c))
	require.False(t, c.Ramp.Enabled)
	require.False(t, c.Ranking.Enabled)
	for _, v := range c.OAuth {
		require.False(t, v.Enabled)
		require.Empty(t, v.GroupIDs)
	}
	c.Ranking.QualityWeight = math.NaN()
	require.Error(t, ValidateOpsAutomationConfig(c))
}
func TestAutomationRampExcludesOldInflightAndManualChanges(t *testing.T) {
	now := time.Now().UTC()
	c := DefaultOpsAutomationConfig()
	c.Revision = "r1"
	c.UpdatedAt = now.Add(-time.Hour)
	c.Ramp.SuccessesPerStep = 2
	c.Ramp.Step = 2
	c.Ramp.Maximum = 6
	c.Ramp.CooldownSeconds = 10
	state, current := AdvanceAutomationRamp(AutomationRampState{}, 2, c, AutomationOutcome{Success: true, StartedAt: now.Add(-time.Second)}, now)
	require.Equal(t, 2, current)
	require.Zero(t, state.Successes)
	e := AutomationOutcome{Success: true, StartedAt: now.Add(11 * time.Second)}
	state, current = AdvanceAutomationRamp(state, current, c, e, now.Add(12*time.Second))
	require.Equal(t, 1, state.Successes)
	state, current = AdvanceAutomationRamp(state, current, c, e, now.Add(13*time.Second))
	require.Equal(t, 4, current)
	require.Zero(t, state.Successes)
	state, current = AdvanceAutomationRamp(state, 3, c, e, now.Add(14*time.Second))
	require.Equal(t, 3, current)
	require.Zero(t, state.Successes)
	state, current = AdvanceAutomationRamp(state, current, c, AutomationOutcome{Success: false}, now.Add(20*time.Second))
	require.Equal(t, 3, current)
	require.Equal(t, now.Add(30*time.Second), state.PausedUntil)
}
func TestWithinGroupRankingKeepsUnknownSlotsPriorityAndDisabledOrder(t *testing.T) {
	s := NewOpsAutomationService(nil, nil, nil, nil)
	accounts := []*Account{{ID: 1, Priority: 1}, {ID: 2, Priority: 1}, {ID: 3, Priority: 1}, {ID: 4, Priority: 2}}
	require.Equal(t, accounts, s.RankAccounts(9, "model", accounts))
	c := DefaultOpsAutomationConfig()
	c.Ranking.Enabled = true
	c.Ranking.GroupIDs = []int64{9}
	c.Ranking.MinimumSamples = 2
	c.Ranking.Mode = "experience"
	s.config.Store(c)
	s.ranks[automationRankKey(9, "model")] = automationRankEntry{expires: time.Now().Add(time.Minute), signals: map[int64]AutomationRankSignal{1: {Samples: 10, P90MS: 30000}, 3: {Samples: 10, P90MS: 100}, 4: {Samples: 10, P90MS: 1}}}
	ordered := s.RankAccounts(9, "model", accounts)
	require.Equal(t, []int64{3, 2, 1, 4}, []int64{ordered[0].ID, ordered[1].ID, ordered[2].ID, ordered[3].ID})
	require.Equal(t, accounts, s.RankAccounts(10, "model", accounts))
}
