package service

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Credentials are the only place upstream provider secrets are persisted for an
// account. Every value whose key is listed in SensitiveCredentialKeys is stored
// as deterministic AES-256-GCM ciphertext ("enc:v1:" + base64(nonce||sealed)); the
// surrounding JSONB document keeps its shape so existing queries that read
// non-secret fields (base_url, model_mapping, oauth_type, ...) keep working.
//
// Deterministic encryption is deliberate: the repository uses whole-document
// JSONB equality (`credentials = $N::jsonb`) as a compare-and-swap guard, and an
// equal plaintext must produce an equal document at rest. The nonce is derived
// with HMAC-SHA256(master, domain||key||plaintext), i.e. it is a PRF of the
// plaintext, so nonce reuse only happens for identical plaintext where it is
// harmless.
//
// Reads are fail-closed: a value that claims to be ciphertext must decrypt, or
// the account load fails instead of silently projecting an empty credential.

// credentialCipherContext separates the deterministic nonce domain for each
// credential sub-key.
func credentialCipherContext(key string) string {
	return "account.credentials:" + key
}

// IsSensitiveCredential reports whether a credential sub-key must be encrypted
// at rest. It reuses the response-redaction registry so a newly added secret
// cannot be redacted for the frontend yet stored in plaintext.
func IsSensitiveCredential(key string) bool {
	return IsSensitiveCredentialKey(key)
}

// EncryptCredentialsForStorage returns a copy of credentials with every
// sensitive sub-key replaced by ciphertext. Already-encrypted values are kept
// as-is so the function is idempotent.
func EncryptCredentialsForStorage(vault SecretVault, credentials map[string]any) (map[string]any, error) {
	if len(credentials) == 0 {
		return map[string]any{}, nil
	}
	if !SecretVaultAvailable(vault) {
		// No installation key (development / read-only review instance): keep the
		// legacy plaintext format. Encrypted rows still fail closed on read and
		// the startup bootstrap refuses to run with unreadable ciphertext.
		return copyAnyMap(credentials), nil
	}
	out := make(map[string]any, len(credentials))
	for key, value := range credentials {
		if !IsSensitiveCredential(key) || value == nil {
			out[key] = value
			continue
		}
		encrypted, err := encryptCredentialValue(vault, key, value)
		if err != nil {
			return nil, err
		}
		out[key] = encrypted
	}
	return out, nil
}

// DecryptCredentialsFromStorage returns a copy of stored credentials with every
// sensitive sub-key decrypted. needsRewrite reports that at least one sensitive
// value was still legacy plaintext so the caller can lazily re-encrypt the row.
func DecryptCredentialsFromStorage(vault SecretVault, stored map[string]any) (map[string]any, bool, error) {
	if len(stored) == 0 {
		return map[string]any{}, false, nil
	}
	out := make(map[string]any, len(stored))
	needsRewrite := false
	for key, value := range stored {
		if !IsSensitiveCredential(key) || value == nil {
			out[key] = value
			continue
		}
		decrypted, rewrite, err := decryptCredentialValue(vault, key, value)
		if err != nil {
			return nil, false, err
		}
		out[key] = decrypted
		needsRewrite = needsRewrite || rewrite
	}
	return out, needsRewrite, nil
}

// WireCredentialsJSON serialises credentials for a statement/parameter that is
// compared against the on-disk JSONB document (compare-and-swap guards).
func WireCredentialsJSON(vault SecretVault, credentials map[string]any) ([]byte, error) {
	encrypted, err := EncryptCredentialsForStorage(vault, credentials)
	if err != nil {
		return nil, err
	}
	return json.Marshal(encrypted)
}

// WireCredentialsJSONFromBytes re-encrypts a JSON document that was already
// marshalled from plaintext service credentials (snapshot comparisons).
func WireCredentialsJSONFromBytes(vault SecretVault, raw []byte) ([]byte, error) {
	if !SecretVaultAvailable(vault) {
		// No key: keep the historical bytes so plaintext compatibility is exact.
		return raw, nil
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return raw, nil
	}
	var credentials map[string]any
	if err := json.Unmarshal(trimmed, &credentials); err != nil {
		return nil, fmt.Errorf("decode credentials for storage: %w", err)
	}
	return WireCredentialsJSON(vault, credentials)
}

// DecryptCredentialSubvalue decrypts one raw JSONB credential value that was
// selected directly from the database (for example `credentials ->
// 'custom_usage_secrets'`). It mirrors DecryptCredentialsFromStorage for callers
// that bypass the entity conversion.
func DecryptCredentialSubvalue(vault SecretVault, key string, raw []byte) (any, bool, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, false, nil
	}
	if !IsSensitiveCredential(key) {
		var value any
		if err := json.Unmarshal(trimmed, &value); err != nil {
			return nil, false, fmt.Errorf("decode credential %q: %w", key, err)
		}
		return value, false, nil
	}
	var value any
	if err := json.Unmarshal(trimmed, &value); err != nil {
		return nil, false, fmt.Errorf("decode credential %q: %w", key, err)
	}
	decrypted, rewrite, err := decryptCredentialValue(vault, key, value)
	if err != nil {
		return nil, false, err
	}
	return decrypted, rewrite, nil
}

func encryptCredentialValue(vault SecretVault, key string, value any) (any, error) {
	// A value that already carries the ciphertext prefix is left untouched. This
	// keeps the write path idempotent for callers that merge a stored document
	// into their update payload.
	if encoded, ok := value.(string); ok && looksLikeUnexpectedCiphertext(encoded) {
		return encoded, nil
	}
	if !SecretVaultAvailable(vault) {
		return value, nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode credential %q for storage: %w", key, err)
	}
	encrypted, err := vault.EncryptStringDeterministic(string(encoded), credentialCipherContext(key))
	if err != nil {
		return nil, fmt.Errorf("encrypt credential %q: %w", key, err)
	}
	return encrypted, nil
}

func decryptCredentialValue(vault SecretVault, key string, value any) (any, bool, error) {
	encoded, ok := value.(string)
	if !ok {
		// Historical rows may store a non-string value (object/array) directly.
		// It is plaintext from before this migration; rewrite it on next write.
		return value, true, nil
	}
	if encoded == "" {
		return "", false, nil
	}
	if !looksLikeUnexpectedCiphertext(encoded) {
		// Pre-encryption plaintext: readable, and lazily rewritten.
		return encoded, true, nil
	}
	plaintext, err := vault.DecryptString(encoded)
	if err != nil {
		return nil, false, fmt.Errorf("credential %q cannot be decrypted: %w", key, err)
	}
	var decoded any
	if err := json.Unmarshal([]byte(plaintext), &decoded); err != nil {
		return nil, false, fmt.Errorf("credential %q cannot be decoded: %w", key, err)
	}
	return decoded, false, nil
}

func copyAnyMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// EncryptCredentialComparisonValue returns the at-rest form of one credential
// value so a raw-SQL predicate can compare it against the stored document
// exactly as EncryptCredentialsForStorage would have written it.
func EncryptCredentialComparisonValue(vault SecretVault, key, value string) (string, error) {
	if !IsSensitiveCredential(key) || value == "" {
		return value, nil
	}
	encrypted, err := encryptCredentialValue(vault, key, value)
	if err != nil {
		return "", err
	}
	encoded, ok := encrypted.(string)
	if !ok {
		return "", fmt.Errorf("credential %q encryption produced a non-string value", key)
	}
	return encoded, nil
}

// CredentialsContainEncryptedValues reports whether any sensitive sub-key is
// currently stored as ciphertext. Migration/rollback tooling uses it to decide
// whether a row needs rewriting; it never decrypts.
func CredentialsContainEncryptedValues(credentials map[string]any) bool {
	for key, value := range credentials {
		if !IsSensitiveCredential(key) {
			continue
		}
		if encoded, ok := value.(string); ok && looksLikeUnexpectedCiphertext(encoded) {
			return true
		}
	}
	return false
}
