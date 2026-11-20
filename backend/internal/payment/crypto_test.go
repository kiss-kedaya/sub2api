package payment

import (
	"crypto/rand"
	"errors"
	"strings"
	"testing"
)

func makeKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("generate random key: %v", err)
	}
	return key
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	t.Parallel()
	key := makeKey(t)

	plaintexts := []string{
		"hello world",
		"short",
		"a longer string with special chars: !@#$%^&*()",
		`{"key":"value","num":42}`,
		"你好世界 unicode test 🎉",
		strings.Repeat("x", 10000),
	}

	for _, pt := range plaintexts {
		encrypted, err := Encrypt(pt, key)
		if err != nil {
			t.Fatalf("Encrypt(%q) error: %v", pt[:min(len(pt), 30)], err)
		}
		decrypted, err := Decrypt(encrypted, key)
		if err != nil {
			t.Fatalf("Decrypt error for plaintext %q: %v", pt[:min(len(pt), 30)], err)
		}
		if decrypted != pt {
			t.Fatalf("round-trip failed: got %q, want %q", decrypted[:min(len(decrypted), 30)], pt[:min(len(pt), 30)])
		}
	}
}

func TestEncryptProducesDifferentCiphertexts(t *testing.T) {
	t.Parallel()
	key := makeKey(t)

	ct1, err := Encrypt("same plaintext", key)
	if err != nil {
		t.Fatalf("first Encrypt error: %v", err)
	}
	ct2, err := Encrypt("same plaintext", key)
	if err != nil {
		t.Fatalf("second Encrypt error: %v", err)
	}
	if ct1 == ct2 {
		t.Fatal("two encryptions of the same plaintext should produce different ciphertexts (random nonce)")
	}
}

func TestDecryptWithWrongKeyFails(t *testing.T) {
	t.Parallel()
	key1 := makeKey(t)
	key2 := makeKey(t)

	encrypted, err := Encrypt("secret data", key1)
	if err != nil {
		t.Fatalf("Encrypt error: %v", err)
	}

	_, err = Decrypt(encrypted, key2)
	if err == nil {
		t.Fatal("Decrypt with wrong key should fail, but got nil error")
	}
}

func TestEncryptRejectsInvalidKeyLength(t *testing.T) {
	t.Parallel()
	badKeys := [][]byte{
		nil,
		make([]byte, 0),
		make([]byte, 16),
		make([]byte, 31),
		make([]byte, 33),
		make([]byte, 64),
	}
	for _, key := range badKeys {
		_, err := Encrypt("test", key)
		if err == nil {
			t.Fatalf("Encrypt should reject key of length %d", len(key))
		}
	}
}

func TestDecryptRejectsInvalidKeyLength(t *testing.T) {
	t.Parallel()
	badKeys := [][]byte{
		nil,
		make([]byte, 16),
		make([]byte, 33),
	}
	for _, key := range badKeys {
		_, err := Decrypt("dummydata:dummydata:dummydata", key)
		if err == nil {
			t.Fatalf("Decrypt should reject key of length %d", len(key))
		}
	}
}

func TestEncryptEmptyPlaintext(t *testing.T) {
	t.Parallel()
	key := makeKey(t)

	encrypted, err := Encrypt("", key)
	if err != nil {
		t.Fatalf("Encrypt empty plaintext error: %v", err)
	}
	decrypted, err := Decrypt(encrypted, key)
	if err != nil {
		t.Fatalf("Decrypt empty plaintext error: %v", err)
	}
	if decrypted != "" {
		t.Fatalf("expected empty string, got %q", decrypted)
	}
}

func TestEncryptDecryptUnicodeJSON(t *testing.T) {
	t.Parallel()
	key := makeKey(t)

	jsonContent := `{"name":"测试用户","email":"test@example.com","balance":100.50}`
	encrypted, err := Encrypt(jsonContent, key)
	if err != nil {
		t.Fatalf("Encrypt JSON error: %v", err)
	}
	decrypted, err := Decrypt(encrypted, key)
	if err != nil {
		t.Fatalf("Decrypt JSON error: %v", err)
	}
	if decrypted != jsonContent {
		t.Fatalf("JSON round-trip failed: got %q, want %q", decrypted, jsonContent)
	}
}

func TestDecryptInvalidFormat(t *testing.T) {
	t.Parallel()
	key := makeKey(t)

	invalidInputs := []string{
		"",
		"nodelimiter",
		"only:two",
		"invalid:base64:!!!",
	}
	for _, input := range invalidInputs {
		_, err := Decrypt(input, key)
		if err == nil {
			t.Fatalf("Decrypt(%q) should fail but got nil error", input)
		}
	}
}

func TestCiphertextFormat(t *testing.T) {
	t.Parallel()
	key := makeKey(t)

	encrypted, err := Encrypt("test", key)
	if err != nil {
		t.Fatalf("Encrypt error: %v", err)
	}

	parts := strings.SplitN(encrypted, ":", 3)
	if len(parts) != 3 {
		t.Fatalf("ciphertext should have format iv:authTag:ciphertext, got %d parts", len(parts))
	}
	for i, part := range parts {
		if part == "" {
			t.Fatalf("ciphertext part %d is empty", i)
		}
	}
}

func TestEncryptProviderConfigRoundTrip(t *testing.T) {
	t.Parallel()
	key := makeKey(t)
	plaintext := `{"pkey":"merchant-secret","appId":"app-1"}`

	encrypted, err := EncryptProviderConfig(plaintext, key)
	if err != nil {
		t.Fatalf("EncryptProviderConfig error: %v", err)
	}
	if !strings.HasPrefix(encrypted, ProviderConfigCipherPrefix) {
		t.Fatalf("ciphertext %q must carry the %q version prefix", encrypted, ProviderConfigCipherPrefix)
	}
	if strings.Contains(encrypted, "merchant-secret") {
		t.Fatal("ciphertext must not contain the plaintext secret")
	}
	if !IsEncryptedProviderConfig(encrypted) {
		t.Fatal("IsEncryptedProviderConfig must report current ciphertext as encrypted")
	}

	decrypted, err := DecryptProviderConfig(encrypted, key)
	if err != nil {
		t.Fatalf("DecryptProviderConfig error: %v", err)
	}
	if decrypted != plaintext {
		t.Fatalf("round trip = %q, want %q", decrypted, plaintext)
	}
}

func TestDecryptProviderConfigLegacyAndPlaintextCompat(t *testing.T) {
	t.Parallel()
	key := makeKey(t)
	plaintext := `{"pkey":"merchant-secret"}`

	legacy, err := Encrypt(plaintext, key)
	if err != nil {
		t.Fatalf("seed legacy ciphertext: %v", err)
	}
	if !IsEncryptedProviderConfig(legacy) {
		t.Fatal("IsEncryptedProviderConfig must recognise legacy ciphertext")
	}
	decrypted, err := DecryptProviderConfig(legacy, key)
	if err != nil {
		t.Fatalf("DecryptProviderConfig legacy error: %v", err)
	}
	if decrypted != plaintext {
		t.Fatalf("legacy decrypt = %q, want %q", decrypted, plaintext)
	}

	// Pre-encryption plaintext rows stay readable so an upgrade never loses a
	// merchant configuration.
	if IsEncryptedProviderConfig(plaintext) {
		t.Fatal("plaintext JSON must not be reported as encrypted")
	}
	decrypted, err = DecryptProviderConfig(plaintext, nil)
	if err != nil {
		t.Fatalf("DecryptProviderConfig plaintext error: %v", err)
	}
	if decrypted != plaintext {
		t.Fatalf("plaintext passthrough = %q, want %q", decrypted, plaintext)
	}
}

func TestDecryptProviderConfigFailsClosed(t *testing.T) {
	t.Parallel()
	key := makeKey(t)
	wrongKey := makeKey(t)
	encrypted, err := EncryptProviderConfig(`{"pkey":"merchant-secret"}`, key)
	if err != nil {
		t.Fatalf("EncryptProviderConfig error: %v", err)
	}

	if _, err := DecryptProviderConfig(encrypted, wrongKey); err == nil {
		t.Fatal("wrong key must fail closed")
	} else if strings.Contains(err.Error(), "merchant-secret") {
		t.Fatal("error must not echo secret material")
	}
	if _, err := DecryptProviderConfig(encrypted, nil); !errors.Is(err, ErrProviderConfigKeyUnavailable) {
		t.Fatalf("missing key must return ErrProviderConfigKeyUnavailable, got %v", err)
	}
	if _, err := DecryptProviderConfig(encrypted, []byte("short-key")); !errors.Is(err, ErrProviderConfigKeyUnavailable) {
		t.Fatalf("short key must return ErrProviderConfigKeyUnavailable, got %v", err)
	}
}
