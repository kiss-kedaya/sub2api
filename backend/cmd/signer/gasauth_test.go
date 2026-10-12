package main

import (
	"testing"
)

// The gas path lets a native transfer to a NON-whitelisted address through,
// so the derivation check is the only thing standing between a bug and
// draining the hot wallet. These tests pin that behaviour.
func TestGasTopUpAuthorisation(t *testing.T) {
	mnemonic := "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"
	d, err := NewDeriver(mnemonic)
	if err != nil {
		t.Fatal(err)
	}
	_, addr, err := d.EVMKey(7)
	if err != nil {
		t.Fatal(err)
	}
	other, err := NewDeriver("legal winner thank year wave sausage worth useful legal winner thank yellow")
	if err != nil {
		t.Fatal(err)
	}
	_, foreign, err := other.EVMKey(7)
	if err != nil {
		t.Fatal(err)
	}

	// A gas request for index 7 targeting our own derived address: allowed.
	idx := uint32(7)
	req := evmSignRequest{Kind: "gas", GasIndex: &idx, To: addr.Hex(), Token: "native"}
	if !gasTargetAllowed(d, req) {
		t.Fatal("gas top-up to our own derived address must be allowed")
	}

	// Same request to a foreign address: must be rejected.
	req.To = foreign.Hex()
	if gasTargetAllowed(d, req) {
		t.Fatal("gas top-up to a foreign address must be rejected")
	}

	// A non-gas request without gas_index is not covered by this path.
	if gasTargetAllowed(d, evmSignRequest{Kind: "sweep", To: addr.Hex()}) {
		t.Fatal("non-gas requests must not use the gas authorisation path")
	}
}
