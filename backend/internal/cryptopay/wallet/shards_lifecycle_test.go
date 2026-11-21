package wallet

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/wallet/shards"
	"github.com/stretchr/testify/require"
)

// TestShardLifecycle covers the local end-to-end protocol without any network:
// v2 vault -> browser data key + 2-of-3 shares -> server stores only A ->
// backfill proof -> locked state -> A+B unlock -> re-split invalidates the old
// set. All share material is generated in-process.
func TestShardLifecycle(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	defer func() { _ = db.Close() }()
	mock.MatchExpectationsInOrder(false)

	legacyVault := managedVault(t)
	dataKey := make([]byte, 32)
	for i := range dataKey {
		dataKey[i] = byte(i + 1)
	}
	split, err := shards.Split(dataKey, shards.DefaultThreshold, shards.DefaultTotal)
	require.NoError(t, err)
	commitment := shards.Commitment(dataKey)
	commitmentB64 := base64.StdEncoding.EncodeToString(commitment[:])
	newVault := v3Vault(t, dataKey)

	sealer := New(nil, testMasterKey)
	sealedOldA, err := sealer.sealMasterContext([]byte(split[0].String()), shareACipherContext)
	require.NoError(t, err)
	recordJSON := mustRecordJSON(t, shardRecord{
		Version: shardRecordVersion, Scheme: shards.Scheme,
		Threshold: shards.DefaultThreshold, Total: shards.DefaultTotal,
		ShareA: sealedOldA, Commitment: commitmentB64,
		CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z",
	})
	require.NotContains(t, recordJSON, split[0].String())
	require.NotContains(t, recordJSON, base64.StdEncoding.EncodeToString(dataKey))

	manager := New(db, testMasterKey)
	expectAdvisoryLock := func() {
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT pg_try_advisory_xact_lock").
			WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(true))
	}
	expectSetting := func(key, value string) {
		mock.ExpectQuery(`SELECT value FROM settings WHERE key = \$1`).WithArgs(key).
			WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(value))
	}
	expectSettingMissing := func(key string) {
		mock.ExpectQuery(`SELECT value FROM settings WHERE key = \$1`).WithArgs(key).
			WillReturnRows(sqlmock.NewRows([]string{"value"}))
	}
	expectVaultForUpdate := func(value string) {
		mock.ExpectQuery(`SELECT value FROM settings WHERE key = \$1 FOR UPDATE`).WithArgs(settingVault).
			WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(value))
	}
	expectSettingWrite := func() { mock.ExpectExec(`INSERT INTO settings`).WillReturnResult(sqlmock.NewResult(1, 1)) }
	expectVaultUpdate := func() {
		mock.ExpectExec(`UPDATE settings SET value = \$2`).WillReturnResult(sqlmock.NewResult(0, 1))
	}
	expectAuditWrite := func() {
		mock.ExpectExec(`INSERT INTO crypto_signer_audit`).WillReturnResult(sqlmock.NewResult(1, 1))
	}
	expectCommit := func() { mock.ExpectCommit() }
	expectPoolEmpty := func() {
		mock.ExpectQuery(`SELECT chain_family, derivation_index, address FROM crypto_address_pool`).
			WillReturnRows(sqlmock.NewRows([]string{"chain_family", "derivation_index", "address"}))
	}
	expectShareAttempts := func(failed int) {
		mock.ExpectQuery(`SELECT COUNT\(\*\) FROM crypto_signer_audit`).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(failed))
	}

	// 1. Setup: one transaction rewrites the vault with the browser data key and
	// stores the sealed share A, then reports the new (locked) status.
	expectAdvisoryLock()
	expectSettingMissing(settingShards)
	expectVaultForUpdate(legacyVault)
	expectPoolEmpty()
	expectVaultUpdate()  // vault ciphertext
	expectSettingWrite() // shard record
	expectSettingWrite() // public hot addresses
	expectAuditWrite()
	expectCommit()
	expectSetting(settingVault, newVault)
	expectSetting(settingShards, recordJSON)
	status, err := manager.SetupShards(context.Background(), ShardSetupRequest{
		DataKey: dataKey, ShareA: split[0].String(), ConfirmShare: split[1].String(),
		Commitment: commitmentB64, RiskAcknowledged: true,
	})
	if err != nil {
		t.Fatalf("setup failed: %v; expectations: %v", err, mock.ExpectationsWereMet())
	}
	require.True(t, status.Configured)
	require.True(t, status.Locked)
	require.Equal(t, 3, status.VaultVersion)

	// 2. Backfill proof: the operator-typed share B must reconstruct the key.
	expectShareAttempts(0)
	expectSetting(settingShards, recordJSON)
	expectSetting(settingVault, newVault)
	expectAuditWrite()
	require.NoError(t, manager.VerifyShardShare(context.Background(), split[2].String()))

	// 3. A wrong share is rejected without revealing closeness, and audited.
	expectShareAttempts(0)
	expectSetting(settingShards, recordJSON)
	expectAuditWrite()
	require.ErrorIs(t, manager.VerifyShardShare(context.Background(), "S2V1-AAAA-AAAA-AAAA"), ErrSignerShareInvalid)

	// 4. Five failures in the window lock the signer.
	expectShareAttempts(shareFailureLimit)
	require.ErrorIs(t, manager.VerifyShardShare(context.Background(), split[1].String()), ErrSignerRateLimited)

	// 5. Locked: no seed access without a share.
	expectSetting(settingVault, newVault)
	locked, err := manager.SignerLocked(context.Background())
	require.NoError(t, err)
	require.True(t, locked)
	_, err = manager.openContents(newVault)
	require.ErrorIs(t, err, ErrSignerShareRequired)

	// 6. A+B unlocks and LockSigner zeroes the key again.
	expectShareAttempts(0)
	expectSetting(settingShards, recordJSON)
	expectSetting(settingVault, newVault)
	require.NoError(t, manager.UnlockSigner(context.Background(), split[1].String()))
	manager.mu.Lock()
	require.True(t, manager.dataKeyUsable())
	manager.mu.Unlock()
	manager.LockSigner()
	manager.mu.Lock()
	require.False(t, manager.dataKeyUsable())
	require.Nil(t, manager.deposit)
	require.Nil(t, manager.hot)
	manager.mu.Unlock()

	// 7. Re-split with the same data key replaces share A atomically.
	newSplit, err := shards.Split(dataKey, shards.DefaultThreshold, shards.DefaultTotal)
	require.NoError(t, err)
	expectAdvisoryLock()
	expectSetting(settingShards, recordJSON)
	expectVaultForUpdate(newVault)
	expectSettingWrite()
	expectAuditWrite()
	expectCommit()
	expectSetting(settingVault, newVault)
	expectSetting(settingShards, recordJSON)
	_, err = manager.ResplitShards(context.Background(), ShardSetupRequest{
		DataKey: dataKey, ShareA: newSplit[0].String(), ConfirmShare: newSplit[1].String(),
		Commitment: commitmentB64, RiskAcknowledged: true,
	})
	require.NoError(t, err)

	require.NoError(t, mock.ExpectationsWereMet())
}

func v3Vault(t *testing.T, dataKey []byte) string {
	t.Helper()
	raw, err := sealVaultContentsV3(dataKey, vaultContents{Deposit: testDeposit, Hot: testHot})
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(raw, managedVaultPrefixV3))
	return raw
}
