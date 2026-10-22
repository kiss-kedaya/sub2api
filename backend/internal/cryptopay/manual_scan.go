package cryptopay

import (
	"context"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/mode"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/scanner"
)

var ErrScanBusy = scanner.ErrScanBusy

// RunScan leaves all background maintenance disabled. The local guard pins the
// config against reload/sweep; the worker's session lock protects all instances.
func (s *Service) RunScan(ctx context.Context) error {
	if err := mode.CheckWritable(); err != nil {
		return err
	}
	if s == nil {
		return errors.New("payment scanner unavailable")
	}
	if !s.sweepMu.TryLock() {
		return ErrScanBusy
	}
	defer s.sweepMu.Unlock()
	s.mu.RLock()
	enabled, worker := s.config.Enabled, s.worker
	s.mu.RUnlock()
	if !enabled || worker == nil {
		return errors.New("enable payment channel before scanning")
	}
	return worker.RunOnce(ctx)
}
