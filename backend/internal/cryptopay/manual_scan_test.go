package cryptopay

import (
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/mode"
)

func TestManualScanRejectsReadOnlyBeforeDatabase(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "true")
	t.Setenv("CRYPTO_ADMIN_TEST_WRITE_ENABLED", "false")
	var s *Service
	if err := s.RunScan(t.Context()); !errors.Is(err, mode.ErrReadOnly) {
		t.Fatalf("read-only scan: %v", err)
	}
}

func TestManualScanKeepsSharedJobsDisabledAndSerializesConfig(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "true")
	t.Setenv("CRYPTO_ADMIN_TEST_WRITE_ENABLED", "true")
	t.Setenv("SUB2API_DISABLE_BACKGROUND_JOBS", "true")
	s := &Service{}
	if err := s.RunScan(t.Context()); err == nil {
		t.Fatal("disabled channel scanned")
	}
	s.sweepMu.Lock()
	err := s.RunScan(t.Context())
	s.sweepMu.Unlock()
	if !errors.Is(err, ErrScanBusy) {
		t.Fatalf("concurrent reload/sweep did not reject scan: %v", err)
	}
	if s.maintCancel != nil || cryptoWorkersAllowed() {
		t.Fatal("manual request started shared jobs")
	}
}
