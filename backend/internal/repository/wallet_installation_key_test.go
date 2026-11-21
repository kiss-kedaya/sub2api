package repository

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/wallet"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/wallet/shards"
	"github.com/stretchr/testify/require"
)

const walletStartupTestMasterKey = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"

const startupSettingsQuery = `SELECT value FROM settings WHERE key = \$1`

// sealStartupTestV2 mimics the installation-key vault format (HKDF domain
// separated key + AES-256-GCM with the v2 prefix as AAD).
func sealStartupTestV2(t *testing.T, masterHex string) string {
	t.Helper()
	master, err := hex.DecodeString(masterHex)
	require.NoError(t, err)
	key, err := hkdf.Key(sha256.New, master, nil, "sub2api/crypto-wallet/seed-encryption/v2", 32)
	require.NoError(t, err)
	block, err := aes.NewCipher(key)
	require.NoError(t, err)
	aead, err := cipher.NewGCMWithRandomNonce(block)
	require.NoError(t, err)
	payload, err := json.Marshal(map[string]string{
		"deposit": "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about",
		"hot":     "legal winner thank year wave sausage worth useful legal winner thank yellow",
	})
	require.NoError(t, err)
	return "sub2api-wallet:v2:" + base64.StdEncoding.EncodeToString(aead.Seal(nil, nil, payload, []byte("sub2api-wallet:v2:")))
}

// sealStartupTestV3 mimics the browser data-key vault format.
func sealStartupTestV3(t *testing.T, dataKey []byte) string {
	t.Helper()
	block, err := aes.NewCipher(dataKey)
	require.NoError(t, err)
	aead, err := cipher.NewGCMWithRandomNonce(block)
	require.NoError(t, err)
	payload, err := json.Marshal(map[string]any{
		"deposit":          "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about",
		"hot":              "legal winner thank year wave sausage worth useful legal winner thank yellow",
		"security_version": 1,
	})
	require.NoError(t, err)
	return "sub2api-wallet:v3:" + base64.StdEncoding.EncodeToString(aead.Seal(nil, nil, payload, []byte("sub2api-wallet:v3:")))
}

// startupShardRecord builds the settings JSON with share A sealed exactly like
// the wallet manager seals it.
func startupShardRecord(t *testing.T, masterHex string, dataKey []byte) string {
	t.Helper()
	split, err := shards.Split(dataKey, shards.DefaultThreshold, shards.DefaultTotal)
	require.NoError(t, err)
	master, err := hex.DecodeString(masterHex)
	require.NoError(t, err)
	key, err := hkdf.Key(sha256.New, master, nil, "sub2api/crypto-wallet/shard-a/v1", 32)
	require.NoError(t, err)
	block, err := aes.NewCipher(key)
	require.NoError(t, err)
	aead, err := cipher.NewGCMWithRandomNonce(block)
	require.NoError(t, err)
	sealedA := base64.StdEncoding.EncodeToString(aead.Seal(nil, nil, []byte(split[0].String()), []byte("sub2api/crypto-wallet/shard-a/v1")))
	commitment := shards.Commitment(dataKey)
	record, err := json.Marshal(map[string]any{
		"version":       1,
		"scheme":        shards.Scheme,
		"threshold":     shards.DefaultThreshold,
		"total":         shards.DefaultTotal,
		"share_a":       sealedA,
		"dk_commitment": base64.StdEncoding.EncodeToString(commitment[:]),
		"created_at":    "2026-01-01T00:00:00Z",
		"created_by":    1,
		"updated_at":    "2026-01-01T00:00:00Z",
	})
	require.NoError(t, err)
	return string(record)
}

func expectStartupSetting(mock sqlmock.Sqlmock, value string) {
	mock.ExpectQuery(startupSettingsQuery).
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(value))
}

func TestWalletCiphertextUsableWithInstallationKeyV2Vault(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	expectStartupSetting(mock, sealStartupTestV2(t, walletStartupTestMasterKey))

	require.True(t, walletCiphertextUsableWithInstallationKey(context.Background(), db, walletStartupTestMasterKey))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestWalletCiphertextUsableWithInstallationKeyLockedV3Vault(t *testing.T) {
	dataKey := []byte(strings.Repeat("\x5a", 32))
	record := startupShardRecord(t, walletStartupTestMasterKey, dataKey)
	vault := sealStartupTestV3(t, dataKey)

	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	// Status: read vault, then the locked branch reads the vault and the shards
	// record again. VerifyInstallationKey re-reads the record.
	expectStartupSetting(mock, vault)
	mock.ExpectQuery(startupSettingsQuery).WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(vault))
	mock.ExpectQuery(startupSettingsQuery).WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(record))
	mock.ExpectQuery(startupSettingsQuery).WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(record))

	// A locked v3 vault is a supported steady state: the installation key only
	// has to open the server-held share A.
	require.True(t, walletCiphertextUsableWithInstallationKey(context.Background(), db, walletStartupTestMasterKey))
	require.NoError(t, mock.ExpectationsWereMet())

	// The wrong installation key must fail closed.
	wrongDB, wrongMock, err := sqlmock.New()
	require.NoError(t, err)
	wrongDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = wrongDB.Close() })
	expectStartupSetting(wrongMock, vault)
	wrongMock.ExpectQuery(startupSettingsQuery).WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(vault))
	wrongMock.ExpectQuery(startupSettingsQuery).WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(record))
	wrongMock.ExpectQuery(startupSettingsQuery).WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(record))
	require.False(t, walletCiphertextUsableWithInstallationKey(context.Background(), wrongDB, strings.Repeat("11", 32)))
	require.NoError(t, wrongMock.ExpectationsWereMet())
}

func TestWalletCiphertextUsableWithInstallationKeyV3WithoutShardsFailsClosed(t *testing.T) {
	dataKey := []byte(strings.Repeat("\x37", 32))
	vault := sealStartupTestV3(t, dataKey)

	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	expectStartupSetting(mock, vault)
	mock.ExpectQuery(startupSettingsQuery).WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(vault))
	expectStartupSetting(mock, "")

	require.False(t, walletCiphertextUsableWithInstallationKey(context.Background(), db, walletStartupTestMasterKey))
	require.NoError(t, mock.ExpectationsWereMet())
}

var (
	_ = wallet.New
	_ = strings.TrimSpace
)
