package cryptopay

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
	"github.com/stretchr/testify/require"
)

func TestClassifyNodeErrorUsesOnlyTheFixedVocabulary(t *testing.T) {
	secretErr := errors.New(`Post "https://rpc.example.com/v1/SECRETKEY": dial tcp: lookup failed`)
	cases := map[string]struct {
		err  error
		want string
	}{
		"nil":               {nil, ""},
		"deadline":          {context.DeadlineExceeded, NodeErrTimeout},
		"wrapped deadline":  {fmt.Errorf("all rpc endpoints failed: %w", context.DeadlineExceeded), NodeErrTimeout},
		"transport timeout": {fmt.Errorf("wrapped: %w", rpc.NewTransportFailure(context.DeadlineExceeded)), NodeErrTimeout},
		"transport failure": {rpc.NewTransportFailure(errors.New("refused")), NodeErrConnection},
		"429":               {&rpc.Failure{Kind: "http", HTTPStatus: 429}, NodeErrHTTP429},
		"500":               {&rpc.Failure{Kind: "http", HTTPStatus: 500}, NodeErrHTTP5xx},
		"503 wrapped":       {fmt.Errorf("all rpc endpoints failed: %w", &rpc.Failure{Kind: "http", HTTPStatus: 503}), NodeErrHTTP5xx},
		"599":               {&rpc.Failure{Kind: "http", HTTPStatus: 599}, NodeErrHTTP5xx},
		"401":               {&rpc.Failure{Kind: "http", HTTPStatus: 401}, NodeErrHTTP4xx},
		"403":               {&rpc.Failure{Kind: "http", HTTPStatus: 403}, NodeErrHTTP4xx},
		"redirect is not an error class of its own": {&rpc.Failure{Kind: "http", HTTPStatus: 302}, NodeErrBadResp},
		"json-rpc error object":                     {&rpc.Failure{Kind: "remote", RPCCode: -32005}, NodeErrRPC},
		"undecodable body":                          {&rpc.Failure{Kind: "response"}, NodeErrBadResp},
		"request could not be built":                {&rpc.Failure{Kind: "request"}, NodeErrConnection},
		"text mentioning a timeout is not parsed":   {errors.New("context deadline exceeded"), NodeErrConnection},
		"unknown error carrying a secret":           {secretErr, NodeErrConnection},
	}
	allowed := map[string]bool{
		NodeErrTimeout: true, NodeErrConnection: true, NodeErrHTTP429: true, NodeErrHTTP5xx: true,
		NodeErrHTTP4xx: true, NodeErrRPC: true, NodeErrBadResp: true, "": true,
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := classifyNodeError(tc.err)
			require.Equal(t, tc.want, got)
			require.True(t, allowed[got], "%q is outside the fixed vocabulary", got)
		})
	}
}

func TestNodeIdentityKeepsOnlyMainDomainAndPosition(t *testing.T) {
	for _, tc := range []struct {
		endpoint, label, host string
		index                 int
	}{
		{"https://rpc.ankr.com/bsc/SECRETKEY", "ankr#0", "rpc.ankr.com", 0},
		{"https://bsc-mainnet.blockpi.network/v1/rpc/SECRETKEY?token=SECRETKEY", "blockpi#1", "bsc-mainnet.blockpi.network", 1},
		{"https://user:SECRETKEY@bsc-dataseed.binance.org:8545/x#frag", "binance#2", "bsc-dataseed.binance.org", 2},
		{"https://EthereumRPC.PublicNode.com", "publicnode#0", "ethereumrpc.publicnode.com", 0},
		{"https://1rpc.io/bnb", "1rpc#3", "1rpc.io", 3},
		{"https://eth.llamarpc.com", "llamarpc#0", "eth.llamarpc.com", 0},
		{"https://bsc.drpc.org", "drpc#0", "bsc.drpc.org", 0},
		{"https://node.example.co.uk/key", "example#0", "node.example.co.uk", 0},
		{"https://127.0.0.1:8545/SECRETKEY", "node#0", "127.0.0.1", 0},
		{"http://[::1]:8545/SECRETKEY", "node#0", "::1", 0},
		{"https://rpc.ankr.com./bsc", "ankr#0", "rpc.ankr.com.", 0},
		{"localhost:8545/SECRETKEY", "node#4", "", 4},
		{"not a url", "node#0", "", 0},
		{"", "node#0", "", 0},
		{"https://" + strings.Repeat("a", 40) + ".example.com", strings.Repeat("example", 1) + "#0", strings.Repeat("a", 40) + ".example.com", 0},
	} {
		label, host := nodeIdentity(tc.endpoint, tc.index)
		require.Equal(t, tc.label, label, tc.endpoint)
		require.Equal(t, tc.host, host, tc.endpoint)
		require.NotContains(t, label+host, "SECRETKEY")
		require.NotContains(t, label+host, ":8545")
		require.NotContains(t, label+host, "/")
	}
	require.Equal(t, "node", providerLabel(""))
	require.Equal(t, strings.Repeat("a", 24), providerLabel(strings.Repeat("a", 40)+".test"), "labels are capped at 24 characters")
}

func TestNodeHealthBoundariesAreStrict(t *testing.T) {
	slow := 2000 * time.Millisecond
	for name, tc := range map[string]struct {
		behind  uint64
		latency time.Duration
		want    string
	}{
		"fresh and fast":             {0, 10 * time.Millisecond, NodeStatusOK},
		"exactly 20 blocks behind":   {20, 0, NodeStatusOK},
		"21 blocks behind":           {21, 0, NodeStatusBehind},
		"exactly 2000ms":             {0, 2000 * time.Millisecond, NodeStatusOK},
		"2001ms":                     {0, 2001 * time.Millisecond, NodeStatusSlow},
		"behind outranks slow":       {21, 5 * time.Second, NodeStatusBehind},
		"20 behind but slow is slow": {20, 2001 * time.Millisecond, NodeStatusSlow},
	} {
		t.Run(name, func(t *testing.T) { require.Equal(t, tc.want, nodeHealth(tc.behind, tc.latency, slow)) })
	}
}
