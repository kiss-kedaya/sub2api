package sweeper

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

func (e *Executor) prepareGas(ctx context.Context, pool *rpc.Pool, j *sweepJob, amount, price *big.Int) error {
	if e.wallet == nil {
		return fmt.Errorf("wallet is not initialized")
	}
	hot, err := e.wallet.HotAddresses(ctx)
	if err != nil {
		return err
	}
	if !common.IsHexAddress(hot.EVM) || isZeroAddress(hot.EVM) {
		return fmt.Errorf("wallet returned invalid gas payer")
	}
	busy, err := e.gasPayerBusy(ctx, j, hot.EVM)
	if err != nil {
		return &persistenceError{err}
	}
	if busy {
		return fmt.Errorf("gas payer has an unresolved transaction; funding deferred")
	}
	// The receiving address may be a cold wallet. Its nonce is irrelevant.
	nonce, err := pool.Nonce(ctx, hot.EVM)
	if err != nil {
		return err
	}
	idx := j.DerivationIndex
	raw, err := e.wallet.SignEVM(ctx, EVMSignRequest{Index: hot.Index, Kind: "gas", GasIndex: &idx,
		ChainID: ChainIDs[j.Chain], Token: "native", To: j.Address, Amount: amount.String(),
		Nonce: nonce, GasPrice: price.String(), GasLimit: 21000})
	if err != nil {
		return err
	}
	hash, err := validateSigned(raw, j.Chain, hot.EVM, j.Address, "native", amount, nonce, 21000, price)
	if err != nil {
		return err
	}
	j.GasPayer, j.GasAmount, j.GasNonce = hot.EVM, amount.String(), strconv.FormatUint(nonce, 10)
	j.GasRaw, j.GasHash, j.Status, j.Error = raw, hash, "gas_pending", ""
	return e.saveJob(ctx, j) // autocommit; broadcast is only in settle
}

func (e *Executor) prepareSweep(ctx context.Context, pool *rpc.Pool, j *sweepJob, price *big.Int) error {
	if e.wallet == nil {
		return fmt.Errorf("wallet is not initialized")
	}
	nonce, err := pool.Nonce(ctx, j.Address)
	if err != nil {
		return err
	}
	raw, err := e.wallet.SignEVM(ctx, EVMSignRequest{Index: j.DerivationIndex, Kind: "sweep",
		ChainID: ChainIDs[j.Chain], Token: j.Token, To: j.Destination, Amount: j.RawAmount.String(),
		Nonce: nonce, GasPrice: price.String(), GasLimit: transferGasLimit})
	if err != nil {
		return err
	}
	hash, err := validateSigned(raw, j.Chain, j.Address, j.Destination, j.Token,
		j.RawAmount, nonce, transferGasLimit, price)
	if err != nil {
		return err
	}
	j.SweepNonce, j.SweepRaw, j.SweepHash = strconv.FormatUint(nonce, 10), raw, hash
	j.Status, j.Error = "sweep_pending", ""
	return e.saveJob(ctx, j)
}

// Decode and verify the signed bytes, rather than trusting a wallet/RPC hash.
// This also detects seed/index changes before any funds can be spent.
func validateSigned(raw, chain, from, to, token string, amount *big.Int, nonce, gas uint64, price *big.Int) (string, error) {
	encoded, err := hex.DecodeString(strings.TrimPrefix(raw, "0x"))
	if err != nil {
		return "", fmt.Errorf("invalid signed transaction encoding: %w", err)
	}
	var tx types.Transaction
	if err := tx.UnmarshalBinary(encoded); err != nil {
		return "", fmt.Errorf("invalid signed transaction: %w", err)
	}
	chainID := ChainIDs[chain]
	if chainID <= 0 || tx.ChainId().Cmp(big.NewInt(chainID)) != 0 ||
		tx.Nonce() != nonce || tx.Gas() != gas || tx.To() == nil ||
		!common.IsHexAddress(from) || !common.IsHexAddress(to) || amount == nil || amount.Sign() <= 0 ||
		(tx.Type() != types.DynamicFeeTxType && tx.Type() != types.LegacyTxType) {
		return "", fmt.Errorf("signed transaction does not match reserved chain/nonce/gas")
	}
	sender, err := types.Sender(types.LatestSignerForChainID(big.NewInt(chainID)), &tx)
	if err != nil || sender != common.HexToAddress(from) {
		return "", fmt.Errorf("signed transaction sender does not match reserved payer")
	}
	if tx.GasFeeCap().Sign() <= 0 || tx.GasTipCap().Cmp(tx.GasFeeCap()) > 0 ||
		(price != nil && tx.GasFeeCap().Cmp(price) != 0) {
		return "", fmt.Errorf("signed transaction fee does not match request")
	}
	if token == "native" {
		if *tx.To() != common.HexToAddress(to) || tx.Value().Cmp(amount) != 0 || len(tx.Data()) != 0 {
			return "", fmt.Errorf("signed gas payment differs from reserved payment")
		}
	} else {
		if !common.IsHexAddress(token) || amount.BitLen() > 256 {
			return "", fmt.Errorf("invalid reserved token/amount")
		}
		data := common.FromHex("0xa9059cbb")
		data = append(data, common.LeftPadBytes(common.HexToAddress(to).Bytes(), 32)...)
		data = append(data, common.LeftPadBytes(amount.Bytes(), 32)...)
		if *tx.To() != common.HexToAddress(token) || tx.Value().Sign() != 0 || !bytes.Equal(tx.Data(), data) {
			return "", fmt.Errorf("signed token transfer differs from immutable plan")
		}
	}
	return tx.Hash().Hex(), nil
}

type receiptState int

const (
	receiptAbsent receiptState = iota
	receiptWaiting
	receiptConfirmed
	receiptReverted
)

// No polling loops: a pending job returns control to the rest of the group.
// Missing confirmation policy permits observation, never finalization.
func checkReceipt(ctx context.Context, pool *rpc.Pool, hash string, required uint64) (receiptState, error) {
	result, err := pool.Call(ctx, "eth_getTransactionReceipt", []any{hash})
	if err != nil {
		return receiptWaiting, err
	}
	if len(result) == 0 || string(result) == "null" {
		return receiptAbsent, nil
	}
	var r struct {
		Hash        string `json:"transactionHash"`
		Status      string `json:"status"`
		BlockNumber string `json:"blockNumber"`
		BlockHash   string `json:"blockHash"`
	}
	if err := json.Unmarshal(result, &r); err != nil {
		return receiptWaiting, err
	}
	if !strings.EqualFold(r.Hash, hash) || len(r.BlockHash) != 66 ||
		(r.Status != "0x1" && r.Status != "0x0") {
		return receiptWaiting, fmt.Errorf("incomplete or mismatched receipt")
	}
	block, err := strconv.ParseUint(strings.TrimPrefix(r.BlockNumber, "0x"), 16, 64)
	if err != nil {
		return receiptWaiting, err
	}
	canonical, err := pool.Call(ctx, "eth_getBlockByNumber", []any{r.BlockNumber, false})
	if err != nil {
		return receiptWaiting, err
	}
	var header struct {
		Hash string `json:"hash"`
	}
	if err := json.Unmarshal(canonical, &header); err != nil {
		return receiptWaiting, err
	}
	if !strings.EqualFold(header.Hash, r.BlockHash) {
		return receiptWaiting, fmt.Errorf("receipt block is no longer canonical")
	}
	head, err := pool.BlockNumber(ctx)
	if err != nil {
		return receiptWaiting, err
	}
	if required == 0 || head < block || head-block < required {
		return receiptWaiting, nil
	}
	if r.Status == "0x0" {
		return receiptReverted, nil
	}
	return receiptConfirmed, nil
}

// settle only rebroadcasts identical persisted bytes, and only after a
// successful receipt query says absent. RPC failures never authorize a new tx.
func (e *Executor) settle(ctx context.Context, pool *rpc.Pool, j *sweepJob, gas bool, required uint64) (bool, error) {
	raw, hash, nonceStr := j.SweepRaw, j.SweepHash, j.SweepNonce
	from, to, token, amount, limit := j.Address, j.Destination, j.Token, j.RawAmount, uint64(transferGasLimit)
	if gas {
		raw, hash, nonceStr = j.GasRaw, j.GasHash, j.GasNonce
		from, to, token, limit = j.GasPayer, j.Address, "native", 21000
		amount, _ = new(big.Int).SetString(j.GasAmount, 10)
	}
	nonce, err := strconv.ParseUint(nonceStr, 10, 64)
	if err == nil {
		var actual string
		actual, err = validateSigned(raw, j.Chain, from, to, token, amount, nonce, limit, nil)
		if err == nil && !strings.EqualFold(hash, actual) {
			err = fmt.Errorf("persisted transaction hash does not match signed bytes")
		}
	}
	if err != nil {
		j.Status = "manual_review"
		_, saveErr := e.jobError(ctx, j, err)
		return false, saveErr
	}
	state, err := checkReceipt(ctx, pool, hash, required)
	if err == nil && state == receiptAbsent {
		var returned string
		if guard, ok := e.wallet.(interface {
			GuardSweepBroadcast(context.Context, func() (string, error)) (string, error)
		}); ok {
			returned, err = guard.GuardSweepBroadcast(ctx, func() (string, error) { return pool.SendRawTransaction(ctx, raw) })
		} else {
			if guard, ok := e.wallet.(interface{ ValidateSweepAuthorization(context.Context) error }); ok {
				if guardErr := guard.ValidateSweepAuthorization(ctx); guardErr != nil {
					return false, guardErr
				}
			}
			returned, err = pool.SendRawTransaction(ctx, raw)
		}
		if err == nil && !strings.EqualFold(returned, hash) {
			err = fmt.Errorf("broadcast returned a different transaction hash; outcome unknown")
		}
		if err == nil {
			state, err = checkReceipt(ctx, pool, hash, required)
		}
	}
	if err != nil {
		j.Error = rpc.SafeError(err)
		return false, joinErrors(err, e.saveJob(ctx, j))
	}
	if state == receiptReverted {
		j.Status, j.Error = "manual_review", "confirmed transaction reverted; automatic replacement/funding refused"
		return false, e.saveJob(ctx, j)
	}
	if state != receiptConfirmed {
		j.Error = "awaiting canonical receipt and required confirmations"
		return false, e.saveJob(ctx, j)
	}
	j.Error = ""
	if gas {
		j.GasConfirmed, j.Status = true, "gas_confirmed"
	} else {
		j.Status = "swept"
	}
	if err := e.saveJob(ctx, j); err != nil {
		return false, err
	}
	return true, nil
}
