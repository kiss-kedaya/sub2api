package payment

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
)

// ProviderConfigCipherPrefix marks provider-instance config values written by
// the encrypted write path. Values without the prefix are either legacy
// AES-GCM ciphertext ("iv:authTag:ciphertext", produced before the plaintext
// migration) or pre-encryption plaintext JSON, both of which stay readable so
// an upgrade never loses an existing merchant configuration.
const ProviderConfigCipherPrefix = "v2:"

// ErrProviderConfigKeyUnavailable is returned when encrypted provider config
// cannot be produced or read because the installation encryption key is
// missing. Callers must fail closed instead of falling back to plaintext.
var ErrProviderConfigKeyUnavailable = errors.New("payment provider config encryption key is unavailable")

// AES256KeySize is the required key length (in bytes) for AES-256-GCM.
const AES256KeySize = 32

// Encrypt encrypts plaintext using AES-256-GCM with the given 32-byte key.
// The output format is "iv:authTag:ciphertext" where each component is base64-encoded,
// matching the Node.js crypto.ts format for cross-compatibility.
//
// It is the building block for provider config encryption; prefer
// EncryptProviderConfig, which adds the version prefix.
func Encrypt(plaintext string, key []byte) (string, error) {
	if len(key) != AES256KeySize {
		return "", fmt.Errorf("encryption key must be %d bytes, got %d", AES256KeySize, len(key))
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("create AES cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create GCM: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize()) // 12 bytes for GCM
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}

	// Seal appends the ciphertext + auth tag
	sealed := gcm.Seal(nil, nonce, []byte(plaintext), nil)

	// Split sealed into ciphertext and auth tag (last 16 bytes)
	tagSize := gcm.Overhead()
	ciphertext := sealed[:len(sealed)-tagSize]
	authTag := sealed[len(sealed)-tagSize:]

	// Format: iv:authTag:ciphertext (all base64)
	return fmt.Sprintf("%s:%s:%s",
		base64.StdEncoding.EncodeToString(nonce),
		base64.StdEncoding.EncodeToString(authTag),
		base64.StdEncoding.EncodeToString(ciphertext),
	), nil
}

// Decrypt decrypts a ciphertext string produced by Encrypt.
// The input format is "iv:authTag:ciphertext" where each component is base64-encoded.
//
// Callers reading provider-instance configs should use DecryptProviderConfig,
// which also recognises the version prefix and legacy plaintext rows.
func Decrypt(ciphertext string, key []byte) (string, error) {
	if len(key) != AES256KeySize {
		return "", fmt.Errorf("encryption key must be %d bytes, got %d", AES256KeySize, len(key))
	}

	parts := strings.SplitN(ciphertext, ":", 3)
	if len(parts) != 3 {
		return "", fmt.Errorf("invalid ciphertext format: expected iv:authTag:ciphertext")
	}

	nonce, err := base64.StdEncoding.DecodeString(parts[0])
	if err != nil {
		return "", fmt.Errorf("decode IV: %w", err)
	}

	authTag, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("decode auth tag: %w", err)
	}

	encrypted, err := base64.StdEncoding.DecodeString(parts[2])
	if err != nil {
		return "", fmt.Errorf("decode ciphertext: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("create AES cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create GCM: %w", err)
	}

	// Reconstruct the sealed data: ciphertext + authTag
	sealed := append(encrypted, authTag...)

	plaintext, err := gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}

	return string(plaintext), nil
}

// EncryptProviderConfig encrypts a provider-instance config document for
// storage. The version prefix lets readers distinguish current ciphertext from
// legacy rows without guessing.
func EncryptProviderConfig(plaintext string, key []byte) (string, error) {
	encrypted, err := Encrypt(plaintext, key)
	if err != nil {
		return "", err
	}
	return ProviderConfigCipherPrefix + encrypted, nil
}

// DecryptProviderConfig reads a stored provider-instance config document.
//
// Accepted inputs:
//   - "v2:" + AES-256-GCM ciphertext (current write path)
//   - plain "iv:authTag:ciphertext" (pre-plaintext legacy rows)
//   - plaintext (pre-encryption rows); returned unchanged for lazy migration
//
// Encrypted input fails closed when the key is missing or authentication
// fails: it is never returned as plaintext.
func DecryptProviderConfig(stored string, key []byte) (string, error) {
	if stored == "" {
		return "", nil
	}
	switch {
	case strings.HasPrefix(stored, ProviderConfigCipherPrefix):
		if len(key) != AES256KeySize {
			return "", ErrProviderConfigKeyUnavailable
		}
		return Decrypt(strings.TrimPrefix(stored, ProviderConfigCipherPrefix), key)
	case looksLikeLegacyProviderCiphertext(stored):
		if len(key) != AES256KeySize {
			return "", ErrProviderConfigKeyUnavailable
		}
		return Decrypt(stored, key)
	default:
		// Pre-encryption plaintext row. Callers validate the JSON and re-save
		// through EncryptProviderConfig on the next write/migration.
		return stored, nil
	}
}

// IsEncryptedProviderConfig reports whether the stored value is ciphertext
// written by either the current or the legacy encrypted path. It never
// decrypts, so it is safe to call for migration decisions.
func IsEncryptedProviderConfig(stored string) bool {
	return strings.HasPrefix(stored, ProviderConfigCipherPrefix) || looksLikeLegacyProviderCiphertext(stored)
}

func looksLikeLegacyProviderCiphertext(stored string) bool {
	parts := strings.Split(stored, ":")
	if len(parts) != 3 {
		return false
	}
	nonce, nonceErr := base64.StdEncoding.DecodeString(parts[0])
	authTag, tagErr := base64.StdEncoding.DecodeString(parts[1])
	if nonceErr != nil || tagErr != nil || len(nonce) != 12 || len(authTag) != 16 {
		return false
	}
	_, cipherErr := base64.StdEncoding.DecodeString(parts[2])
	return cipherErr == nil
}
