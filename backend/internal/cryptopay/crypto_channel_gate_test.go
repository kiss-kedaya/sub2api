package cryptopay

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/wallet"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func newChannelGateService(t *testing.T) (*Service, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	cfg := DefaultCryptoConfig()
	cfg.Enabled = true
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	settings := service.NewSettingService(readOnlyConfigRepository{raw: string(raw)}, nil)
	return &Service{
		db:        db,
		settings:  settings,
		wallet:    wallet.New(db, strings.Repeat("ab", 32)),
		addresses: NewAddressService(db, settings),
	}, mock
}

// A frontend that delegates scanning must keep serving the customer channel:
// the admin switch, not the scan duty, decides what users see.
func TestDelegatedScanHostKeepsCustomerChannelServing(t *testing.T) {
	t.Setenv("CRYPTO_INSTANCE_ENABLED", "false")
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "")
	t.Setenv("SUB2API_DISABLE_BACKGROUND_JOBS", "")

	s, mock := newChannelGateService(t)
	s.reload(t.Context())

	if !s.Config().Enabled {
		t.Fatal("delegating scans hid the admin-enabled customer channel")
	}
	if s.worker != nil || s.maintCancel != nil || s.executor != nil || s.planner != nil {
		t.Fatal("delegated host started scanning, maintenance or sweep work")
	}
	if s.discoveryCancel != nil {
		s.Close()
		t.Fatal("delegated host started RPC discovery")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// The host that owns scanning keeps the worker and sweep machinery; the split
// must not quietly disable the only scanner.
func TestScanHostStillOwnsWorkerAndSweeper(t *testing.T) {
	t.Setenv("CRYPTO_INSTANCE_ENABLED", "true")
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "")
	t.Setenv("SUB2API_DISABLE_BACKGROUND_JOBS", "true")

	s, mock := newChannelGateService(t)
	s.reload(t.Context())

	if !s.Config().Enabled {
		t.Fatal("scan host lost the enabled channel")
	}
	if s.worker == nil || s.executor == nil || s.planner == nil {
		s.Close()
		t.Fatal("scan host did not build the scanner and sweeper")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
