package secretcrypt

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func testKey(t *testing.T) []byte {
	t.Helper()
	raw := make([]byte, KeySize)
	_, err := rand.Read(raw)
	require.NoError(t, err)
	return raw
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := testKey(t)
	plaintext := `{"api_key":"sk-live-secret","base_url":"https://upstream.example.com"}`

	encrypted, err := EncryptString(plaintext, key)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(encrypted, Prefix))
	require.True(t, IsEncrypted(encrypted))
	require.NotContains(t, encrypted, "sk-live-secret")

	decrypted, err := DecryptString(encrypted, key)
	require.NoError(t, err)
	require.Equal(t, plaintext, decrypted)
}

func TestEncryptStringUsesRandomNonce(t *testing.T) {
	key := testKey(t)
	first, err := EncryptString("same-secret", key)
	require.NoError(t, err)
	second, err := EncryptString("same-secret", key)
	require.NoError(t, err)
	require.NotEqual(t, first, second, "random nonce must make equal plaintext differ at rest")
}

func TestEncryptStringDeterministicIsStable(t *testing.T) {
	key := testKey(t)
	first, err := EncryptStringDeterministic("same-secret", "api_key", key)
	require.NoError(t, err)
	second, err := EncryptStringDeterministic("same-secret", "api_key", key)
	require.NoError(t, err)
	require.Equal(t, first, second, "deterministic encryption must keep JSONB equality working")

	other, err := EncryptStringDeterministic("same-secret", "refresh_token", key)
	require.NoError(t, err)
	require.NotEqual(t, first, other, "context must separate domains")

	decrypted, err := DecryptString(first, key)
	require.NoError(t, err)
	require.Equal(t, "same-secret", decrypted)
}

func TestDecryptWrongKeyFailsClosed(t *testing.T) {
	encrypted, err := EncryptString("secret", testKey(t))
	require.NoError(t, err)

	_, err = DecryptString(encrypted, testKey(t))
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrAuthentication))
}

func TestDecryptTamperedCiphertextFailsClosed(t *testing.T) {
	key := testKey(t)
	encrypted, err := EncryptString("secret", key)
	require.NoError(t, err)

	payload := strings.TrimPrefix(encrypted, Prefix)
	replacement := byte('A')
	if payload[len(payload)-2] == replacement {
		replacement = 'B'
	}
	tampered := Prefix + payload[:len(payload)-2] + string(replacement) + payload[len(payload)-1:]

	_, err = DecryptString(tampered, key)
	require.Error(t, err)
}

func TestErrorsNeverContainPlaintext(t *testing.T) {
	key := testKey(t)
	const plaintext = "super-secret-value"
	encrypted, err := EncryptString(plaintext, key)
	require.NoError(t, err)

	_, err = DecryptString(encrypted, testKey(t))
	require.Error(t, err)
	require.NotContains(t, err.Error(), plaintext)
	require.NotContains(t, err.Error(), encrypted)

	_, err = DecryptString("not-ciphertext", key)
	require.ErrorIs(t, err, ErrMalformed)
	require.NotContains(t, err.Error(), "not-ciphertext")
}

func TestMissingKeyFailsClosed(t *testing.T) {
	key := testKey(t)
	encrypted, err := EncryptString("secret", key)
	require.NoError(t, err)

	_, err = DecryptString(encrypted, nil)
	require.ErrorIs(t, err, ErrKeyUnavailable)
	_, err = EncryptString("secret", nil)
	require.ErrorIs(t, err, ErrKeyUnavailable)
}

func TestParseKey(t *testing.T) {
	key := testKey(t)
	parsed, err := ParseKey(hex.EncodeToString(key))
	require.NoError(t, err)
	require.Equal(t, key, parsed)

	_, err = ParseKey("")
	require.ErrorIs(t, err, ErrKeyUnavailable)
	_, err = ParseKey("zz")
	require.ErrorIs(t, err, ErrKeyUnavailable)
	_, err = ParseKey(hex.EncodeToString(make([]byte, 16)))
	require.ErrorIs(t, err, ErrKeyUnavailable)
}

func TestDecryptLegacyBase64(t *testing.T) {
	key := testKey(t)
	plaintext := "managed-password"

	// Produce the legacy format: base64(nonce || sealed) without a prefix.
	legacy, err := EncryptString(plaintext, key)
	require.NoError(t, err)
	legacyRaw := strings.TrimPrefix(legacy, Prefix)

	decrypted, err := DecryptLegacyBase64(legacyRaw, key)
	require.NoError(t, err)
	require.Equal(t, plaintext, decrypted)

	_, err = DecryptLegacyBase64(legacyRaw, testKey(t))
	require.ErrorIs(t, err, ErrAuthentication)
}
