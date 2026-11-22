package atrest

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

const testKeyHex = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"

func mustCipher(t *testing.T, keyMaterial string) *Cipher {
	t.Helper()
	c, err := New(keyMaterial)
	if err != nil {
		t.Fatalf("New(%q): %v", keyMaterial, err)
	}
	return c
}

func TestParseKey_Formats(t *testing.T) {
	key, err := ParseKey(testKeyHex)
	if err != nil {
		t.Fatalf("hex key: %v", err)
	}
	if len(key) != 32 {
		t.Fatalf("key len = %d, want 32", len(key))
	}
	raw, _ := hex.DecodeString(testKeyHex)

	cases := map[string]string{
		"std base64":     base64.StdEncoding.EncodeToString(raw),
		"raw std base64": base64.RawStdEncoding.EncodeToString(raw),
		"url base64":     base64.URLEncoding.EncodeToString(raw),
		"0x hex":         "0x" + testKeyHex,
		"upper hex":      strings.ToUpper(testKeyHex),
	}
	for name, material := range cases {
		got, err := ParseKey(material)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if hex.EncodeToString(got) != hex.EncodeToString(raw) {
			t.Fatalf("%s: key mismatch", name)
		}
	}
}

func TestParseKey_RejectsBadInput(t *testing.T) {
	bad := []string{
		"short",
		strings.Repeat("zz", 32),
		base64.StdEncoding.EncodeToString([]byte("not-32-bytes")),
		strings.Repeat("ab", 31),
	}
	for _, material := range bad {
		if _, err := ParseKey(material); err == nil {
			t.Fatalf("ParseKey(%q) unexpectedly succeeded", material)
		}
	}
}

func TestNew_EmptyKeyIsDisabled(t *testing.T) {
	c, err := New("   ")
	if err != nil {
		t.Fatalf("empty key should not error: %v", err)
	}
	if c != nil {
		t.Fatalf("empty key should disable encryption")
	}
}

func TestRoundTrip(t *testing.T) {
	c := mustCipher(t, testKeyHex)
	plaintexts := []string{
		"",
		"sk-test-123",
		`{"api_key":"sk-abc","refresh_token":"rt"}`,
		"中文与 emoji 🔐",
		strings.Repeat("x", 4096),
	}
	for _, plaintext := range plaintexts {
		ciphertext, err := c.EncryptString(plaintext)
		if err != nil {
			t.Fatalf("encrypt: %v", err)
		}
		if !strings.HasPrefix(ciphertext, Prefix) {
			t.Fatalf("ciphertext missing prefix: %q", ciphertext)
		}
		if plaintext != "" && strings.Contains(ciphertext, plaintext) {
			t.Fatalf("ciphertext leaks plaintext")
		}
		got, err := c.DecryptString(ciphertext)
		if err != nil {
			t.Fatalf("decrypt: %v", err)
		}
		if got != plaintext {
			t.Fatalf("round trip mismatch: got %q want %q", got, plaintext)
		}
	}
}

func TestEncrypt_UsesRandomNonce(t *testing.T) {
	c := mustCipher(t, testKeyHex)
	first, err := c.EncryptString("same-plaintext")
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.EncryptString("same-plaintext")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("random nonce must produce different ciphertext")
	}
}

func TestDecrypt_LegacyPlaintextPassthrough(t *testing.T) {
	c := mustCipher(t, testKeyHex)
	for _, plaintext := range []string{"sk-legacy", "", "enc:managed:v1:abc"} {
		got, err := c.DecryptString(plaintext)
		if err != nil {
			t.Fatalf("legacy value %q should pass through: %v", plaintext, err)
		}
		if got != plaintext {
			t.Fatalf("legacy passthrough mismatch")
		}
	}
}

func TestDecrypt_WrongKeyFailsClosed(t *testing.T) {
	encryptor := mustCipher(t, testKeyHex)
	ciphertext, err := encryptor.EncryptString("sk-secret")
	if err != nil {
		t.Fatal(err)
	}
	other := mustCipher(t, strings.Repeat("11", 32))
	got, err := other.DecryptString(ciphertext)
	if err == nil {
		t.Fatalf("wrong key must fail")
	}
	if got != "" {
		t.Fatalf("wrong key must not return ciphertext, got %q", got)
	}
	if strings.Contains(err.Error(), "sk-secret") || strings.Contains(err.Error(), ciphertext) {
		t.Fatalf("error leaks plaintext or ciphertext: %v", err)
	}
}

func TestDecrypt_TamperedCiphertextFailsClosed(t *testing.T) {
	c := mustCipher(t, testKeyHex)
	ciphertext, err := c.EncryptString("sk-secret")
	if err != nil {
		t.Fatal(err)
	}
	payload, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(ciphertext, Prefix))
	if err != nil {
		t.Fatal(err)
	}
	payload[len(payload)-1] ^= 0xff
	tampered := Prefix + base64.StdEncoding.EncodeToString(payload)
	if _, err := c.DecryptString(tampered); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("tampered ciphertext should fail with ErrDecrypt, got %v", err)
	}
}

func TestDisabledCipher_RefusesEncryptedWrite(t *testing.T) {
	var c *Cipher
	if _, err := c.EncryptString("sk-secret"); !errors.Is(err, ErrNoKey) {
		t.Fatalf("disabled cipher must refuse encryption, got %v", err)
	}
	if _, err := c.DecryptString("enc:v2:AAAA"); !errors.Is(err, ErrNoKey) {
		t.Fatalf("disabled cipher must fail closed on ciphertext, got %v", err)
	}
	if got, err := c.DecryptString("plaintext"); err != nil || got != "plaintext" {
		t.Fatalf("disabled cipher must read plaintext: got %q err %v", got, err)
	}
}

func TestConfigureFromEnv(t *testing.T) {
	t.Cleanup(func() { Reset() })

	t.Setenv(KeyEnv, "")
	Reset()
	c, err := ConfigureFromEnv()
	if err != nil {
		t.Fatalf("unset key should not error: %v", err)
	}
	if c != nil || Enabled() {
		t.Fatalf("unset key should stay disabled")
	}

	t.Setenv(KeyEnv, testKeyHex)
	c, err = ConfigureFromEnv()
	if err != nil {
		t.Fatalf("valid key: %v", err)
	}
	if c == nil || !Enabled() {
		t.Fatalf("valid key should enable encryption")
	}
	if len(KeyFingerprint()) != 16 {
		t.Fatalf("fingerprint = %q", KeyFingerprint())
	}

	t.Setenv(KeyEnv, "not-a-key")
	if _, err := ConfigureFromEnv(); err == nil {
		t.Fatalf("invalid key must fail")
	}
}

func TestIdentityDigest_DeterministicAndDomainSeparated(t *testing.T) {
	a := IdentityDigest("api_key", "sk-abc")
	b := IdentityDigest("api_key", "sk-abc")
	c := IdentityDigest("access_token", "sk-abc")
	if a != b {
		t.Fatalf("digest must be deterministic")
	}
	if a == c {
		t.Fatalf("digest must be domain separated")
	}
	if strings.Contains(a, "sk-abc") {
		t.Fatalf("digest leaks plaintext")
	}
	if len(a) != 64 {
		t.Fatalf("digest length = %d", len(a))
	}
}
