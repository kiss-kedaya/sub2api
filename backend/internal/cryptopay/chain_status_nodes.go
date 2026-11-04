package cryptopay

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
	"golang.org/x/net/publicsuffix"
)

// classifyNodeError maps a probe failure to the fixed vocabulary. The error text
// is never inspected for display: transport and driver errors can contain the
// full endpoint URL, and node-supplied messages are untrusted.
func classifyNodeError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return NodeErrTimeout
	}
	var failure *rpc.Failure
	if errors.As(err, &failure) {
		switch failure.Kind {
		case "http":
			switch {
			case failure.HTTPStatus == http.StatusTooManyRequests:
				return NodeErrHTTP429
			case failure.HTTPStatus >= 500 && failure.HTTPStatus <= 599:
				return NodeErrHTTP5xx
			case failure.HTTPStatus >= 400 && failure.HTTPStatus <= 499:
				return NodeErrHTTP4xx
			}
			return NodeErrBadResp
		case "remote":
			return NodeErrRPC
		case "response":
			return NodeErrBadResp
		}
		return NodeErrConnection
	}
	return NodeErrConnection
}

// nodeIdentity derives the only identity the page shows for a node: the host name
// and a label made of the provider's main domain plus the node's position in the
// chain's list ("ankr#0"). Path, query, userinfo, port and scheme are dropped
// because API keys live there. A URL that does not parse gets the generic label
// and an empty host rather than being echoed back.
func nodeIdentity(endpoint string, index int) (label, host string) {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err == nil {
		host = strings.ToLower(parsed.Hostname())
	}
	return fmt.Sprintf("%s#%d", providerLabel(host), index), host
}

func providerLabel(host string) string {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "" || net.ParseIP(host) != nil {
		return "node"
	}
	name := host
	if registrable, err := publicsuffix.EffectiveTLDPlusOne(host); err == nil {
		name = registrable
	}
	if dot := strings.IndexByte(name, '.'); dot > 0 {
		name = name[:dot]
	}
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		}
		if b.Len() >= 24 {
			break
		}
	}
	if b.Len() == 0 {
		return "node"
	}
	return b.String()
}

// nodeProbe is the raw outcome of probing one node. The derived status (ok, slow,
// behind) depends on the other nodes, so it is computed per request.
type nodeProbe struct {
	// Answered: eth_chainId came back, so the node is reachable.
	Answered bool
	// WrongChain: it answered, but for a different chain than the one expected.
	WrongChain bool
	// HasHead: eth_blockNumber also came back; Head is valid.
	HasHead bool
	Head    uint64
	// HasLatency: Latency is a real round trip (zero must not read as "instant").
	HasLatency bool
	Latency    time.Duration
	ErrorKind  string
}

// nodeHealth grades a node that returned a head. Falling behind outranks being
// slow: a stale head silently delays deposits, a slow answer only costs time.
// Both limits are strict (exactly 20 blocks or exactly 2000 ms is still ok).
func nodeHealth(behind uint64, latency, slowAfter time.Duration) string {
	switch {
	case behind > nodeBehindBlocks:
		return NodeStatusBehind
	case latency > slowAfter:
		return NodeStatusSlow
	}
	return NodeStatusOK
}

// bestProbeHead is the highest head among nodes that answered for the right chain.
// A node on the wrong chain must not set the bar everyone else is measured against.
func bestProbeHead(probes []nodeProbe) (uint64, bool) {
	var best uint64
	found := false
	for _, p := range probes {
		if p.WrongChain || !p.HasHead {
			continue
		}
		if !found || p.Head > best {
			best, found = p.Head, true
		}
	}
	return best, found
}

// buildNodeStatuses renders one status per configured endpoint, in list order,
// and counts the nodes usable for scanning (ok, slow or behind).
func buildNodeStatuses(endpoints []string, probes []nodeProbe, slowAfter time.Duration) ([]ChainNodeStatus, int) {
	best, haveBest := bestProbeHead(probes)
	nodes := make([]ChainNodeStatus, 0, len(endpoints))
	usable := 0
	for i, endpoint := range endpoints {
		label, host := nodeIdentity(endpoint, i)
		node := ChainNodeStatus{Label: label, Host: host}
		var probe nodeProbe
		if i < len(probes) {
			probe = probes[i]
		}
		if probe.HasLatency {
			node.LatencyMS = chainPtr(probe.Latency.Milliseconds())
		}
		switch {
		case probe.WrongChain:
			node.Status = NodeStatusWrongChain
			node.ErrorKind = chainPtr(NodeErrWrongChain)
		case !probe.HasHead || !haveBest:
			node.Status = NodeStatusError
			kind := probe.ErrorKind
			if kind == "" {
				kind = NodeErrConnection
			}
			node.ErrorKind = &kind
		default:
			var behind uint64
			if best > probe.Head {
				behind = best - probe.Head
			}
			node.Head = chainPtr(probe.Head)
			node.Behind = chainPtr(behind)
			node.Status = nodeHealth(behind, probe.Latency, slowAfter)
			usable++
		}
		nodes = append(nodes, node)
	}
	return nodes, usable
}

// dominantNodeError is the most common failure kind among nodes without a head,
// first seen wins a tie. It feeds last_error when no node is usable.
func dominantNodeError(probes []nodeProbe) string {
	var order []string
	counts := map[string]int{}
	for _, p := range probes {
		if p.HasHead && !p.WrongChain {
			continue
		}
		kind := p.ErrorKind
		if p.WrongChain {
			kind = NodeErrWrongChain
		}
		if kind == "" {
			kind = NodeErrConnection
		}
		if counts[kind] == 0 {
			order = append(order, kind)
		}
		counts[kind]++
	}
	best, bestCount := "", 0
	for _, kind := range order {
		if counts[kind] > bestCount {
			best, bestCount = kind, counts[kind]
		}
	}
	return best
}
