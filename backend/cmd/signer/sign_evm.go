package main

import (
	"encoding/hex"
	"fmt"
	"math/big"
	"strconv"
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
	if req.Kind != "gas" || req.GasIndex == nil || *req.GasIndex >= 1<<31 || !common.IsHexAddress(req.To) {
		return false
	}
	_, derived, err := d.EVMKey(*req.GasIndex)
	if err != nil {
		return false
	}
	return strings.EqualFold(derived.Hex(), req.To)
}

// signEVMTransfer builds and signs an EIP-1559
// ERC20 transfer or native value transfer. Nonce, gas price and gas limit
// are supplied by the caller (the web process reads them from RPC).
func signEVMTransfer(d *Deriver, req evmSignRequest) (string, error) {
	amount, gasPrice, _, err := validateEVMRequest(req)
	if err != nil {
		return "", err
	}
	key, _, err := d.EVMKey(req.Index)
	if err != nil {
		return "", err
	}
	to := common.HexToAddress(req.To)

	var data []byte
	value := new(big.Int)
	if isEVMNative(req.Token) {
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
	return "0x" + hex.EncodeToString(raw), nil
}

func isEVMNative(token string) bool {
	return token == "" || strings.EqualFold(token, "native")
}

// Validation is shared with the budget path, so the exact fee cap and gas
// limit checked here are the ones serialized into the signed transaction.
func validateEVMRequest(req evmSignRequest) (amount, price, fee *big.Int, err error) {
	if req.ChainID <= 0 || req.Index >= 1<<31 {
		return nil, nil, nil, fmt.Errorf("invalid chain id or derivation index")
	}
	if req.Kind != "" && req.Kind != "sweep" && req.Kind != "gas" {
		return nil, nil, nil, fmt.Errorf("invalid transaction kind")
	}
	if !common.IsHexAddress(req.To) || (!isEVMNative(req.Token) && !common.IsHexAddress(req.Token)) {
		return nil, nil, nil, fmt.Errorf("invalid EVM target or token")
	}
	amount, err = positiveUint(req.Amount, 256)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("invalid uint256 amount")
	}
	price, err = positiveUint(req.GasPrice, 256)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("invalid uint256 gas price")
	}
	if req.GasLimit < 21_000 || req.GasLimit > 1_000_000 {
		return nil, nil, nil, fmt.Errorf("invalid gas limit")
	}
	fee = new(big.Int).Mul(new(big.Int).SetUint64(req.GasLimit), price)
	if fee.BitLen() > 256 {
		return nil, nil, nil, fmt.Errorf("uint256 fee overflow")
	}
	if isEVMNative(req.Token) && new(big.Int).Add(fee, amount).BitLen() > 256 {
		return nil, nil, nil, fmt.Errorf("uint256 total cost overflow")
	}
	return amount, price, fee, nil
}

func evmBudgetCharges(p *Policy, req evmSignRequest) (map[string]*big.Int, error) {
	amount, price, fee, err := validateEVMRequest(req)
	if err != nil {
		return nil, err
	}
	chain, ok := p.EVMChains[strconv.FormatInt(req.ChainID, 10)]
	if !ok {
		return nil, fmt.Errorf("chain_not_allowed")
	}
	maxPrice, err := positiveUint(chain.MaxGasPrice, 256)
	if err != nil {
		return nil, fmt.Errorf("chain gas price cap missing or invalid")
	}
	maxFee, err := positiveUint(chain.MaxFee, 256)
	if err != nil {
		return nil, fmt.Errorf("chain fee cap missing or invalid")
	}
	if chain.MaxGasLimit == 0 || req.GasLimit > chain.MaxGasLimit || price.Cmp(maxPrice) > 0 || fee.Cmp(maxFee) > 0 {
		return nil, fmt.Errorf("chain_gas_limit")
	}
	scope := "evm:" + strconv.FormatInt(req.ChainID, 10)
	nativeKey := p.limitKey(scope, "native")
	charges := map[string]*big.Int{nativeKey: fee}
	if isEVMNative(req.Token) {
		charges[nativeKey] = new(big.Int).Add(fee, amount)
		if charges[nativeKey].BitLen() > 256 {
			return nil, fmt.Errorf("uint256 total cost overflow")
		}
	} else {
		charges[p.limitKey(scope, req.Token)] = amount
	}
	return charges, nil
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

	// hotKey routes signing through the hot wallet's own seed. Set only by
	// the gas path; never accepted from the request body (json:"-").
	hotKey bool `json:"-"`
}
