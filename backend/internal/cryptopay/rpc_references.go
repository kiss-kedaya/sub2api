package cryptopay

import "github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"

// Public reference operators are deliberately independent of Chainlist's
// remotely supplied candidates and speed ranking. These contain no keys or
// installation settings. HTTPS and the pool's public-IP pinning still apply.
// Automatic mode needs two independent operators; loss of quorum keeps funds
// pending, never silently reverting to a single registry-supplied node.
var automaticRPCReferences = map[string][]string{
	ChainEthereum: {"https://ethereum-rpc.publicnode.com", "https://eth.drpc.org", "https://eth.llamarpc.com"},
	ChainBSC:      {"https://bsc-rpc.publicnode.com", "https://bsc.drpc.org", "https://1rpc.io/bnb"},
	ChainPolygon:  {"https://polygon-bor-rpc.publicnode.com", "https://polygon.drpc.org", "https://1rpc.io/matic"},
	ChainArbitrum: {"https://arbitrum-one-rpc.publicnode.com", "https://arbitrum.drpc.org", "https://1rpc.io/arb"},
	ChainBase:     {"https://base-rpc.publicnode.com", "https://base.drpc.org", "https://1rpc.io/base"},
	ChainPlasma:   {"https://rpc.plasma.to", "https://plasma.drpc.org"},
	ChainXLayer:   {"https://rpc.xlayer.tech", "https://xlayer.drpc.org"},
}

func configureRPCEndpoints(pool *rpc.Pool, chain, endpoints string, automatic bool) {
	pool.ConfigureEndpoints(endpoints, automatic, uint64(chainlistIDs[chain]), automaticRPCReferences[chain])
}
