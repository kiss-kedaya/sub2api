package main

import (
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// erc20TransferSelector is keccak256("transfer(address,uint256)")[:4].
var erc20TransferSelector = []byte{0xa9, 0x05, 0x9c, 0xbb}

// gasTargetAllowed reports whether a native gas top-up may pay to req.To.
// Authorisation is the derivation itself: the target must equal the address
// derived at gas_index. A caller cannot name an arbitrary payee, so the hot
// wallet stays safe even though the target is not whitelisted.
func gasTargetAllowed(d *Deriver, req evmSignRequest) bool {
	if req.Kind != "gas" || req.GasIndex == nil {
		return false
	}
	_, derived, err := d.EVMKey(*req.GasIndex)
	if err != nil {
		return false
	}
	return strings.EqualFold(derived.Hex(), req.To)
}

// signEVMTransfer builds and signs an EIP-1559 (or legacy, caller's choice)
// ERC20 transfer or native value transfer. Nonce, gas price and gas limit
// are supplied by the caller (the web process reads them from RPC).
func signEVMTransfer(d *Deriver, req evmSignRequest) (string, error) {
	key, from, err := d.EVMKey(req.Index)
	if err != nil {
		return "", err
	}
	to := common.HexToAddress(req.To)
	amount, ok := new(big.Int).SetString(req.Amount, 10)
	if !ok || amount.Sign() <= 0 {
		return "", fmt.Errorf("invalid amount")
	}
	if req.GasLimit <= 0 || req.GasLimit > 1_000_000 {
		return "", fmt.Errorf("invalid gas limit")
	}
	gasPrice, ok := new(big.Int).SetString(req.GasPrice, 10)
	if !ok || gasPrice.Sign() <= 0 {
		return "", fmt.Errorf("invalid gas price")
	}

	var data []byte
	value := new(big.Int)
	if strings.TrimSpace(req.Token) == "" || strings.EqualFold(req.Token, "native") {
		value = amount
	} else {
		token := common.HexToAddress(req.Token)
		to = token
		data = make([]byte, 0, 68)
		data = append(data, erc20TransferSelector...)
		data = append(data, common.LeftPadBytes(common.HexToAddress(req.To).Bytes(), 32)...)
		data = append(data, common.LeftPadBytes(amount.Bytes(), 32)...)
	}

	var tx *types.Transaction
	if req.ChainID == 0 {
		return "", fmt.Errorf("chain id required")
	}
	tx = types.NewTx(&types.DynamicFeeTx{
		ChainID:   big.NewInt(req.ChainID),
		Nonce:     req.Nonce,
		GasTipCap: gasPrice,
		GasFeeCap: gasPrice,
		Gas:       req.GasLimit,
		To:        &to,
		Value:     value,
		Data:      data,
	})
	signer := types.LatestSignerForChainID(big.NewInt(req.ChainID))
	signed, err := types.SignTx(tx, signer, key)
	if err != nil {
		return "", fmt.Errorf("sign: %w", err)
	}
	raw, err := signed.MarshalBinary()
	if err != nil {
		return "", fmt.Errorf("marshal: %w", err)
	}
	_ = from
	return "0x" + hex.EncodeToString(raw), nil
}

// evmSignRequest mirrors the /sign/evm request body.
type evmSignRequest struct {
	Index    uint32  `json:"index"`
	Kind     string  `json:"kind,omitempty"`      // "sweep" (default) or "gas"
	GasIndex *uint32 `json:"gas_index,omitempty"` // kind=gas: index the target derives from
	ChainID  int64   `json:"chain_id"`
	Token    string  `json:"token"` // empty/"native" = value transfer
	To       string  `json:"to"`
	Amount   string  `json:"amount"` // raw units, decimal string
	Nonce    uint64  `json:"nonce"`
	GasPrice string  `json:"gas_price"`
	GasLimit uint64  `json:"gas_limit"`
}

