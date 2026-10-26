package bitget

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rlp"
)

// MinedDelegate proves that the authorization installed by this transaction is
// the exact auth digest and signature signed for this order. A receipt followed
// by eth_getCode(latest) alone cannot establish this binding.
func MinedDelegate(in Intent, signed, hash, blockHash string, raw json.RawMessage) (string, error) {
	var msgs []Message
	if len(signed) > 128<<10 || json.Unmarshal([]byte(signed), &msgs) != nil || len(msgs) < 1 || len(msgs) > 2 {
		return "", ErrUnsafeOrder
	}
	var auth *Message
	for i := range msgs {
		if msgs[i].Type == "auth" {
			if auth != nil {
				return "", ErrUnsafeOrder
			}
			auth = &msgs[i]
		}
	}
	var tx struct {
		Hash           string          `json:"hash"`
		BlockHash      string          `json:"blockHash"`
		ChainID        json.RawMessage `json:"chainId"`
		Type           string          `json:"type"`
		Authorizations []struct {
			ChainID json.RawMessage `json:"chainId"`
			Address string          `json:"address"`
			Nonce   json.RawMessage `json:"nonce"`
			Parity  json.RawMessage `json:"yParity"`
			R       json.RawMessage `json:"r"`
			S       json.RawMessage `json:"s"`
			V       json.RawMessage `json:"v"`
		} `json:"authorizationList"`
	}
	if json.Unmarshal(raw, &tx) != nil || !Hash(hash) || !Hash(blockHash) || !strings.EqualFold(tx.Hash, hash) || !strings.EqualFold(tx.BlockHash, blockHash) {
		return "", ErrUnsafeOrder
	}
	chain, e := quantity(tx.ChainID)
	if e != nil || chain.Cmp(big.NewInt(in.ChainID)) != 0 || (auth != nil && tx.Type != "0x4") {
		return "", ErrUnsafeOrder
	}
	delegate := in.CurrentDelegate
	matched := false
	for _, a := range tx.Authorizations {
		id, e := quantity(a.ChainID)
		n, e2 := quantity(a.Nonce)
		parity, e3 := quantity(a.Parity)
		if len(a.Parity) == 0 {
			parity, e3 = quantity(a.V)
			if e3 == nil && parity.Cmp(big.NewInt(27)) >= 0 {
				parity.Sub(parity, big.NewInt(27))
			}
		}
		r, e4 := quantity(a.R)
		s, e5 := quantity(a.S)
		if e != nil || e2 != nil || e3 != nil || e4 != nil || e5 != nil || !id.IsInt64() || !n.IsUint64() || !parity.IsUint64() || parity.Uint64() > 1 || !ValidAddress(a.Address) {
			return "", ErrUnsafeOrder
		}
		encoded, e := rlp.EncodeToBytes([]any{id, common.HexToAddress(a.Address), n.Uint64()})
		if e != nil {
			return "", ErrUnsafeOrder
		}
		digest := crypto.Keccak256(append([]byte{0x05}, encoded...))
		sig := make([]byte, 65)
		r.FillBytes(sig[:32])
		s.FillBytes(sig[32:64])
		sig[64] = byte(parity.Uint64())
		pub, e := crypto.SigToPub(digest, sig)
		if e != nil {
			return "", ErrUnsafeOrder
		}
		if !strings.EqualFold(crypto.PubkeyToAddress(*pub).Hex(), in.From) {
			continue
		}
		if auth == nil || matched || !id.IsInt64() || id.Int64() != in.ChainID || n.Uint64() != in.Nonce || !Hash(auth.Hash) || !bytes.Equal(digest, common.FromHex(auth.Hash)) {
			return "", ErrUnsafeOrder
		}
		want, e := hex.DecodeString(strings.TrimPrefix(auth.Signature, "0x"))
		if e != nil || len(want) != 65 || want[64] < 27 || want[64] > 28 {
			return "", ErrUnsafeOrder
		}
		want[64] -= 27
		if !bytes.Equal(sig, want) {
			return "", ErrUnsafeOrder
		}
		matched = true
		delegate = a.Address
	}
	if (auth != nil && !matched) || !ValidAddress(delegate) || (in.CurrentDelegate != "" && !strings.EqualFold(delegate, in.CurrentDelegate)) {
		return "", ErrUnsafeOrder
	}
	return delegate, nil
}
