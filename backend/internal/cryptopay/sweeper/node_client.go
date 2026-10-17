package sweeper

import (
	"context"
	"math/big"
)

// nodeReader is the read-only slice of an RPC pool the planner needs to
// estimate gas. Keeping it an interface lets tests inject balances without a
// live chain and keeps the plan path free of signing or broadcasting.
type nodeReader interface {
	Balance(ctx context.Context, address string) (*big.Int, error)
	GasPrice(ctx context.Context) (*big.Int, error)
}
