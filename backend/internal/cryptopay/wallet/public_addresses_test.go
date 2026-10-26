package wallet

import (
	"context"
	"errors"
	"testing"
)

func TestPublicHotAddressesAreReadOnlyAndRefreshAfterRemoteImport(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "true")
	m, mock := mockManager(t)
	m.SetEnabled(false)
	first := managedVault(t)
	expectRead(mock, first, "")
	before, err := m.PublicHotAddresses(t.Context())
	if err != nil || before == nil || before.EVM == "" || before.Solana == "" {
		t.Fatalf("public addresses unavailable while signing is disabled: %v", err)
	}
	afterImport, err := m.sealContents(testDeposit, testReplacement)
	if err != nil {
		t.Fatal(err)
	}
	expectRead(mock, afterImport, "")
	after, err := m.PublicHotAddresses(t.Context())
	if err != nil || after == nil || after.EVM == before.EVM || after.Solana == before.Solana {
		t.Fatalf("public address query retained the former gas wallet: %v", err)
	}
	// mockManager verifies the only DB operations above were SELECTs. No audit,
	// signing, initialization or persistent changes are permitted by polling.
}

func TestPublicHotAddressesNeverReturnsStaleAddressAfterVaultFailure(t *testing.T) {
	for _, raw := range []string{"", "invalid encrypted vault"} {
		t.Run(raw, func(t *testing.T) {
			m, mock, _ := residentManager(t)
			expectRead(mock, raw, "")
			hot, err := m.PublicHotAddresses(t.Context())
			if hot != nil || err == nil || m.ready() {
				t.Fatal("failed vault lookup reused a resident gas wallet")
			}
			if raw == "" && !errors.Is(err, ErrWalletNotConfigured) {
				t.Fatal(err)
			}
		})
	}
}

func TestPublicHotAddressesCancellationDoesNotReadVault(t *testing.T) {
	m, _ := mockManager(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := m.PublicHotAddresses(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
