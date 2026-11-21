// Package secretcrypt provides the small, shared primitive used to encrypt
// credential material "at rest" with the installation-wide AES-256 key.
//
// Storage format
//
//	enc:v1:<base64(nonce || AES-256-GCM ciphertext || tag)>
//
// The "enc:v1:" prefix lets readers distinguish ciphertext written by this package
// from historical plaintext without guessing. Values that do not carry the
// prefix are treated as legacy plaintext by the callers so an upgrade never
// loses an existing credential; the callers re-encrypt them lazily.
//
// The package deliberately exposes no plaintext in error values: every error is
// a stable, generic message so it can be logged from a failed read without
// leaking the credential it was trying to protect.
package secretcrypt

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Prefix marks a value produced by this package.
const Prefix = "enc:v1:"

// KeySize is the required key length in bytes for AES-256-GCM.
const KeySize = 32

var (
	// ErrKeyUnavailable reports that encryption or decryption was requested
	// without a usable installation key. Callers must fail closed: an encrypted
	// value is never replaced by an empty credential.
	ErrKeyUnavailable = errors.New("secrets encryption key is unavailable")
	// ErrMalformed reports a value that claims to be ciphertext but cannot be
	// framed as one. It never contains the offending value.
	ErrMalformed = errors.New("encrypted value is malformed")
	// ErrAuthentication reports an AES-GCM authentication failure (wrong key or
	// tampered ciphertext). It never contains the offending value.
	ErrAuthentication = errors.New("encrypted value failed authentication")
)

// ParseKey decodes the hexadecimal installation key. An empty or malformed key
// yields ErrKeyUnavailable so callers can distinguish "not configured" from a
// corrupt value.
func ParseKey(hexKey string) ([]byte, error) {
	trimmed := strings.TrimSpace(hexKey)
	if trimmed == "" {
		return nil, ErrKeyUnavailable
	}
	key, err := hex.DecodeString(trimmed)
	if err != nil || len(key) != KeySize {
		return nil, ErrKeyUnavailable
	}
	return key, nil
}

// IsEncrypted reports whether the stored value carries the ciphertext prefix.
// It never decrypts, so it is safe for migration decisions.
func IsEncrypted(stored string) bool {
	return strings.HasPrefix(stored, Prefix)
}

// EncryptString encrypts plaintext with a fresh random 96-bit nonce. The
// returned value carries Prefix and is safe to store as text.
func EncryptString(plaintext string, key []byte) (string, error) {
	nonce := make([]byte, 12)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	return seal(plaintext, nonce, key)
}

// EncryptStringDeterministic encrypts plaintext with a nonce derived from
// HMAC-SHA256(key, domain || context || plaintext). Equal plaintext under the
// same context produces identical ciphertext while different plaintext always
// produces a different nonce (PRF output). Deterministic encryption is required
// by callers whose existing SQL compares the stored JSONB document byte for
// byte as a compare-and-swap guard; it is not used by default.
func EncryptStringDeterministic(plaintext, context string, key []byte) (string, error) {
	if len(key) != KeySize {
		return "", ErrKeyUnavailable
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte("sub2api:secretcrypt:nonce:v1\x00" + context + "\x00" + plaintext))
	nonce := mac.Sum(nil)[:12]
	return seal(plaintext, nonce, key)
}

func seal(plaintext string, nonce, key []byte) (string, error) {
	if len(key) != KeySize {
		return "", ErrKeyUnavailable
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("create AES cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create GCM: %w", err)
	}
	sealed := gcm.Seal(nil, nonce, []byte(plaintext), nil)
	payload := make([]byte, 0, len(nonce)+len(sealed))
	payload = append(payload, nonce...)
	payload = append(payload, sealed...)
	return Prefix + base64.StdEncoding.EncodeToString(payload), nil
}

// DecryptString decrypts a value produced by EncryptString or
// EncryptStringDeterministic. A value without the ciphertext prefix is rejected
// with ErrMalformed; callers that support legacy plaintext inspect IsEncrypted
// first and never pass plaintext here.
func DecryptString(stored string, key []byte) (string, error) {
	if len(key) != KeySize {
		return "", ErrKeyUnavailable
	}
	if !strings.HasPrefix(stored, Prefix) {
		return "", ErrMalformed
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(stored, Prefix))
	if err != nil {
		return "", ErrMalformed
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("create AES cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create GCM: %w", err)
	}
	nonceSize := gcm.NonceSize()
	if len(raw) < nonceSize+gcm.Overhead() {
		return "", ErrMalformed
	}
	plaintext, err := gcm.Open(nil, raw[:nonceSize], raw[nonceSize:], nil)
	if err != nil {
		return "", ErrAuthentication
	}
	return string(plaintext), nil
}

// DecryptLegacyBase64 decrypts the pre-v2 format used for managed proxy
// passwords: base64(nonce || AES-256-GCM ciphertext || tag) without a prefix.
// It exists only so existing rows stay readable while they migrate to v2.
func DecryptLegacyBase64(stored string, key []byte) (string, error) {
	if len(key) != KeySize {
		return "", ErrKeyUnavailable
	}
	raw, err := base64.StdEncoding.DecodeString(stored)
	if err != nil {
		return "", ErrMalformed
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("create AES cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create GCM: %w", err)
	}
	nonceSize := gcm.NonceSize()
	if len(raw) < nonceSize+gcm.Overhead() {
		return "", ErrMalformed
	}
	plaintext, err := gcm.Open(nil, raw[:nonceSize], raw[nonceSize:], nil)
	if err != nil {
		return "", ErrAuthentication
	}
	return string(plaintext), nil
}
