package service

import (
	"errors"
	"fmt"
	"strings"
)

// Proxy passwords are stored as AES-256-GCM ciphertext with the shared "enc:v1:"
// prefix. The pre-v2 managed-proxy format ("enc:managed:v1:" + base64) and
// pre-encryption plaintext rows stay readable so an upgrade never loses an
// outbound proxy credential; both are rewritten lazily.
const (
	// ProxyPasswordCipherPrefix mirrors secretcrypt.Prefix for readability at
	// call sites that reason about proxy storage.
	ProxyPasswordCipherPrefix = "enc:v1:"
	// LegacyManagedProxyCipherPrefix is the previous managed-proxy format.
	LegacyManagedProxyCipherPrefix = "enc:managed:v1:"
)

// ErrProxyPasswordUndecryptable reports a stored proxy password that cannot be
// read with the current installation key. Callers must fail closed rather than
// dialing the upstream without authentication.
var ErrProxyPasswordUndecryptable = errors.New("stored proxy password cannot be decrypted")

// IsManagedProxyUsername reports whether the proxy was created by the managed
// static-node importer. Managed proxies keep the release guard in the
// repository wrapper; ordinary proxies do not.
func IsManagedProxyUsername(username string) bool {
	return strings.HasPrefix(username, "mh_")
}

// EncryptProxyPassword returns the at-rest form of a proxy password. Empty
// passwords stay empty. When no installation key is configured the plaintext is
// kept (legacy / development mode); production bootstrap always provides one.
func EncryptProxyPassword(vault SecretVault, plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	if secretcryptIsEncrypted(plaintext) {
		return plaintext, nil
	}
	if !SecretVaultAvailable(vault) {
		return plaintext, nil
	}
	encrypted, err := vault.EncryptString(plaintext)
	if err != nil {
		return "", fmt.Errorf("encrypt proxy password: %w", err)
	}
	return encrypted, nil
}

// DecryptProxyPassword returns the plaintext password plus whether the stored
// value was encrypted and whether it should be lazily rewritten (legacy managed
// ciphertext or plaintext).
func DecryptProxyPassword(vault SecretVault, stored string) (plaintext string, needsRewrite bool, err error) {
	if stored == "" {
		return "", false, nil
	}
	switch {
	case secretcryptIsEncrypted(stored):
		plaintext, err = vault.DecryptString(stored)
		if err != nil {
			return "", false, fmt.Errorf("%w: %v", ErrProxyPasswordUndecryptable, err)
		}
		return plaintext, false, nil
	case strings.HasPrefix(stored, LegacyManagedProxyCipherPrefix):
		legacy := strings.TrimPrefix(stored, LegacyManagedProxyCipherPrefix)
		plaintext, err = vault.DecryptLegacyBase64(legacy)
		if err != nil {
			return "", false, fmt.Errorf("%w: %v", ErrProxyPasswordUndecryptable, err)
		}
		// Rewrite as v2 on the first read so only one format remains.
		return plaintext, true, nil
	default:
		// Pre-encryption plaintext.
		return stored, true, nil
	}
}

func secretcryptIsEncrypted(value string) bool {
	return strings.HasPrefix(value, ProxyPasswordCipherPrefix)
}

// ProxyPasswordIsEncrypted reports whether the stored password is ciphertext in
// either the current or the legacy managed format.
func ProxyPasswordIsEncrypted(stored string) bool {
	if stored == "" {
		return false
	}
	if secretcryptIsEncrypted(stored) {
		return true
	}
	return strings.HasPrefix(stored, LegacyManagedProxyCipherPrefix)
}
