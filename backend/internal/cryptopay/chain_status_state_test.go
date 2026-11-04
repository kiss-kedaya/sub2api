package cryptopay

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClassifyChainStateBoundaries(t *testing.T) {
	base := chainStateInput{Enabled: true, HasEndpoints: true, UsableNodes: 2, Threshold: 200}
	with := func(mutate func(*chainStateInput)) chainStateInput {
		in := base
		mutate(&in)
		return in
	}
	for name, tc := range map[string]struct {
		in   chainStateInput
		want string
	}{
		"configured RPC but not in chains is disabled, whatever else is true": {
			with(func(i *chainStateInput) {
				i.Enabled, i.HasEndpoints, i.UsableNodes, i.CursorFailed = false, false, 0, true
			}),
			ChainStateDisabled},
		"enabled with no RPC source at all": {
			with(func(i *chainStateInput) { i.HasEndpoints, i.UsableNodes = false, 0 }), ChainStateNoRPC},
		"no usable node": {with(func(i *chainStateInput) { i.UsableNodes = 0 }), ChainStateError},
		"unreadable cursor table": {
			with(func(i *chainStateInput) { i.CursorFailed = true }), ChainStateError},
		"error outranks a huge stalled backlog": {
			with(func(i *chainStateInput) { i.UsableNodes, i.Unscanned, i.StalledSeconds = 0, 1_000_000, 1_000_000 }),
			ChainStateError},
		"backlog exactly at the threshold is synced": {
			with(func(i *chainStateInput) { i.Unscanned = 200 }), ChainStateSynced},
		"no backlog stays synced however long the cursor idles": {
			with(func(i *chainStateInput) { i.Unscanned, i.StalledSeconds = 0, 86_400 }), ChainStateSynced},
		"one block over the threshold with a moving cursor": {
			with(func(i *chainStateInput) { i.Unscanned, i.StalledSeconds = 201, 0 }), ChainStateCatchingUp},
		"cursor idle for exactly 180s is still catching up": {
			with(func(i *chainStateInput) { i.Unscanned, i.StalledSeconds = 201, 180 }), ChainStateCatchingUp},
		"cursor idle for 181s is stalled": {
			with(func(i *chainStateInput) { i.Unscanned, i.StalledSeconds = 201, 181 }), ChainStateStalled},
		"no cursor row yet is never synced": {
			with(func(i *chainStateInput) { i.CursorMissing, i.Unscanned = true, 0 }), ChainStateCatchingUp},
		"no cursor row for 180s": {
			with(func(i *chainStateInput) { i.CursorMissing, i.StalledSeconds = true, 180 }), ChainStateCatchingUp},
		"no cursor row for 181s": {
			with(func(i *chainStateInput) { i.CursorMissing, i.StalledSeconds = true, 181 }), ChainStateStalled},
	} {
		t.Run(name, func(t *testing.T) { require.Equal(t, tc.want, classifyChainState(tc.in)) })
	}
}
