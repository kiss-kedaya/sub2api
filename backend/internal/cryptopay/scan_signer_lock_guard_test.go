package cryptopay

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/wallet"
	"github.com/stretchr/testify/require"
)

// masterKeyForTest is a 32-byte hex master key, the shape wallet.New requires to
// initialise the security key. Test material only.
const masterKeyForTest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// settingVaultForTest mirrors the wallet package's unexported vault key.
const settingVaultForTest = "crypto.signer.vault"

// A v3 signer vault sealed behind recovery shards is a supported steady state
// and must not fail the deposit scan.
//
// Production symptom this guards: BSC sat at cursor 135294224 for hours with
// "扫描速度 0 块/分" while the chain grew ~133 blocks/min. Every pass failed in
// its security stage on the first range after the cursor, so the cursor could
// never advance and the chain fell permanently behind.
func TestScanSecurityGuardIgnoresSealedVault(t *testing.T) {
	// Before the fix the guard returned this error verbatim, so the scanner
	// failed the block range and never saved the cursor.
	require.NoError(t, forgiveSealedVault(wallet.ErrSignerShareRequired))
	require.NoError(t, forgiveSealedVault(
		errors.Join(errors.New("security stage"), wallet.ErrSignerShareRequired)))
}

// The guard forgives exactly one condition. A real security incident must keep
// stopping the scan, otherwise this fix would silently disable the outflow
// guard's blocking behaviour.
func TestScanSecurityGuardOnlyForgivesSealedVault(t *testing.T) {
	require.True(t, isSealedVaultSteadyState(wallet.ErrSignerShareRequired))
	require.True(t, isSealedVaultSteadyState(errors.Join(errors.New("wrap"), wallet.ErrSignerShareRequired)))

	for _, err := range []error{
		nil,
		wallet.ErrSecurityLocked,
		wallet.ErrVaultUnavailable,
		wallet.ErrWalletNotConfigured,
		wallet.ErrSigningDisabled,
		errors.New("vault_unavailable"),
	} {
		require.False(t, isSealedVaultSteadyState(err),
			"expected %v to keep blocking the scan", err)
	}
}

// A sealed vault must not abort the scan when an outflow needs the signer audit
// table. AuthorizedTokenTransfer surfaces ErrSignerShareRequired whenever the
// vault is sealed, so one outflow inside the scanned range used to fail the
// whole block range and freeze the cursor behind it.
func TestAuthorizedTokenTransferSealedVaultIsNotAScanFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mgr := wallet.New(db, masterKeyForTest)
	mgr.EnableSecurityMonitoring()

	// currentVaultTx reads the vault row, cannot derive (no data key), and
	// returns ErrSignerShareRequired before any security-state read.
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT value FROM settings").
		WithArgs(settingVaultForTest).
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow("sub2api-wallet:v3:deadbeef"))
	mock.ExpectRollback()

	_, err = mgr.AuthorizedTokenTransfer(context.Background(), "bsc", 56, "0xdead",
		"0xfrom", "0xto", "0xtoken", "1000")
	require.True(t, isSealedVaultSteadyState(err),
		"an outflow lookup against a sealed vault must be the forgivable steady state, got %v", err)
}
