package wallet

import (
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

func nativeSweepRequest() EVMSignRequest {
	req := sweepRequest()
	// A native sweep is a plain value transfer: intrinsic gas, no calldata.
	req.Token = "native"
	req.GasLimit = 21_000
	return req
}

// A native coin sweep must serialize exactly like a plain payment: value moved
// to the configured destination, no calldata, no token contract. Anything else
// would move ETH/BNB somewhere the operator did not configure.
func TestNativeSweepSignsPlainValueTransfer(t *testing.T) {
	m, mock, raw := residentManager(t)
	req := nativeSweepRequest()
	signExpectation(mock, raw, req)
	signed, err := m.SignEVM(t.Context(), req)
	if err != nil {
		t.Fatalf("native sweep was refused: %v", err)
	}
	tx := decodeTransaction(t, signed)
	destination := common.HexToAddress(req.To)
	if tx.To() == nil || *tx.To() != destination {
		t.Fatalf("native sweep recipient = %v, want %s", tx.To(), destination.Hex())
	}
	if tx.Value().Cmp(big.NewInt(1_000_000)) != 0 {
		t.Fatalf("native sweep value = %s, want 1000000", tx.Value())
	}
	if len(tx.Data()) != 0 {
		t.Fatalf("native sweep carried calldata: %x", tx.Data())
	}
	if tx.Gas() != 21_000 {
		t.Fatalf("native sweep gas = %d, want 21000", tx.Gas())
	}
	// The signer must still be the deposit key at the requested index, not the
	// hot wallet that funds gas.
	sender, err := types.Sender(types.LatestSignerForChainID(big.NewInt(req.ChainID)), tx)
	if err != nil || sender.Hex() != "0x9858EfFD232B4033E47d90003D41EC34EcaEda94" {
		t.Fatalf("native sweep was not signed by the deposit key: %v %v", sender.Hex(), err)
	}
}

func TestNativeSweepKeepsDestinationAndGasPolicy(t *testing.T) {
	cases := []struct {
		name string
		// mutate changes the request; afterVault reports whether the refusal
		// happens after the vault read (and therefore needs the transaction
		// expectations) or during request validation (which happens first).
		mutate     func(*EVMSignRequest)
		afterVault bool
		want       string
	}{
		{"wrong destination", func(r *EVMSignRequest) {
			r.To = "0x3333333333333333333333333333333333333333"
		}, true, "destination"},
		{"contract-call gas limit", func(r *EVMSignRequest) { r.GasLimit = 65_000 }, false, "gas limit"},
		{"zero amount", func(r *EVMSignRequest) { r.Amount = "0" }, false, "amount"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, mock, raw := residentManager(t)
			req := nativeSweepRequest()
			tc.mutate(&req)
			if tc.afterVault {
				mock.ExpectBegin()
				expectRead(mock, raw, " FOR SHARE")
				mock.ExpectRollback()
			}
			auditOK(mock, "sign_evm", "denied")
			signed, err := m.SignEVM(t.Context(), req)
			if signed != "" || err == nil {
				t.Fatal("an invalid native sweep was signed")
			}
			if !strings.Contains(strings.ToLower(err.Error()), tc.want) {
				t.Fatalf("refusal %q does not mention %q", err, tc.want)
			}
		})
	}
}

// A gas top-up must still be refused when it claims to be a deposit sweep:
// only the intrinsic-limit native payment to our own child is a top-up.
func TestNativeGasTopUpStillNeedsTheIntrinsicLimit(t *testing.T) {
	m, _, _ := residentManager(t)
	req := nativeSweepRequest()
	req.Kind = "gas"
	req.GasLimit = 65_000
	if _, err := m.SignEVM(t.Context(), req); err == nil {
		t.Fatal("a gas top-up with a contract-call gas limit was signed")
	}
}
