package bitget

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/stretchr/testify/require"
)

func TestMinedDelegationMustMatchExactSignedAuthorization(t *testing.T) {
	key, err := crypto.GenerateKey()
	require.NoError(t, err)
	in := Intent{Chain: "bsc", ChainID: 56, Nonce: 3, From: crypto.PubkeyToAddress(key.PublicKey).Hex()}
	delegate := "0x3333333333333333333333333333333333333333"
	raw, err := rlp.EncodeToBytes([]any{big.NewInt(56), common.HexToAddress(delegate), uint64(3)})
	require.NoError(t, err)
	digest := crypto.Keccak256(append([]byte{0x05}, raw...))
	sig, err := crypto.Sign(digest, key)
	require.NoError(t, err)
	encodedSig := append([]byte{}, sig...)
	encodedSig[64] += 27
	msgs, err := json.Marshal([]Message{{Type: "auth", Hash: common.BytesToHash(digest).Hex(), Signature: "0x" + hex.EncodeToString(encodedSig)}, {Type: "call", Hash: "0x" + strings.Repeat("ab", 32)}})
	require.NoError(t, err)
	hash := "0x" + strings.Repeat("cd", 32)
	block := "0x" + strings.Repeat("ef", 32)
	auth := map[string]any{"chainId": "0x38", "address": delegate, "nonce": "0x3", "yParity": fmt.Sprintf("0x%x", sig[64]), "r": "0x" + hex.EncodeToString(sig[:32]), "s": "0x" + hex.EncodeToString(sig[32:64])}
	tx := map[string]any{"hash": hash, "blockHash": block, "chainId": "0x38", "type": "0x4", "authorizationList": []any{auth}}
	for _, name := range []string{"valid", "foreign delegate", "wrong nonce", "wrong chain", "missing auth", "wrong block", "wrong sender", "different order digest"} {
		t.Run(name, func(t *testing.T) {
			raw, err := json.Marshal(tx)
			require.NoError(t, err)
			var copy map[string]any
			require.NoError(t, json.Unmarshal(raw, &copy))
			intent := in
			signed := string(msgs)
			auths, ok := copy["authorizationList"].([]any)
			require.True(t, ok)
			a, ok := auths[0].(map[string]any)
			require.True(t, ok)
			switch name {
			case "foreign delegate":
				a["address"] = in.From
			case "wrong nonce":
				a["nonce"] = "0x4"
			case "wrong chain":
				a["chainId"] = "0x1"
			case "missing auth":
				copy["authorizationList"] = []any{}
			case "wrong block":
				copy["blockHash"] = hash
			case "wrong sender":
				intent.From = delegate
			case "different order digest":
				signed = strings.Replace(signed, common.BytesToHash(digest).Hex(), hash, 1)
			}
			raw, err = json.Marshal(copy)
			require.NoError(t, err)
			got, err := MinedDelegate(intent, signed, hash, block, raw)
			if name == "valid" {
				require.NoError(t, err)
				require.Equal(t, delegate, got)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestSignedProtocolJSONRejectsDuplicateAndAmbiguousKeys(t *testing.T) {
	for _, s := range []string{`{"a":1,"a":2}`, `{"nested":{"a":1,"a":2}}`, `[1,{"a":0,"a":1}]`, `{} {}`, strings.Repeat("[", 140) + strings.Repeat("]", 140)} {
		require.Error(t, rejectDuplicateJSONKeys([]byte(s)))
	}
	require.NoError(t, rejectDuplicateJSONKeys([]byte(`{"a":{},"b":[{"a":1}]}`)))
}
