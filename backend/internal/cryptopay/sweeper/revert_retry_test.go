package sweeper

import (
	"testing"
)

func signSweep(t *testing.T, gasLimit uint64) string {
	t.Helper()
	_, _, chain, entry := setupExecutor(t)
	return chain.signed(EVMSignRequest{Index: 42, Kind: "sweep", ChainID: 56, Token: entry.Token,
		To: testDestination, Amount: "10000000", Nonce: 3, GasPrice: "1", GasLimit: gasLimit})
}

func TestShouldRetrySweepRevertOnlyForLegacyLimit(t *testing.T) {
	legacy := signSweep(t, legacyTransferGasLimit)
	if !shouldRetrySweepRevert(false, legacy) {
		t.Fatal("a transfer signed with the legacy limit must be retried once")
	}
	current := signSweep(t, transferGasLimit)
	if shouldRetrySweepRevert(false, current) {
		t.Fatal("a retry that already used the larger limit must go to manual review")
	}
	if shouldRetrySweepRevert(true, legacy) {
		t.Fatal("gas top-ups must never be auto-retried")
	}
	if shouldRetrySweepRevert(false, "") {
		t.Fatal("undecodable bytes must not be retried automatically")
	}
}

func TestGasLimitMatchesLegacyAndCurrent(t *testing.T) {
	if !gasLimitMatches(legacyTransferGasLimit, 0) || !gasLimitMatches(transferGasLimit, 0) {
		t.Fatal("both legacy and current sweep limits must validate")
	}
	if gasLimitMatches(12345, 0) {
		t.Fatal("an arbitrary gas limit must not validate")
	}
	if !gasLimitMatches(21000, 21000) || gasLimitMatches(21001, 21000) {
		t.Fatal("exact matching must stay strict for gas top-ups")
	}
}
