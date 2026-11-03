package rpc

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A strict provider in a rotation must constrain the query for the whole pool,
// otherwise every request is rejected by that node and bisected after the fact.
func TestTightestLimitsFollowsTheStrictestEndpoint(t *testing.T) {
	ReplaceEndpointLimits(nil)
	t.Cleanup(func() { ReplaceEndpointLimits(nil) })

	SetEndpointLimits("https://loose.example/rpc", EndpointLimits{MaxBlocks: 1000, MaxAddresses: 200})
	SetEndpointLimits("https://strict.example/rpc/", EndpointLimits{MaxBlocks: 25, MaxAddresses: 50})

	pool := NewPool("https://loose.example/rpc,https://strict.example/rpc")
	limits, ok := pool.TightestLimits()
	require.True(t, ok)
	require.Equal(t, uint64(25), limits.MaxBlocks, "the strictest block cap wins")
	require.Equal(t, 50, limits.MaxAddresses, "the strictest address cap wins")
}

func TestTightestLimitsIgnoresUnconfiguredEndpoints(t *testing.T) {
	ReplaceEndpointLimits(nil)
	t.Cleanup(func() { ReplaceEndpointLimits(nil) })

	// No limits declared anywhere: the pool must not invent a constraint.
	pool := NewPool("https://a.example/rpc,https://b.example/rpc")
	_, ok := pool.TightestLimits()
	require.False(t, ok)
}

func TestTightestLimitsSkipsZeroFields(t *testing.T) {
	ReplaceEndpointLimits(nil)
	t.Cleanup(func() { ReplaceEndpointLimits(nil) })

	// A provider that only caps addresses must not zero the block span.
	SetEndpointLimits("https://addr-only.example/rpc", EndpointLimits{MaxAddresses: 100})
	SetEndpointLimits("https://blocks-only.example/rpc", EndpointLimits{MaxBlocks: 500})

	pool := NewPool("https://addr-only.example/rpc,https://blocks-only.example/rpc")
	limits, ok := pool.TightestLimits()
	require.True(t, ok)
	require.Equal(t, uint64(500), limits.MaxBlocks)
	require.Equal(t, 100, limits.MaxAddresses)
}

func TestReplaceEndpointLimitsClearsPreviousEntries(t *testing.T) {
	ReplaceEndpointLimits(nil)
	t.Cleanup(func() { ReplaceEndpointLimits(nil) })

	SetEndpointLimits("https://gone.example/rpc", EndpointLimits{MaxBlocks: 25})
	require.True(t, len(providerLimits.limits) > 0)

	ReplaceEndpointLimits(nil)
	pool := NewPool("https://gone.example/rpc")
	_, ok := pool.TightestLimits()
	require.False(t, ok, "removing a limit must take effect, not linger")
}
