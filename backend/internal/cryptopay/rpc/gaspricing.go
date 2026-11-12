package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common/hexutil"
)

// A gas price is a signed number, not a hint: it decides whether a transaction
// can be mined at all. Two independent defects produced dead sweep bytes on BSC
// on 2026-11-13 (sweep_records.id=560, tx 0x9f208df3…, nonce 0x0, gasPrice 0x1):
//
//   - Pool.GasPrice returned whatever the first endpoint in the rotation
//     answered, with no cross-endpoint check. A public endpoint that reports
//     0x1 (1 wei) is valid JSON-RPC, so the pool accepted it even though the
//     network itself reported 0x2faf080 (50,000,000 wei) at the same time.
//   - Nothing compared the quote with the chain's own minimum, so a single bad
//     endpoint produced a fee no validator would ever include.
//
// Both are fixed here: every healthy endpoint is sampled and the median is
// used (one outlier cannot move it), and the result is clamped to a per-chain
// floor that is documented below.
const weiPerGwei = 1_000_000_000

// Minimum acceptable gas price per chain, in wei. A pool never signs below its
// chain's floor; a quote under it is treated as a bad answer, not a bargain.
const (
	// Ethereum mainnet: 1 gwei is the long-standing minimum that relayers and
	// block builders accept; the observed eth_gasPrice is 1-5 gwei.
	ethereumMinGasPriceWei = 1 * weiPerGwei
	// BSC: 0.05 gwei, measured as eth_gasPrice = 0x2faf080 on 2026-11-13. BSC
	// validators include transactions at the network's minimum gas price, which
	// is 0 for the chain but 0.05 gwei in practice on every public endpoint.
	bscMinGasPriceWei = 50_000_000
	// Polygon PoS: 30 gwei is the minimum gas price bor validators enforce;
	// anything below it is rejected by the pool, not merely unprofitable.
	polygonMinGasPriceWei = 30 * weiPerGwei
	// Arbitrum One: L2 fees are priced around 0.01 gwei on the parent chain.
	arbitrumMinGasPriceWei = 10_000_000
	// Base: OP-Stack L2, market price around 0.005-0.01 gwei.
	baseMinGasPriceWei = 1_000_000
	// X Layer: OP-Stack L2 priced like the other L2s above.
	xlayerMinGasPriceWei = 10_000_000
	// Plasma: EVM chain with mainnet-like pricing.
	plasmaMinGasPriceWei = 1 * weiPerGwei
	// unknownChainMinGasPriceWei applies when the pool cannot prove which chain
	// it serves. It is deliberately the conservative mainnet figure: signing a
	// too-low fee produces a transaction that never lands, while this value is
	// still accepted on every chain the sweeper supports.
	unknownChainMinGasPriceWei = 1 * weiPerGwei
)

// gasPriceFloors is keyed by EIP-155 chain id so a pool only needs to know the
// chain it verified, never a chain name.
var gasPriceFloors = map[uint64]int64{
	1:     ethereumMinGasPriceWei,
	56:    bscMinGasPriceWei,
	137:   polygonMinGasPriceWei,
	42161: arbitrumMinGasPriceWei,
	8453:  baseMinGasPriceWei,
	196:   xlayerMinGasPriceWei,
	9745:  plasmaMinGasPriceWei,
}

// GasPriceFloor returns the minimum gas price, in wei, that a pool may sign
// with on chainID. An unknown chain id gets the conservative default rather
// than no floor: a missing floor is exactly the defect this guards against.
// The returned value is a copy the caller may modify.
func GasPriceFloor(chainID uint64) *big.Int {
	if floor, ok := gasPriceFloors[chainID]; ok {
		return big.NewInt(floor)
	}
	return big.NewInt(unknownChainMinGasPriceWei)
}

// decodeGasPrice accepts only a strictly positive JSON-RPC quantity. hexutil
// already rejects a missing 0x prefix, leading zeros, negative values and
// numbers wider than 256 bits; zero is rejected here because it cannot be
// signed and a node answering it is not describing a usable fee.
func decodeGasPrice(result json.RawMessage) (*big.Int, error) {
	var hex string
	if err := json.Unmarshal(result, &hex); err != nil {
		return nil, &Failure{Kind: "response"}
	}
	if hex == "" || strings.HasPrefix(hex, "-") {
		return nil, &Failure{Kind: "response"}
	}
	value, err := hexutil.DecodeBig(hex)
	if err != nil || value.Sign() <= 0 {
		return nil, &Failure{Kind: "response"}
	}
	return value, nil
}

// robustGasPrice samples every endpoint that is not cooling down and returns
// the median of the usable answers. Median, not maximum: a single endpoint
// reporting 5000 gwei while its peers report 20 and 50 must not set the fee,
// and a single endpoint reporting 1 wei must not set it either.
func (p *Pool) robustGasPrice(ctx context.Context) (*big.Int, error) {
	p.mu.RLock()
	endpoints := append([]string(nil), p.endpoints...)
	p.mu.RUnlock()
	if len(endpoints) == 0 {
		return nil, fmt.Errorf("no rpc endpoints configured")
	}
	health := p.healthTracker()
	candidates := health.eligible(endpoints)
	samples := make([]*big.Int, 0, len(candidates))
	var lastErr error
	for _, endpoint := range candidates {
		result, err := p.callOne(ctx, endpoint, "eth_gasPrice", nil)
		var value *big.Int
		if err == nil {
			value, err = decodeGasPrice(result)
		}
		if err == nil {
			health.succeed(endpoint)
			samples = append(samples, value)
			continue
		}
		// A well-formed but unusable answer (0x0) is a response failure for this
		// sample only: it must not authorize a price, and it must not stop the
		// other endpoints from being asked.
		health.fail(endpoint, err)
		lastErr = err
		if ctx.Err() != nil {
			break
		}
	}
	if len(samples) == 0 {
		if lastErr == nil {
			lastErr = &Failure{Kind: "response"}
		}
		return nil, fmt.Errorf("all rpc endpoints failed: %w", lastErr)
	}
	return medianGasPrice(samples), nil
}

// medianGasPrice is the upper median, so an even number of samples rounds up:
// paying one step more is recoverable, signing a fee that cannot be mined is
// not. The input is not modified.
func medianGasPrice(samples []*big.Int) *big.Int {
	sorted := append([]*big.Int(nil), samples...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Cmp(sorted[j]) < 0 })
	return new(big.Int).Set(sorted[len(sorted)/2])
}

// clampGasPrice raises price to floor. A missing or zero floor leaves the price
// untouched; callers only ever lose the ability to sign below the floor.
func clampGasPrice(price, floor *big.Int) *big.Int {
	if price == nil {
		return new(big.Int).Set(floor)
	}
	if floor == nil || floor.Sign() <= 0 || price.Cmp(floor) >= 0 {
		return price
	}
	return new(big.Int).Set(floor)
}

// gasPriceCacheTTL keeps one sweep run from sampling the whole pool once per
// address. It is short enough that a real price move is picked up quickly.
const gasPriceCacheTTL = 30 * time.Second

func (p *Pool) cachedGasPrice() *big.Int {
	p.gasMu.Lock()
	defer p.gasMu.Unlock()
	if p.gasValue == nil || time.Since(p.gasAt) > gasPriceCacheTTL {
		return nil
	}
	return new(big.Int).Set(p.gasValue)
}

func (p *Pool) storeGasPrice(price *big.Int) {
	if price == nil || price.Sign() <= 0 {
		return
	}
	p.gasMu.Lock()
	defer p.gasMu.Unlock()
	p.gasAt, p.gasValue = time.Now(), new(big.Int).Set(price)
}

func (p *Pool) clearGasPriceCache() {
	p.gasMu.Lock()
	defer p.gasMu.Unlock()
	p.gasAt, p.gasValue = time.Time{}, nil
}
