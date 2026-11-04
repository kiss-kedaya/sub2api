package cryptopay

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAsyncSweepCompletesInsteadOfRelockingItself(t *testing.T) {
	s := &Service{}
	started, err := s.StartSweepAsync(true, []string{"bsc"})
	require.NoError(t, err)
	require.NotEmpty(t, started.ID)
	require.Eventually(t, func() bool {
		return s.SweepRun(started.ID).Finished
	}, time.Second, 5*time.Millisecond, "an unavailable executor must finish with an error, not deadlock")
	require.NotEmpty(t, s.SweepRun(started.ID).Error)
	require.False(t, started.Finished, "the returned start response must be an immutable snapshot")
	started.Chains[0] = "changed"
	require.Equal(t, []string{"bsc"}, s.SweepRun(started.ID).Chains)
	require.True(t, s.sweepMu.TryLock(), "completion must release the sweep lock")
	s.sweepMu.Unlock()
}

func TestAsyncSweepRefusesAnAlreadyRunningSweep(t *testing.T) {
	s := &Service{}
	s.sweepMu.Lock()
	defer s.sweepMu.Unlock()
	run, err := s.StartSweepAsync(true, nil)
	require.ErrorIs(t, err, ErrScanBusy)
	require.Nil(t, run)
}
