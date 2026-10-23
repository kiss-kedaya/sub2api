package wallet

import (
	"context"
	"encoding/hex"
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/mode"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

var erc20TransferSelector = []byte{0xa9, 0x05, 0x9c, 0xbb}

// SignEVM signs an EIP-1559 transaction offline. Gas is always funded by hot
// index zero to a verified deposit child; deposits can only send ERC20 tokens
// to the configured destination. It never makes an RPC or HTTP request.
func (m *Manager) SignEVM(ctx context.Context, req EVMSignRequest) (string, error) {
	if err := mode.CheckWritable(); err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	const op = "sign_evm"
	deny := func(err error) (string, error) { return "", m.denied(op, err) }
	if err := ctx.Err(); err != nil {
		return deny(err)
	}
	if !m.enabled {
		return deny(ErrSigningDisabled)
	}
	amount, price, err := validateEVMRequest(req)
	if err != nil {
		return deny(err)
	}
	// Query the durable version on EVERY signature. An import in another
	// gateway instance invalidates the cached seed before any cached key is used.
	// Keep a shared row lock through signing and the durable audit, closing
	// the version-check/import race. No sweep advisory lock is taken here:
	// the caller may already own that lock on a separate reserved connection.
	storeTx, err := m.currentVaultTx(ctx)
	if err != nil {
		return deny(err)
	}
	defer func() { _ = storeTx.Rollback() }()
	// Roll back BEFORE recording a denial via db. This also works with a
	// one-connection pool, avoiding pool starvation and self-deadlocks.
	deny = func(err error) (string, error) { _ = storeTx.Rollback(); return "", m.denied(op, err) }
	deriver := m.deposit
	if req.Kind == "gas" {
		if !isEVMNative(req.Token) || req.GasIndex == nil || *req.GasIndex >= 1<<31 {
			return deny(errors.New("gas requires native payment to our derived gas_index"))
		}
		_, target, err := m.deposit.EVMKey(*req.GasIndex)
		if err != nil {
			return deny(errors.New("gas target derivation failed"))
		}
		if !strings.EqualFold(target.Hex(), req.To) {
			return deny(errors.New("gas target is not our deposit address"))
		}
		deriver = m.hot
		req.Index = 0
	} else {
		if isEVMNative(req.Token) {
			return deny(errors.New("deposit sweeps must be ERC20 transfers"))
		}
		if m.destination == "" || !strings.EqualFold(m.destination, req.To) {
			return deny(errors.New("sweep destination is not configured or does not match"))
		}
	}
	key, _, err := deriver.EVMKey(req.Index)
	if err != nil {
		return deny(errors.New("signing key derivation failed"))
	}
	to := common.HexToAddress(req.To)
	value := new(big.Int)
	var data []byte
	if isEVMNative(req.Token) {
		value = amount
	} else {
		to = common.HexToAddress(req.Token)
		data = append(data, erc20TransferSelector...)
		data = append(data, common.LeftPadBytes(common.HexToAddress(req.To).Bytes(), 32)...)
		data = append(data, common.LeftPadBytes(amount.Bytes(), 32)...)
	}
	tx := types.NewTx(&types.DynamicFeeTx{ChainID: big.NewInt(req.ChainID), Nonce: req.Nonce,
		GasTipCap: price, GasFeeCap: price, Gas: req.GasLimit, To: &to, Value: value, Data: data})
	signed, err := types.SignTx(tx, types.LatestSignerForChainID(big.NewInt(req.ChainID)), key)
	if err != nil {
		return deny(errors.New("wallet signing failed"))
	}
	encoded, err := signed.MarshalBinary()
	if err != nil {
		return deny(errors.New("wallet transaction encoding failed"))
	}
	if err := writeAudit(ctx, storeTx, auditEntry{op: op, kind: req.Kind, index: req.Index, chainID: req.ChainID,
		to: req.To, token: req.Token, amount: req.Amount, result: "ok"}); err != nil {
		return "", err
	}
	if err := storeTx.Commit(); err != nil {
		return "", ErrAuditUnavailable
	}
	return "0x" + hex.EncodeToString(encoded), nil
}

func isEVMNative(token string) bool { return token == "" || strings.EqualFold(token, "native") }

func positiveUint(raw string) (*big.Int, error) {
	if len(raw) == 0 || len(raw) > 78 {
		return nil, errors.New("invalid positive uint256")
	}
	for _, c := range raw {
		if c < '0' || c > '9' {
			return nil, errors.New("invalid positive uint256")
		}
	}
	v, ok := new(big.Int).SetString(raw, 10)
	if !ok || v.Sign() <= 0 || v.BitLen() > 256 {
		return nil, errors.New("invalid positive uint256")
	}
	return v, nil
}

// These are the old signer's basic transaction checks, without its policy or
// local SQLite budgets. Fee caps serialized below are exactly those checked.
func validateEVMRequest(req EVMSignRequest) (*big.Int, *big.Int, error) {
	if req.ChainID <= 0 || req.Index >= 1<<31 {
		return nil, nil, errors.New("invalid chain id or derivation index")
	}
	// Match the six networks supported by the deposit/sweep catalog. This
	// is a transaction invariant, not the removed independent approval policy.
	switch req.ChainID {
	case 56, 137, 9745, 42161, 8453, 196:
	default:
		return nil, nil, errors.New("unsupported EVM chain id")
	}
	if req.Kind != "" && req.Kind != "sweep" && req.Kind != "gas" {
		return nil, nil, errors.New("invalid transaction kind")
	}
	if !common.IsHexAddress(req.To) || common.HexToAddress(req.To) == (common.Address{}) ||
		(!isEVMNative(req.Token) && (!common.IsHexAddress(req.Token) || common.HexToAddress(req.Token) == (common.Address{}))) {
		return nil, nil, errors.New("invalid EVM target or token")
	}
	amount, err := positiveUint(req.Amount)
	if err != nil {
		return nil, nil, errors.New("invalid uint256 amount")
	}
	price, err := positiveUint(req.GasPrice)
	if err != nil {
		return nil, nil, errors.New("invalid uint256 gas price")
	}
	if req.GasLimit < 21_000 || req.GasLimit > 1_000_000 {
		return nil, nil, errors.New("invalid gas limit")
	}
	expectedGas := uint64(65_000)
	if req.Kind == "gas" {
		expectedGas = 21_000
	}
	if req.GasLimit != expectedGas {
		return nil, nil, errors.New("gas limit does not match wallet transaction kind")
	}
	fee := new(big.Int).Mul(new(big.Int).SetUint64(req.GasLimit), price)
	if fee.BitLen() > 256 {
		return nil, nil, errors.New("uint256 fee overflow")
	}
	if isEVMNative(req.Token) && new(big.Int).Add(fee, amount).BitLen() > 256 {
		return nil, nil, errors.New("uint256 total cost overflow")
	}
	return amount, price, nil
}
