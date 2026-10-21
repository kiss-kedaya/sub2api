package wallet

import (
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/mode"
)

func TestReadOnlyWalletNeverTouchesVaultOrAudit(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "true")
	m := New(nil)
	checks := map[string]func() error{
		"initialize or unlock":  func() error { return m.Unlock(t.Context(), testPassphrase, testDeposit, testHot) },
		"replace gas seed":      func() error { return m.ImportHotMnemonic(t.Context(), testPassphrase, testReplacement) },
		"sign":                  func() error { _, err := m.SignEVM(t.Context(), sweepRequest()); return err },
		"derive":                func() error { _, err := m.Derive(t.Context(), 1, 1); return err },
		"hot address audit":     func() error { _, err := m.HotAddresses(t.Context()); return err },
		"mnemonic export audit": func() error { _, err := m.ExportMnemonics(t.Context(), testPassphrase); return err },
		"vault backup audit":    func() error { _, err := m.ExportVaultBackup(t.Context(), testPassphrase); return err },
	}
	for name, call := range checks {
		t.Run(name, func(t *testing.T) {
			if err := call(); !errors.Is(err, mode.ErrReadOnly) {
				t.Fatalf("read-only wallet attempted a persistent operation: %v", err)
			}
		})
	}
	if status, err := m.Status(t.Context()); err != nil || status.Unlocked {
		t.Fatalf("status polling must remain read-only and locked: %+v %v", status, err)
	}
}
