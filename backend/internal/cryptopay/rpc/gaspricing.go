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
// can be mined at all. Pool.GasPrice used to return whatever the first endpoint
// in the rotation answered. A public BSC endpoint that reports 0x1 (1 wei) is
// valid JSON-RPC, so the pool accepted it and the sweeper signed a transaction
// that can never be included, even though the network itself reported 0x2faf080
// (50,000,000 wei) at the same moment.
//
// Every endpoint that is not cooling down is therefore sampled and the median
// is used: one outlier can no longer move the fee in either direction.

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
