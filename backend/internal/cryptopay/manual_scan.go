package cryptopay

import (
	"context"
	"errors"
	"slices"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/mode"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/scanner"
)

var ErrScanBusy = scanner.ErrScanBusy

// ErrInvalidScanChain means a requested chain is not enabled for EVM scanning.
var ErrInvalidScanChain = scanner.ErrInvalidScanChain

// RunScan leaves all background maintenance disabled. The local guard pins the
// config against reload/sweep; the worker's session lock protects all instances.
func (s *Service) RunScan(ctx context.Context, fromBlock uint64, chains ...string) error {
	if err := mode.CheckWritable(); err != nil {
		return err
	}
	if s == nil {
		return errors.New("payment scanner unavailable")
	}
	if err := s.validateScanChains(chains); err != nil {
		return err
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
	return worker.RunOnceFrom(ctx, fromBlock, chains...)
}

func (s *Service) validateScanChains(chains []string) error {
	if len(chains) == 0 {
		return nil
	}
	if s == nil {
		return ErrScanUnavailable
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, chain := range chains {
		if !IsEVMChain(chain) || !slices.Contains(s.config.Chains, chain) {
			return ErrInvalidScanChain
		}
	}
	return nil
}
