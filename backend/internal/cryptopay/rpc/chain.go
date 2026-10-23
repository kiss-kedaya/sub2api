package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ethereum/go-ethereum/common/hexutil"
)

// ForChain verifies EACH endpoint before it may serve a money-moving job.
// A successful probe on one rotating endpoint cannot authorize the others.
func (p *Pool) ForChain(ctx context.Context, chainID uint64) (*Pool, error) {
	if chainID == 0 {
		return nil, fmt.Errorf("missing expected RPC chain id")
	}
	var endpoints []string
	for _, endpoint := range p.Endpoints() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		raw, err := p.WithEndpoints(endpoint).Call(ctx, "eth_chainId", nil)
		if err != nil {
			continue
		}
		var quantity string
		if json.Unmarshal(raw, &quantity) != nil {
			continue
		}
		got, err := hexutil.DecodeUint64(quantity)
		if err == nil && got == chainID {
			endpoints = append(endpoints, endpoint)
		}
	}
	if len(endpoints) == 0 {
		return nil, fmt.Errorf("no RPC endpoints verified for chain id %d", chainID)
	}
	return p.WithEndpoints(strings.Join(endpoints, ",")), nil
}
