//go:build unit

package cryptopay

// HoldSweepForTest takes the sweep lock the way a running sweep does, so
// external-package handler regressions can exercise the real service behind a
// long sweep without exporting production state. Call the returned func to end
// the "sweep".
func (s *Service) HoldSweepForTest() (release func()) {
	s.sweepMu.Lock()
	return s.sweepMu.Unlock
}
