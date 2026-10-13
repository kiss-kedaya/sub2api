package main

import "testing"

// The hot wallet must live on its own seed. If it shared the deposit seed, a
// single leaked mnemonic would drain both the user deposits and every swept
// fund — the whole point of the split is that these two are independent.
func TestHotSeedIsIndependentFromDepositSeed(t *testing.T) {
	depositMnemonic := "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"
	hotMnemonic := "legal winner thank year wave sausage worth useful legal winner thank yellow"

	deposit, err := NewDeriver(depositMnemonic)
	if err != nil {
		t.Fatal(err)
	}
	hot, err := NewDeriver(hotMnemonic)
	if err != nil {
		t.Fatal(err)
	}

	_, depositAddr, err := deposit.EVMKey(0)
	if err != nil {
		t.Fatal(err)
	}
	_, hotAddr, err := hot.EVMKey(0)
	if err != nil {
		t.Fatal(err)
	}
	if depositAddr == hotAddr {
		t.Fatal("hot wallet and deposit address must not collide")
	}

	// The gas authorisation derives the target from the DEPOSIT seed (the
	// recipient is a user deposit address), while signing uses the HOT seed.
	gasReq := evmSignRequest{Kind: "gas", To: depositAddr.Hex()}
	idx := uint32(0)
	gasReq.GasIndex = &idx
	if !gasTargetAllowed(deposit, gasReq) {
		t.Fatal("gas target must authorise against the deposit seed")
	}
	if gasTargetAllowed(hot, gasReq) {
		t.Fatal("the hot seed must not authorise a deposit-derived target")
	}
}
