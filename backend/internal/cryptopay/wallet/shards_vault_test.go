package wallet

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/wallet/shards"
	"github.com/stretchr/testify/require"
)

const settingsValueQuery = `SELECT value FROM settings WHERE key = \$1`

type shardFixture struct {
	dataKey  []byte
	shareA   string
	shareB   string
	shareC   string
	record   string
	vaultRaw string
}

func newShardFixture(t *testing.T) shardFixture {
	t.Helper()
	dataKey := bytes.Repeat([]byte{0x5a}, 32)
	split, err := shards.Split(dataKey, shards.DefaultThreshold, shards.DefaultTotal)
	require.NoError(t, err)
	sealer := New(nil, testMasterKey)
	sealedA, err := sealer.sealMasterContext([]byte(split[0].String()), shareACipherContext)
	require.NoError(t, err)
	vaultRaw, err := sealVaultContentsV3(dataKey, vaultContents{Deposit: testDeposit, Hot: testHot})
	require.NoError(t, err)
	commitment := shards.Commitment(dataKey)
	record, err := json.Marshal(shardRecord{
		Version: shardRecordVersion, Scheme: shards.Scheme,
		Threshold: shards.DefaultThreshold, Total: shards.DefaultTotal,
		ShareA: sealedA, Commitment: base64.StdEncoding.EncodeToString(commitment[:]),
		CreatedAt: "2026-01-01T00:00:00Z", CreatedBy: 1,
	})
	require.NoError(t, err)
	return shardFixture{
		dataKey: dataKey, shareA: split[0].String(), shareB: split[1].String(), shareC: split[2].String(),
		record: string(record), vaultRaw: vaultRaw,
	}
}

func shardTestManager(t *testing.T) (*Manager, sqlmock.Sqlmock, shardFixture) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	m := New(db, testMasterKey)
	fixture := newShardFixture(t)
	return m, mock, fixture
}

func mustRecordJSON(t *testing.T, record shardRecord) string {
	t.Helper()
	encoded, err := json.Marshal(record)
	require.NoError(t, err)
	return string(encoded)
}

func expectShareAttempts(mock sqlmock.Sqlmock, failed int) {
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM crypto_signer_audit").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(failed))
}

func expectSettingValue(mock sqlmock.Sqlmock, value string) {
	mock.ExpectQuery(settingsValueQuery).
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(value))
}

func expectShareAudit(t *testing.T, mock sqlmock.Sqlmock, result string) {
	t.Helper()
	mock.ExpectExec("INSERT INTO crypto_signer_audit").
		WillReturnResult(sqlmock.NewResult(1, 1))
}

func TestVaultV3RoundTripAndWrongKeyRejected(t *testing.T) {
	dataKey := bytes.Repeat([]byte{0x11}, 32)
	raw, err := sealVaultContentsV3(dataKey, vaultContents{Deposit: testDeposit, Hot: testHot})
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(raw, managedVaultPrefixV3))
	require.Equal(t, 3, vaultVersion(raw))

	opened, err := openVaultContentsV3(dataKey, raw)
	require.NoError(t, err)
	require.Equal(t, testDeposit, opened.Deposit)
	require.Equal(t, testHot, opened.Hot)

	wrong := bytes.Repeat([]byte{0x22}, 32)
	_, err = openVaultContentsV3(wrong, raw)
	require.ErrorIs(t, err, ErrVaultUnavailable)

	// A v3 ciphertext must never be parsed as the installation-key format.
	m := New(nil, testMasterKey)
	_, err = m.openContents(raw)
	require.ErrorIs(t, err, ErrSignerShareRequired)
}

func TestSetupParamsRequireBackfillAndCommitment(t *testing.T) {
	dataKey := bytes.Repeat([]byte{0x33}, 32)
	split, err := shards.Split(dataKey, shards.DefaultThreshold, shards.DefaultTotal)
	require.NoError(t, err)
	commitment := shards.Commitment(dataKey)
	valid := ShardSetupRequest{
		DataKey: dataKey, ShareA: split[0].String(), ConfirmShare: split[1].String(),
		Commitment: base64.StdEncoding.EncodeToString(commitment[:]),
	}
	m := New(nil, testMasterKey)
	shareA, published, parsedCommitment, err := m.shardSetupParams(valid)
	require.NoError(t, err)
	require.Equal(t, split[0].String(), shareA)
	require.Equal(t, valid.Commitment, published)
	require.Equal(t, commitment, parsedCommitment)

	// The operator must type back a share that belongs to the same split.
	wrongBackfill := valid
	wrongBackfill.ConfirmShare = split[2].String() + "X"
	_, _, _, err = m.shardSetupParams(wrongBackfill)
	require.ErrorIs(t, err, ErrSignerShareInvalid)

	// A differing data key is rejected even with a well-formed commitment.
	wrongKey := valid
	wrongKey.DataKey = bytes.Repeat([]byte{0x44}, 32)
	_, _, _, err = m.shardSetupParams(wrongKey)
	require.ErrorIs(t, err, ErrSignerShareInvalid)

	// Commitment must match the data key.
	wrongCommitment := valid
	wrongCommitment.Commitment = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x00}, 32))
	_, _, _, err = m.shardSetupParams(wrongCommitment)
	require.ErrorIs(t, err, ErrSignerShareInvalid)

	// A share from a different split cannot be mixed in.
	otherSplit, err := shards.Split(dataKey, shards.DefaultThreshold, shards.DefaultTotal)
	require.NoError(t, err)
	mixed := valid
	mixed.ConfirmShare = otherSplit[1].String()
	_, _, _, err = m.shardSetupParams(mixed)
	require.ErrorIs(t, err, ErrSignerShareInvalid)

	// Malformed sizes fail before any database access.
	_, _, _, err = m.shardSetupParams(ShardSetupRequest{})
	require.ErrorIs(t, err, ErrSignerShareInvalid)
}

func TestRecoverDataKeyNeedsServerAndOperatorShare(t *testing.T) {
	m, mock, fixture := shardTestManager(t)
	expectShareAttempts(mock, 0)
	expectSettingValue(mock, fixture.record)
	expectSettingValue(mock, fixture.vaultRaw)
	recovered, err := m.recoverDataKey(context.Background(), fixture.shareB)
	require.NoError(t, err)
	require.Equal(t, fixture.dataKey, recovered)
	clear(recovered)

	// Share A alone is server-held and useless: duplicates are rejected.
	expectShareAttempts(mock, 0)
	expectSettingValue(mock, fixture.record)
	expectShareAudit(t, mock, "denied")
	_, err = m.recoverDataKey(context.Background(), fixture.shareA)
	require.ErrorIs(t, err, ErrSignerShareInvalid)

	// A syntactically valid share from another split is rejected by the
	// commitment check, with no information about "how close" it was.
	other := newShardFixture(t)
	expectShareAttempts(mock, 0)
	expectSettingValue(mock, fixture.record)
	expectShareAudit(t, mock, "denied")
	_, err = m.recoverDataKey(context.Background(), other.shareB)
	require.ErrorIs(t, err, ErrSignerShareInvalid)
}

func TestRecoverDataKeyRateLimitedAfterFiveFailures(t *testing.T) {
	m, mock, fixture := shardTestManager(t)
	expectShareAttempts(mock, shareFailureLimit)
	_, err := m.recoverDataKey(context.Background(), fixture.shareB)
	require.ErrorIs(t, err, ErrSignerRateLimited)
}

func TestUnlockSignerThenLockZeroesKeyAndSeeds(t *testing.T) {
	m, mock, fixture := shardTestManager(t)
	expectShareAttempts(mock, 0)
	expectSettingValue(mock, fixture.record)
	expectSettingValue(mock, fixture.vaultRaw)
	require.NoError(t, m.UnlockSigner(context.Background(), fixture.shareB))
	m.mu.Lock()
	require.Len(t, m.dataKey, 32)
	require.True(t, m.dataKeyUsable())
	m.mu.Unlock()

	m.LockSigner()
	m.mu.Lock()
	require.Nil(t, m.dataKey)
	require.False(t, m.dataKeyUsable())
	require.Nil(t, m.deposit)
	require.Nil(t, m.hot)
	m.mu.Unlock()
}

func TestLockedV3VaultRequiresShareForSeedAccess(t *testing.T) {
	m, mock, fixture := shardTestManager(t)
	expectSettingValue(mock, fixture.vaultRaw)
	_, err := readVault(context.Background(), m.db, false)
	require.NoError(t, err)

	// No data key in memory: opening the vault must fail closed.
	contents, err := m.openContents(fixture.vaultRaw)
	require.Nil(t, contents)
	require.ErrorIs(t, err, ErrSignerShareRequired)
}

func TestShardStatusNeverExposesShareMaterial(t *testing.T) {
	m, mock, fixture := shardTestManager(t)
	expectSettingValue(mock, fixture.vaultRaw)
	expectSettingValue(mock, fixture.record)
	status, err := m.ShardStatus(context.Background())
	require.NoError(t, err)
	require.True(t, status.Configured)
	require.True(t, status.Locked)
	require.Equal(t, 3, status.VaultVersion)
	require.Equal(t, shards.DefaultThreshold, status.Threshold)
	encoded, err := json.Marshal(status)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), fixture.shareA)
	require.NotContains(t, string(encoded), fixture.shareB)
	require.NotContains(t, string(encoded), base64.StdEncoding.EncodeToString(fixture.dataKey))
}

func TestHotAddressesFallBackToPublicSnapshotWhenLocked(t *testing.T) {
	m, mock, fixture := shardTestManager(t)
	cached := `{"evm":"0x1111111111111111111111111111111111111111","solana":"11111111111111111111111111111111"}`
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT value FROM settings WHERE key = \$1 FOR SHARE`).
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(fixture.vaultRaw))
	mock.ExpectRollback()
	expectSettingValue(mock, cached)
	hot, err := m.HotAddresses(context.Background())
	require.NoError(t, err)
	require.Equal(t, "0x1111111111111111111111111111111111111111", hot.EVM)
}

func TestAutoSweepEnabledReadsSealedFlagOnly(t *testing.T) {
	m, mock, fixture := shardTestManager(t)
	var record shardRecord
	require.NoError(t, json.Unmarshal([]byte(fixture.record), &record))
	record.AutoEnabled = true
	record.AutoShare = "sealed-placeholder"
	encoded, err := json.Marshal(record)
	require.NoError(t, err)
	expectSettingValue(mock, string(encoded))
	enabled, err := m.AutoSweepEnabled(context.Background())
	require.NoError(t, err)
	require.True(t, enabled)
}

func TestReadSettingMissingRowIsNotAnError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectQuery(settingsValueQuery).WillReturnRows(sqlmock.NewRows([]string{"value"}))
	value, err := readSetting(context.Background(), db, settingShards)
	require.NoError(t, err)
	require.Empty(t, value)
}

func TestAuthorizeSweepRejectsLockedV3Vault(t *testing.T) {
	m, mock, fixture := shardTestManager(t)
	m.SetEnabled(true)
	require.NoError(t, m.SetDestination(testDestination))
	expectSettingValue(mock, fixture.vaultRaw)
	locked, err := m.SignerLocked(context.Background())
	require.NoError(t, err)
	require.True(t, locked)
	_, _, err = m.AuthorizeSweep(context.Background())
	require.ErrorIs(t, err, ErrSignerShareRequired)
}

func TestVerifyInstallationKeyUnsealsServerHeldShare(t *testing.T) {
	m, mock, fixture := shardTestManager(t)
	expectSettingValue(mock, fixture.record)
	require.NoError(t, m.VerifyInstallationKey(context.Background()))

	// A different installation key must be reported as unusable, so startup can
	// fail closed instead of silently accepting a wrong environment key.
	db, otherMock, err := sqlmock.New()
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	wrong := New(db, strings.Repeat("11", 32))
	expectSettingValue(otherMock, fixture.record)
	require.ErrorIs(t, wrong.VerifyInstallationKey(context.Background()), ErrSignerShareInvalid)

	// A v3 vault without a configured shard record is unrecoverable.
	emptyDB, emptyMock, err := sqlmock.New()
	require.NoError(t, err)
	emptyDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = emptyDB.Close() })
	empty := New(emptyDB, testMasterKey)
	expectSettingValue(emptyMock, "")
	require.ErrorIs(t, empty.VerifyInstallationKey(context.Background()), ErrShardsNotConfigured)
}

var _ = sql.ErrNoRows
