package main

import (
	"testing"
)

// BIP39 test vector (Trezor, "abandon ... about").
const testMnemonic = "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"

func TestDeriverEVMDeterministic(t *testing.T) {
	d, err := NewDeriver(testMnemonic)
	if err != nil {
		t.Fatal(err)
	}
	_, addr0a, err := d.EVMKey(0)
	if err != nil {
		t.Fatal(err)
	}
	_, addr0b, err := d.EVMKey(0)
	if err != nil {
		t.Fatal(err)
	}
	if addr0a != addr0b {
		t.Fatalf("derivation not deterministic: %s vs %s", addr0a, addr0b)
	}
	_, addr1, err := d.EVMKey(1)
	if err != nil {
		t.Fatal(err)
	}
	if addr0a == addr1 {
		t.Fatal("index 0 and 1 produced the same address")
	}
	// Known vector: m/44'/60'/0'/0/0 for this mnemonic is
	// 0x9858EfFD232B4033E47d90003D41EC34EcaEda94.
	if got, want := addr0a.Hex(), "0x9858EfFD232B4033E47d90003D41EC34EcaEda94"; got != want {
		t.Fatalf("EVM index 0 = %s, want %s", got, want)
	}
}

func TestDeriverSolanaDeterministic(t *testing.T) {
	d, err := NewDeriver(testMnemonic)
	if err != nil {
		t.Fatal(err)
	}
	_, addr0a, err := d.SolanaKey(0)
	if err != nil {
		t.Fatal(err)
	}
	_, addr0b, err := d.SolanaKey(0)
	if err != nil {
		t.Fatal(err)
	}
	if addr0a != addr0b {
		t.Fatalf("solana derivation not deterministic: %s vs %s", addr0a, addr0b)
	}
	_, addr1, err := d.SolanaKey(1)
	if err != nil {
		t.Fatal(err)
	}
	if addr0a == addr1 {
		t.Fatal("solana index 0 and 1 produced the same address")
	}
}

func TestNewDeriverRejectsInvalidMnemonic(t *testing.T) {
	if _, err := NewDeriver("not a mnemonic"); err == nil {
		t.Fatal("expected invalid mnemonic error")
	}
}

func TestNewMnemonicValid(t *testing.T) {
	mnemonic, err := NewMnemonic()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewDeriver(mnemonic); err != nil {
		t.Fatalf("generated mnemonic invalid: %v", err)
	}
}
