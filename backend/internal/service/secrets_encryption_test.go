package service

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func testVault(t *testing.T) SecretVault {
	t.Helper()
	raw := make([]byte, 32)
	_, err := rand.Read(raw)
	require.NoError(t, err)
	return NewSecretVault(&config.Config{Totp: config.TotpConfig{EncryptionKey: hex.EncodeToString(raw)}})
}

func TestSecretVaultUnavailableWithoutKey(t *testing.T) {
	vault := NewSecretVault(nil)
	require.False(t, vault.Available())
	require.False(t, SecretVaultAvailable(nil))
	_, err := vault.EncryptString("secret")
	require.Error(t, err)
	_, err = vault.DecryptString("enc:v1:whatever")
	require.Error(t, err)
}

func TestEncryptCredentialsForStorageRoundTrip(t *testing.T) {
	vault := testVault(t)
	credentials := map[string]any{
		"api_key":  "sk-upstream-secret",
		"base_url": "https://upstream.example.com",
		"model_mapping": map[string]any{
			"gpt-4o": "gpt-4o",
		},
		CustomUsageCredentialsKey: map[string]any{"api_key": "private-usage-key"},
	}

	stored, err := EncryptCredentialsForStorage(vault, credentials)
	require.NoError(t, err)
	require.NotEqual(t, "sk-upstream-secret", stored["api_key"])
	storedAPIKey, ok := stored["api_key"].(string)
	require.True(t, ok)
	require.Contains(t, storedAPIKey, "enc:v1:")
	require.NotContains(t, storedAPIKey, "sk-upstream-secret")
	require.Equal(t, "https://upstream.example.com", stored["base_url"])
	// The update path must not mutate the caller's map.
	require.Equal(t, "sk-upstream-secret", credentials["api_key"])

	// Deterministic wire form: the same plaintext encrypts to the same document,
	// which is what keeps the JSONB compare-and-swap guards working.
	again, err := EncryptCredentialsForStorage(vault, credentials)
	require.NoError(t, err)
	require.Equal(t, stored["api_key"], again["api_key"])
	require.Equal(t, stored[CustomUsageCredentialsKey], again[CustomUsageCredentialsKey])

	decrypted, needsRewrite, err := DecryptCredentialsFromStorage(vault, stored)
	require.NoError(t, err)
	require.False(t, needsRewrite)
	require.Equal(t, "sk-upstream-secret", decrypted["api_key"])
	require.Equal(t, map[string]any{"api_key": "private-usage-key"}, decrypted[CustomUsageCredentialsKey])
	require.Equal(t, "https://upstream.example.com", decrypted["base_url"])
}

func TestDecryptCredentialsLegacyPlaintextNeedsRewrite(t *testing.T) {
	vault := testVault(t)
	stored := map[string]any{
		"api_key":                 "sk-legacy-plaintext",
		"refresh_token":           "refresh-legacy",
		"base_url":                "https://example.com",
		CustomUsageCredentialsKey: map[string]any{"api_key": "nested-legacy"},
	}

	decrypted, needsRewrite, err := DecryptCredentialsFromStorage(vault, stored)
	require.NoError(t, err)
	require.True(t, needsRewrite)
	require.Equal(t, "sk-legacy-plaintext", decrypted["api_key"])
	require.Equal(t, map[string]any{"api_key": "nested-legacy"}, decrypted[CustomUsageCredentialsKey])

	// A second lazy rewrite must be a no-op (idempotent).
	encrypted, err := EncryptCredentialsForStorage(vault, decrypted)
	require.NoError(t, err)
	_, needsRewrite, err = DecryptCredentialsFromStorage(vault, encrypted)
	require.NoError(t, err)
	require.False(t, needsRewrite)
}

func TestDecryptCredentialsFailsClosedOnWrongKey(t *testing.T) {
	writer := testVault(t)
	stored, err := EncryptCredentialsForStorage(writer, map[string]any{"api_key": "sk-secret-value"})
	require.NoError(t, err)

	reader := testVault(t) // different random key
	_, _, err = DecryptCredentialsFromStorage(reader, stored)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "sk-secret-value")

	// No key at all must fail closed too, never fall back to empty credentials.
	_, _, err = DecryptCredentialsFromStorage(NewSecretVault(nil), stored)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "sk-secret-value")
}

func TestDecryptCredentialsYieldsNilOnNoKeyWithPlaintext(t *testing.T) {
	// Legacy plaintext without an installation key stays readable (development /
	// review mode); it must be rewritten once a key is available.
	decrypted, needsRewrite, err := DecryptCredentialsFromStorage(NewSecretVault(nil), map[string]any{"api_key": "legacy"})
	require.NoError(t, err)
	require.True(t, needsRewrite)
	require.Equal(t, "legacy", decrypted["api_key"])
}

func TestWireCredentialsJSONFromBytes(t *testing.T) {
	vault := testVault(t)
	raw, err := json.Marshal(map[string]any{"api_key": "plain", "_token_version": 3})
	require.NoError(t, err)

	wire, err := WireCredentialsJSONFromBytes(vault, raw)
	require.NoError(t, err)
	require.NotContains(t, string(wire), `"plain"`)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(wire, &decoded))
	decodedAPIKey, ok := decoded["api_key"].(string)
	require.True(t, ok)
	require.Contains(t, decodedAPIKey, "enc:v1:")
}

func TestDecryptCredentialSubvalue(t *testing.T) {
	vault := testVault(t)
	encrypted, err := EncryptCredentialsForStorage(vault, map[string]any{
		CustomUsageCredentialsKey: map[string]any{"api_key": "nested-secret"},
	})
	require.NoError(t, err)
	raw, err := json.Marshal(encrypted[CustomUsageCredentialsKey])
	require.NoError(t, err)

	value, needsRewrite, err := DecryptCredentialSubvalue(vault, CustomUsageCredentialsKey, raw)
	require.NoError(t, err)
	require.False(t, needsRewrite)
	require.Equal(t, map[string]any{"api_key": "nested-secret"}, value)
}

func TestSettingSecrets(t *testing.T) {
	vault := testVault(t)

	encrypted, err := EncryptSettingValue(vault, SettingKeySMTPPassword, "smtp-secret")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(encrypted, "enc:v1:"))
	require.NotContains(t, encrypted, "smtp-secret")

	decrypted, needsRewrite, err := DecryptSettingValue(vault, SettingKeySMTPPassword, encrypted)
	require.NoError(t, err)
	require.False(t, needsRewrite)
	require.Equal(t, "smtp-secret", decrypted)

	// Legacy plaintext is readable and marked for lazy rewrite.
	decrypted, needsRewrite, err = DecryptSettingValue(vault, SettingKeySMTPPassword, "legacy-plain")
	require.NoError(t, err)
	require.True(t, needsRewrite)
	require.Equal(t, "legacy-plain", decrypted)

	// Non-sensitive keys are never touched.
	value, err := EncryptSettingValue(vault, SettingKeySMTPHost, "smtp.example.com")
	require.NoError(t, err)
	require.Equal(t, "smtp.example.com", value)

	// Wrong key fails closed.
	_, _, err = DecryptSettingValue(testVault(t), SettingKeySMTPPassword, encrypted)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "smtp-secret")
}

func TestProxyPasswordSecrets(t *testing.T) {
	vault := testVault(t)

	encrypted, err := EncryptProxyPassword(vault, "p@ssw0rd")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(encrypted, ProxyPasswordCipherPrefix))

	plaintext, needsRewrite, err := DecryptProxyPassword(vault, encrypted)
	require.NoError(t, err)
	require.False(t, needsRewrite)
	require.Equal(t, "p@ssw0rd", plaintext)

	// Legacy managed ciphertext is readable and rewritten to v2.
	legacyRaw, err := vault.EncryptString("managed")
	require.NoError(t, err)
	legacy := LegacyManagedProxyCipherPrefix + strings.TrimPrefix(legacyRaw, "enc:v1:")
	plaintext, needsRewrite, err = DecryptProxyPassword(vault, legacy)
	require.NoError(t, err)
	require.True(t, needsRewrite)
	require.Equal(t, "managed", plaintext)

	// Pre-encryption plaintext is readable and rewritten.
	plaintext, needsRewrite, err = DecryptProxyPassword(vault, "plain-password")
	require.NoError(t, err)
	require.True(t, needsRewrite)
	require.Equal(t, "plain-password", plaintext)

	// Wrong key fails closed.
	_, _, err = DecryptProxyPassword(testVault(t), encrypted)
	require.ErrorIs(t, err, ErrProxyPasswordUndecryptable)
	require.NotContains(t, err.Error(), "p@ssw0rd")

	// Empty stays empty.
	plaintext, needsRewrite, err = DecryptProxyPassword(vault, "")
	require.NoError(t, err)
	require.False(t, needsRewrite)
	require.Empty(t, plaintext)
}
