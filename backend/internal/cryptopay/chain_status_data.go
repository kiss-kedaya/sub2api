package cryptopay

import (
	"context"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
)

// Both statements are read-only. The cursor table holds one row per chain; the
// pending count is served by idx_deposit_tx_status (status, chain).
const (
	chainCursorQuery  = `SELECT chain, last_block, updated_at FROM crypto_scan_cursors`
	chainPendingQuery = `SELECT chain, COUNT(*) FROM deposit_transactions WHERE status = 'pending' GROUP BY chain`
)

// monitor returns the node-status state. The zero value is ready to use.
func (s *Service) monitor() *chainMonitor { return &s.chainMon }

// chainPlans lists the rows to report: every chain the scanner covers, then every
// other chain that still has RPC nodes configured (the "configured but not
// enabled" case that silently froze BSC). Config and pools are copied under one
// read lock so a row never mixes two configurations.
func (s *Service) chainPlans() []chainPlan {
	s.mu.RLock()
	chains := slices.Clone(s.config.Chains)
	rpcCfg := maps.Clone(s.config.RPC)
	confirmations := maps.Clone(s.config.Confirmations)
	scanRange := maps.Clone(s.config.ScanRange)
	pools := s.pools // replaced wholesale on reload, never mutated in place
	s.mu.RUnlock()

	def := DefaultCryptoConfig()
	pick := func(configured, fallback map[string]int, chain string) int {
		if v, ok := configured[chain]; ok {
			return max(v, 0)
		}
		return max(fallback[chain], 0)
	}
	plans := make([]chainPlan, 0, len(chains))
	seen := make(map[string]bool, len(evmChains))
	add := func(chain string, enabled bool) {
		seen[chain] = true
		plans = append(plans, chainPlan{
			Chain: chain, Enabled: enabled,
			Confirmations: pick(confirmations, def.Confirmations, chain),
			ScanRange:     pick(scanRange, def.ScanRange, chain),
			Endpoints:     chainEndpoints(pools, rpcCfg, chain),
		})
	}
	for _, chain := range chains {
		if IsEVMChain(chain) && !seen[chain] {
			add(chain, true)
		}
	}
	for _, chain := range evmChains {
		if !seen[chain] && len(chainEndpoints(pools, rpcCfg, chain)) > 0 {
			add(chain, false)
		}
	}
	return plans
}

// chainEndpoints prefers the live pool (it also holds automatically discovered
// nodes) and falls back to the stored setting. Order is kept and duplicates are
// dropped, so a node's position, and with it its label, is stable.
func chainEndpoints(pools map[string]*rpc.Pool, rpcCfg map[string]string, chain string) []string {
	var raw []string
	if pool := pools[chain]; pool != nil {
		raw = pool.Endpoints()
	}
	if len(raw) == 0 {
		raw = strings.Split(rpcCfg[chain], ",")
	}
	seen := make(map[string]struct{}, len(raw))
	out := make([]string, 0, len(raw))
	for _, endpoint := range raw {
		endpoint = strings.TrimSpace(endpoint)
		if endpoint == "" {
			continue
		}
		if _, dup := seen[endpoint]; dup {
			continue
		}
		seen[endpoint] = struct{}{}
		out = append(out, endpoint)
	}
	return out
}

// readChainCursors reads every stored cursor with its timestamp. The driver error
// is replaced by a fixed sentinel: it can carry connection details.
func (s *Service) readChainCursors(ctx context.Context) (map[string]chainCursor, error) {
	out := map[string]chainCursor{}
	if s == nil || s.db == nil {
		return out, nil
	}
	ctx, cancel := context.WithTimeout(ctx, chainDBTimeout)
	defer cancel()
	rows, err := s.db.QueryContext(ctx, chainCursorQuery)
	if err != nil {
		return nil, ErrChainCursorUnavailable
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var chain string
		var block int64
		var updated time.Time
		if rows.Scan(&chain, &block, &updated) != nil {
			return nil, ErrChainCursorUnavailable
		}
		out[chain] = chainCursor{Block: uint64(max(block, 0)), UpdatedAt: updated}
	}
	if rows.Err() != nil {
		return nil, ErrChainCursorUnavailable
	}
	return out, nil
}

// readChainPending counts pending deposits per chain.
func (s *Service) readChainPending(ctx context.Context) (map[string]int64, error) {
	out := map[string]int64{}
	if s == nil || s.db == nil {
		return out, nil
	}
	ctx, cancel := context.WithTimeout(ctx, chainDBTimeout)
	defer cancel()
	rows, err := s.db.QueryContext(ctx, chainPendingQuery)
	if err != nil {
		return nil, errChainPendingUnavailable
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var chain string
		var count int64
		if rows.Scan(&chain, &count) != nil {
			return nil, errChainPendingUnavailable
		}
		out[chain] = count
	}
	if rows.Err() != nil {
		return nil, errChainPendingUnavailable
	}
	return out, nil
}
