package sweeper

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
)

// ChainIDs maps the supported EVM chains to their chain id.
var ChainIDs = map[string]int64{
	"bsc":      56,
	"polygon":  137,
	"plasma":   9745,
	"arbitrum": 42161,
	"base":     8453,
	"xlayer":   196,
}

// isZeroAddress catches the all-zero placeholder: sweeping into it burns the
// funds, and an unset field often round-trips as 0x000…0 rather than "".
func isZeroAddress(address string) bool {
	trimmed := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(address)), "0x")
	if trimmed == "" {
		return false
	}
	return strings.Trim(trimmed, "0") == ""
}

// TokenSpec identifies the asset to move and its decimals.
type TokenSpec struct {
	Contract string
	Decimals int
}

// erc20 selector: balanceOf(address)
const selBalanceOf = "0x70a08231"

// transferGasLimit is the gas budget for one ERC20 transfer. 65k clears the
// common non-standard tokens (e.g. tokens with hooks) without over-charging.
const transferGasLimit = 65000

// Executor performs the two-stage sweep: top the child address up with
// native gas from the hot wallet, then have the child sign its own ERC20
// transfer to the hot wallet. Works on every EVM chain out of the box:
// no contract deployment, no third-party API.
type Executor struct {
	db       *sql.DB
	pools    map[string]*rpc.Pool
	signer   *SignerClient
	config   Config
	tokens   map[string]TokenSpec
	reader   BalanceReader
	hotIndex uint32
}

// NewExecutor wires the sweep executor. tokens maps chain -> watched asset.
func NewExecutor(
	db *sql.DB,
	pools map[string]*rpc.Pool,
	signer *SignerClient,
	config Config,
	tokens map[string]TokenSpec,
	reader BalanceReader,
	hotIndex uint32,
) *Executor {
	return &Executor{
		db: db, pools: pools, signer: signer, config: config,
		tokens: tokens, reader: reader, hotIndex: hotIndex,
	}
}

// Summary reports what one run did.
type Summary struct {
	DryRun   bool         `json:"dry_run"`
	Planned  int          `json:"planned"`
	Swept    int          `json:"swept"`
	Failed   int          `json:"failed"`
	GasTopUp int          `json:"gas_topups"`
	Entries  []SweepEntry `json:"entries"`
}

// SweepEntry is one address's outcome for the admin view.
type SweepEntry struct {
	Chain       string `json:"chain"`
	Address     string `json:"address"`
	Amount      string `json:"amount"`
	GasTxHash   string `json:"gas_tx_hash"`
	SweepTxHash string `json:"sweep_tx_hash"`
	Status      string `json:"status"`
	Error       string `json:"error,omitempty"`
}

// Run executes (or dry-runs) the sweep plan. Entries are processed serially
// to keep the nonce chain per address simple and to avoid hammering RPC.
func (e *Executor) Run(ctx context.Context, dryRun bool) (*Summary, error) {
	// Hard gate: a real sweep with no destination would top up gas for
	// nothing and then fail every transfer. Refuse before any spend.
	if !dryRun {
		if strings.TrimSpace(e.config.HotWalletEVM) == "" {
			return nil, fmt.Errorf("sweep refused: hot wallet (receiving address) is not configured")
		}
		if isZeroAddress(e.config.HotWalletEVM) {
			return nil, fmt.Errorf("sweep refused: hot wallet is the zero address")
		}
	}
	planner := NewPlanner(e.db, e.reader, e.config, e.pools)
	plan, err := planner.BuildPlan(ctx)
	if err != nil {
		return nil, err
	}
	summary := &Summary{DryRun: dryRun, Planned: len(plan.Entries)}
	for _, entry := range plan.Entries {
		outcome := e.sweepOne(ctx, entry, dryRun)
		summary.Entries = append(summary.Entries, outcome)
		switch outcome.Status {
		case "swept":
			summary.Swept++
		case "failed":
			summary.Failed++
		}
		if outcome.GasTxHash != "" {
			summary.GasTopUp++
		}
	}
	return summary, nil
}

// sweepOne handles one (address, token) pair. Any failure is recorded in
// sweep_records and returned instead of aborting the whole run: one bad
// address must not block the rest.
func (e *Executor) sweepOne(ctx context.Context, entry PlanEntry, dryRun bool) SweepEntry {
	out := SweepEntry{Chain: entry.Chain, Address: entry.Address, Amount: entry.RawAmount.String()}
	// Specs key on chain|contract: one address can hold both USDT and USDC,
	// and each token needs its own transfer.
	spec, ok := e.tokens[entry.Chain+"|"+strings.ToLower(entry.Token)]
	if !ok {
		out.Status, out.Error = "failed", "no token configured for chain/contract"
		return out
	}
	if spec.Contract == "" {
		out.Status, out.Error = "failed", "chain has no sweepable token contract"
		return out
	}
	pool := e.pools[entry.Chain]
	if pool == nil {
		out.Status, out.Error = "failed", "no rpc pool for chain"
		return out
	}
	if dryRun {
		out.Status = "planned"
		return out
	}

	// Read the live token balance: the plan is derived from credited
	// deposits, but the address may hold more or less by now.
	balance, err := e.tokenBalance(ctx, pool, spec.Contract, entry.Address)
	if err != nil {
		out.Status, out.Error = "failed", err.Error()
		return out
	}
	if balance.Sign() <= 0 {
		out.Status = "swept" // nothing left to move
		return out
	}
	out.Amount = balance.String()

	gasPrice, err := pool.GasPrice(ctx)
	if err != nil {
		out.Status, out.Error = "failed", err.Error()
		return out
	}
	gasBudget := new(big.Int).Mul(gasPrice, big.NewInt(transferGasLimit))
	// 50% headroom so a price bump between quote and broadcast still fits.
	gasBudget.Mul(gasBudget, big.NewInt(3))
	gasBudget.Div(gasBudget, big.NewInt(2))

	native, err := pool.Balance(ctx, entry.Address)
	if err != nil {
		out.Status, out.Error = "failed", err.Error()
		return out
	}
	if native.Cmp(gasBudget) < 0 {
		topUp := e.gasTopUpAmount(entry.Chain, gasBudget, native)
		hash, err := e.sendNativeFromHot(ctx, pool, entry.Chain, entry.Address, entry.DerivationIndex, topUp, gasPrice)
		if err != nil {
			out.Status, out.Error = "failed", fmt.Sprintf("gas top-up: %v", err)
			return out
		}
		out.GasTxHash = hash
		e.record(ctx, entry, "gas_sent", out.GasTxHash, "", "")
		if err := e.waitForReceipt(ctx, pool, hash); err != nil {
			out.Status, out.Error = "failed", fmt.Sprintf("gas top-up not mined: %v", err)
			return out
		}
	}

	txHash, err := e.sendTokenSweep(ctx, pool, entry, spec, balance, gasPrice)
	if err != nil {
		out.Status, out.Error = "failed", err.Error()
		e.record(ctx, entry, "failed", out.GasTxHash, "", err.Error())
		return out
	}
	out.SweepTxHash = txHash
	if err := e.waitForReceipt(ctx, pool, txHash); err != nil {
		out.Status, out.Error = "failed", fmt.Sprintf("sweep not mined: %v", err)
		e.record(ctx, entry, "failed", out.GasTxHash, txHash, out.Error)
		return out
	}
	out.Status = "swept"
	e.record(ctx, entry, "swept", out.GasTxHash, txHash, "")
	return out
}

// gasTopUpAmount picks how much native coin to send: the configured value,
// but at least enough to cover the transfer that follows.
func (e *Executor) gasTopUpAmount(chain string, gasBudget, native *big.Int) *big.Int {
	need := new(big.Int).Sub(gasBudget, native)
	configured := big.NewInt(0)
	if dec, ok := e.config.GasTopUp[chain]; ok && dec > 0 {
		configured = decimalToRawUnits(dec, 18)
	}
	if configured.Cmp(need) > 0 {
		return configured
	}
	// Round the shortfall up to a whole gas unit so it is never just short.
	need.Mul(need, big.NewInt(2))
	return need
}

// sendNativeFromHot signs a native transfer from the hot wallet to a child
// address. The signer authorises it by re-deriving the target.
func (e *Executor) sendNativeFromHot(ctx context.Context, pool *rpc.Pool, chain, to string, toIndex uint32, amount, gasPrice *big.Int) (string, error) {
	hot := strings.TrimSpace(e.config.HotWalletEVM)
	if hot == "" {
		return "", fmt.Errorf("hot_wallet_evm is not configured")
	}
	nonce, err := pool.Nonce(ctx, hot)
	if err != nil {
		return "", err
	}
	idx := toIndex
	signed, err := e.signer.SignEVM(ctx, EVMSignRequest{
		Index: e.hotIndex, Kind: "gas", GasIndex: &idx,
		ChainID: ChainIDs[chain], Token: "native", To: to,
		Amount: amount.String(), Nonce: nonce,
		GasPrice: gasPrice.String(), GasLimit: 21000,
	})
	if err != nil {
		return "", err
	}
	return pool.SendRawTransaction(ctx, signed)
}

// sendTokenSweep signs the ERC20 transfer from the child address itself.
func (e *Executor) sendTokenSweep(ctx context.Context, pool *rpc.Pool, entry PlanEntry, spec TokenSpec, amount, gasPrice *big.Int) (string, error) {
	hot := strings.TrimSpace(e.config.HotWalletEVM)
	if hot == "" {
		return "", fmt.Errorf("hot_wallet_evm is not configured")
	}
	nonce, err := pool.Nonce(ctx, entry.Address)
	if err != nil {
		return "", err
	}
	signed, err := e.signer.SignEVM(ctx, EVMSignRequest{
		Index: entry.DerivationIndex, ChainID: ChainIDs[entry.Chain],
		Token: spec.Contract, To: hot,
		Amount: amount.String(), Nonce: nonce,
		GasPrice: gasPrice.String(), GasLimit: transferGasLimit,
	})
	if err != nil {
		return "", err
	}
	return pool.SendRawTransaction(ctx, signed)
}

// waitForReceipt polls until the transaction is mined, then checks status.
func (e *Executor) waitForReceipt(ctx context.Context, pool *rpc.Pool, txHash string) error {
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		result, err := pool.Call(ctx, "eth_getTransactionReceipt", []any{txHash})
		if err == nil && len(result) > 0 && string(result) != "null" {
			var receipt struct {
				Status string `json:"status"`
			}
			if err := json.Unmarshal(result, &receipt); err != nil {
				return err
			}
			if strings.TrimPrefix(receipt.Status, "0x") == "1" {
				return nil
			}
			return fmt.Errorf("transaction reverted")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
	return fmt.Errorf("timeout waiting for receipt")
}

// tokenBalance reads an ERC20 balanceOf via eth_call.
func (e *Executor) tokenBalance(ctx context.Context, pool *rpc.Pool, contract, address string) (*big.Int, error) {
	addr := strings.TrimPrefix(strings.ToLower(address), "0x")
	data := selBalanceOf + strings.Repeat("0", 64-len(addr)) + addr
	result, err := pool.Call(ctx, "eth_call", []any{
		map[string]string{"to": contract, "data": data}, "latest",
	})
	if err != nil {
		return nil, err
	}
	var hexStr string
	if err := json.Unmarshal(result, &hexStr); err != nil {
		return nil, err
	}
	value, ok := new(big.Int).SetString(strings.TrimPrefix(hexStr, "0x"), 16)
	if !ok {
		return nil, fmt.Errorf("balanceOf decode %q", hexStr)
	}
	return value, nil
}

// record upserts the sweep_records row for this address/token.
func (e *Executor) record(ctx context.Context, entry PlanEntry, status, gasTx, sweepTx, errMsg string) {
	if _, err := e.db.ExecContext(ctx, `
		INSERT INTO sweep_records (chain, from_address, to_address, token, amount, gas_tx_hash, sweep_tx_hash, status, error, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,NOW())`,
		entry.Chain, entry.Address, e.config.HotWalletEVM, entry.Token,
		amountString(entry.RawAmount, entry.Decimals), gasTx, sweepTx, status, errMsg); err != nil {
		slog.Warn("[cryptopay] sweep record failed", "error", err)
	}
}

// decimalToRawUnits converts a coin amount (e.g. 0.002) to raw units at the
// given decimals.
func decimalToRawUnits(value float64, decimals int) *big.Int {
	scaled := new(big.Float).SetFloat64(value)
	scaled.Mul(scaled, new(big.Float).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)))
	raw, _ := scaled.Int(nil)
	if raw == nil {
		return big.NewInt(0)
	}
	return raw
}

// amountString renders raw units as a decimal string for NUMERIC storage.
func amountString(raw *big.Int, decimals int) string {
	if raw == nil {
		return "0"
	}
	value := new(big.Int).Set(raw)
	if decimals <= 0 {
		return value.String()
	}
	digits := value.String()
	for len(digits) <= decimals {
		digits = "0" + digits
	}
	whole := digits[:len(digits)-decimals]
	frac := strings.TrimRight(digits[len(digits)-decimals:], "0")
	if frac == "" {
		return whole
	}
	return whole + "." + frac
}
