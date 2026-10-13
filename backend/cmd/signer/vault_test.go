package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	testDepositMnemonic = "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"
	testHotMnemonic     = "legal winner thank year wave sausage worth useful legal winner thank yellow"
)

func TestVaultRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vault.enc")
	if err := sealVault(path, testDepositMnemonic, testHotMnemonic, "correct horse battery staple"); err != nil {
		t.Fatal(err)
	}
	got, err := openVault(path, "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if got.Deposit != testDepositMnemonic || got.Hot != testHotMnemonic {
		t.Fatalf("round trip mismatch: %+v", got)
	}
}

// The whole point of the vault: the file alone must reveal nothing.
func TestVaultFileLeaksNoMnemonic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vault.enc")
	if err := sealVault(path, testDepositMnemonic, testHotMnemonic, "pw"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, word := range []string{"abandon", "legal", "winner"} {
		if strings.Contains(string(raw), word) {
			t.Fatalf("vault file contains plaintext word %q", word)
		}
	}
}

func TestVaultRejectsWrongPassphrase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vault.enc")
	if err := sealVault(path, testDepositMnemonic, testHotMnemonic, "right"); err != nil {
		t.Fatal(err)
	}
	if _, err := openVault(path, "wrong"); err == nil {
		t.Fatal("wrong passphrase must fail")
	} else if !strings.Contains(err.Error(), "wrong passphrase") {
		t.Fatalf("err = %v, want a wrong-passphrase report", err)
	}
}

// Two seals of the same input must differ: a reused salt/nonce would leak
// equality and weaken GCM.
func TestVaultUsesFreshSaltAndNonce(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.enc")
	b := filepath.Join(dir, "b.enc")
	if err := sealVault(a, testDepositMnemonic, testHotMnemonic, "pw"); err != nil {
		t.Fatal(err)
	}
	if err := sealVault(b, testDepositMnemonic, testHotMnemonic, "pw"); err != nil {
		t.Fatal(err)
	}
	ra, _ := os.ReadFile(a)
	rb, _ := os.ReadFile(b)
	if string(ra) == string(rb) {
		t.Fatal("two seals produced identical ciphertext (salt/nonce reuse)")
	}
}

func TestVaultRejectsEmptyPassphrase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vault.enc")
	if err := sealVault(path, testDepositMnemonic, testHotMnemonic, ""); err == nil {
		t.Fatal("empty passphrase must be rejected")
	}
}
