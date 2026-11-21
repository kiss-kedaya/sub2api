package service

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/secretcrypt"
)

// SecretVault encrypts credential material at rest with the installation-wide
// AES-256 key (the same durable key that protects TOTP secrets and payment
// provider configs). Values written by the vault carry the "enc:v1:" prefix so
// readers can tell ciphertext from historical plaintext without guessing.
//
// The vault never degrades silently: when a value is encrypted and the key is
// missing or wrong, every decrypt returns an error and callers must fail closed
// instead of projecting an empty credential.
type SecretVault interface {
	// Available reports whether an installation key is loaded. When false new
	// writes stay in the legacy plaintext format so development / review
	// instances keep working; encrypted rows still fail closed on read.
	Available() bool
	EncryptString(plaintext string) (string, error)
	// EncryptStringDeterministic keeps ciphertext stable for equal plaintext so
	// existing JSONB equality guards keep working.
	EncryptStringDeterministic(plaintext, context string) (string, error)
	DecryptString(stored string) (string, error)
	// DecryptLegacyBase64 reads the pre-v2 managed-proxy ciphertext format.
	DecryptLegacyBase64(stored string) (string, error)
	IsEncrypted(stored string) bool
}

type installedSecretVault struct {
	key []byte
}

type unavailableSecretVault struct{}

// NewSecretVault builds the vault from the bootstrapped installation key. It
// never generates or rotates a key; the startup bootstrap owns that decision.
func NewSecretVault(cfg *config.Config) SecretVault {
	if cfg != nil {
		if key, err := secretcrypt.ParseKey(cfg.Totp.EncryptionKey); err == nil {
			return &installedSecretVault{key: key}
		}
	}
	return unavailableSecretVault{}
}

func (v *installedSecretVault) Available() bool { return v != nil && len(v.key) == secretcrypt.KeySize }

func (v *installedSecretVault) EncryptString(plaintext string) (string, error) {
	if !v.Available() {
		return "", secretcrypt.ErrKeyUnavailable
	}
	return secretcrypt.EncryptString(plaintext, v.key)
}

func (v *installedSecretVault) EncryptStringDeterministic(plaintext, context string) (string, error) {
	if !v.Available() {
		return "", secretcrypt.ErrKeyUnavailable
	}
	return secretcrypt.EncryptStringDeterministic(plaintext, context, v.key)
}

func (v *installedSecretVault) DecryptString(stored string) (string, error) {
	if !v.Available() {
		return "", secretcrypt.ErrKeyUnavailable
	}
	return secretcrypt.DecryptString(stored, v.key)
}

func (v *installedSecretVault) DecryptLegacyBase64(stored string) (string, error) {
	if !v.Available() {
		return "", secretcrypt.ErrKeyUnavailable
	}
	return secretcrypt.DecryptLegacyBase64(stored, v.key)
}

func (v *installedSecretVault) IsEncrypted(stored string) bool {
	return secretcrypt.IsEncrypted(stored)
}

func (unavailableSecretVault) Available() bool { return false }

func (unavailableSecretVault) EncryptString(string) (string, error) {
	return "", secretcrypt.ErrKeyUnavailable
}

func (unavailableSecretVault) EncryptStringDeterministic(string, string) (string, error) {
	return "", secretcrypt.ErrKeyUnavailable
}

func (unavailableSecretVault) DecryptString(string) (string, error) {
	return "", secretcrypt.ErrKeyUnavailable
}

func (unavailableSecretVault) DecryptLegacyBase64(string) (string, error) {
	return "", secretcrypt.ErrKeyUnavailable
}

func (unavailableSecretVault) IsEncrypted(stored string) bool { return secretcrypt.IsEncrypted(stored) }

// SecretVaultAvailable is a nil-safe helper for callers that only hold the
// interface value.
func SecretVaultAvailable(vault SecretVault) bool {
	if vault == nil {
		return false
	}
	return vault.Available()
}

// looksLikeUnexpectedCiphertext reports a value that looks encrypted but cannot
// be treated as legacy plaintext. Used to fail closed when no key is available.
func looksLikeUnexpectedCiphertext(value string) bool {
	return strings.HasPrefix(value, secretcrypt.Prefix)
}
