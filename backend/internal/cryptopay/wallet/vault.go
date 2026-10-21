package wallet

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/scrypt"
)

// The vault seals the mnemonic at rest with a passphrase the operator holds.
// Nothing in the ciphertext is usable without that passphrase, so a stolen disk or
// database dump is worthless on its own.
//
// Layout: base64(salt ‖ nonce ‖ ciphertext+tag)
//
//	salt  16 bytes, random per seal (scrypt input)
//	nonce 12 bytes, random per seal (GCM)
//
// scrypt parameters follow the "interactive" guidance (N=2^17, r=8, p=1):
// ~128 MB and a fraction of a second per attempt, which makes an offline
// brute force of a human passphrase expensive.
const (
	vaultSaltLen = 16
	scryptN      = 1 << 17
	scryptR      = 8
	scryptP      = 1
	scryptKeyLen = 32
)

// ErrVaultLocked is returned when signing is attempted before an unlock.
var ErrVaultLocked = errors.New("wallet is locked: unlock with the admin passphrase first")

// vaultContents is the plaintext inside the vault: both seeds share one blob.
type vaultContents struct {
	Deposit string `json:"deposit"`
	Hot     string `json:"hot"`
}

// sealContents returns the base64 vault blob for both seeds.
func sealContents(depositMnemonic, hotMnemonic, passphrase string) (string, error) {
	if passphrase == "" {
		return "", fmt.Errorf("passphrase must not be empty")
	}
	plaintext, err := json.Marshal(vaultContents{Deposit: depositMnemonic, Hot: hotMnemonic})
	if err != nil {
		return "", fmt.Errorf("encode vault contents: %w", err)
	}
	defer clear(plaintext)
	salt := make([]byte, vaultSaltLen)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}
	key, err := scrypt.Key([]byte(passphrase), salt, scryptN, scryptR, scryptP, scryptKeyLen)
	if err != nil {
		return "", fmt.Errorf("derive key: %w", err)
	}
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create gcm: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	sealed := gcm.Seal(nil, nonce, plaintext, nil)

	blob := make([]byte, 0, len(salt)+len(nonce)+len(sealed))
	blob = append(blob, salt...)
	blob = append(blob, nonce...)
	blob = append(blob, sealed...)
	return base64.StdEncoding.EncodeToString(blob), nil
}

// openVaultString decrypts a vault blob held in memory (settings row).
func openVaultString(encoded, passphrase string) (*vaultContents, error) {
	blob, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("vault is not valid base64: %w", err)
	}
	if len(blob) < vaultSaltLen+12 {
		return nil, fmt.Errorf("vault is truncated")
	}
	salt := blob[:vaultSaltLen]
	rest := blob[vaultSaltLen:]
	key, err := scrypt.Key([]byte(passphrase), salt, scryptN, scryptR, scryptP, scryptKeyLen)
	if err != nil {
		return nil, fmt.Errorf("derive key: %w", err)
	}
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create gcm: %w", err)
	}
	nonceSize := gcm.NonceSize()
	if len(rest) < nonceSize {
		return nil, fmt.Errorf("vault is truncated")
	}
	nonce, ciphertext := rest[:nonceSize], rest[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("wrong passphrase")
	}
	defer clear(plaintext)
	var contents vaultContents
	if err := json.Unmarshal(plaintext, &contents); err != nil {
		return nil, fmt.Errorf("vault contents are corrupt: %w", err)
	}
	return &contents, nil
}
