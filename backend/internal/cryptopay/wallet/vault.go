package wallet

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
)

// Seeds use an application-managed, domain-separated encryption key derived
// from the installation's persistent master key. No operator password, signing
// lock, separate key file or unlock request is part of normal wallet operation.
const managedVaultPrefix = "sub2api-wallet:v2:"
const walletKeyContext = "sub2api/crypto-wallet/seed-encryption/v2"

type vaultContents struct {
	Deposit         string `json:"deposit"`
	Hot             string `json:"hot"`
	SecurityVersion int    `json:"security_version,omitempty"`
}

func newSeedCipher(masterHex string) cipher.AEAD {
	master, err := hex.DecodeString(masterHex)
	if err != nil || len(master) != 32 {
		clear(master)
		return nil
	}
	defer clear(master)
	key, err := hkdf.Key(sha256.New, master, nil, walletKeyContext, 32)
	if err != nil {
		return nil
	}
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil
	}
	aead, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return nil
	}
	return aead
}

func (m *Manager) sealContents(deposit, hot string) (string, error) {
	contents := vaultContents{Deposit: deposit, Hot: hot}
	if m.securityRegistered {
		contents.SecurityVersion = 1
	}
	return m.sealVaultContents(contents)
}

func (m *Manager) sealVaultContents(contents vaultContents) (string, error) {
	if m.cipher == nil {
		return "", ErrVaultUnavailable
	}
	plaintext, err := json.Marshal(contents)
	if err != nil {
		return "", ErrVaultUnavailable
	}
	defer clear(plaintext)
	sealed := m.cipher.Seal(nil, nil, plaintext, []byte(managedVaultPrefix))
	return managedVaultPrefix + base64.StdEncoding.EncodeToString(sealed), nil
}

func (m *Manager) openContents(raw string) (*vaultContents, error) {
	if m.cipher == nil || !strings.HasPrefix(raw, managedVaultPrefix) {
		// Legacy ciphertext must never be treated as absent or overwritten.
		return nil, ErrVaultUnavailable
	}
	sealed, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(raw, managedVaultPrefix))
	if err != nil {
		return nil, ErrVaultUnavailable
	}
	plaintext, err := m.cipher.Open(nil, nil, sealed, []byte(managedVaultPrefix))
	if err != nil {
		return nil, ErrVaultUnavailable
	}
	defer clear(plaintext)
	var contents vaultContents
	if json.Unmarshal(plaintext, &contents) != nil {
		return nil, ErrVaultUnavailable
	}
	return &contents, nil
}
