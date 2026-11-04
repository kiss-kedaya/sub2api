package cryptopay

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ChainStatus reports every chain's scan position and node health for the admin
// "node status" page. It is strictly read-only: it never writes the database or
// the configuration, never triggers a scan and never touches a cursor. Each call
// also marks the page as being watched, which keeps the 15 s sampling ticker
// alive for the next chainWatchWindow.
func (s *Service) ChainStatus(ctx context.Context) (*ChainStatusReport, error) {
	if s == nil {
		return nil, errors.New("crypto service unavailable")
	}
	s.monitor().touch(s.chainStatusTick)
	return s.buildChainStatus(ctx)
}

// chainStatusTick is the ticker body: build the report only for its sampling side
// effect (probe cache and sample window), without counting as page activity.
func (s *Service) chainStatusTick(ctx context.Context) {
	_, _ = s.buildChainStatus(ctx)
}

func (s *Service) buildChainStatus(ctx context.Context) (*ChainStatusReport, error) {
	mon := s.monitor()
	mon.init()
	plans := s.chainPlans()

	var (
		cursors    map[string]chainCursor
		pending    map[string]int64
		cursorErr  error
		pendingErr error
	)
	if len(plans) > 0 {
		cursors, cursorErr = s.readChainCursors(ctx)
		pending, pendingErr = s.readChainPending(ctx)
	}
	now := mon.now()

	chains := make([]ChainStatus, len(plans))
	errs := make([]error, len(plans))
	var wg sync.WaitGroup
	for i, plan := range plans {
		wg.Add(1)
		go func() {
			defer wg.Done()
			chains[i], errs[i] = s.chainStatusFor(ctx, plan, cursors, cursorErr != nil, pending, pendingErr != nil, now)
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	sortChainStatuses(chains)
	return &ChainStatusReport{
		GeneratedAt: now.UTC().Format(time.RFC3339),
		Summary:     summarizeChains(chains),
		Chains:      chains,
	}, nil
}

func (s *Service) chainStatusFor(
	ctx context.Context, plan chainPlan,
	cursors map[string]chainCursor, cursorFailed bool,
	pending map[string]int64, pendingFailed bool, now time.Time,
) (ChainStatus, error) {
	mon := s.monitor()
	probes, err := mon.probe(ctx, plan.Chain, uint64(chainlistIDs[plan.Chain]), plan.Endpoints)
	if err != nil {
		return ChainStatus{}, err
	}
	in := chainAssembleInput{
		Plan: plan, Probes: probes, SlowAfter: mon.slowAfter,
		CursorFailed: cursorFailed, Now: now,
	}
	if !cursorFailed {
		if c, ok := cursors[plan.Chain]; ok {
			in.Cursor = &c
		}
		if plan.Enabled {
			in.MissingFor = mon.cursorMissingFor(plan.Chain, in.Cursor != nil)
		}
	}
	if !pendingFailed {
		in.Pending, in.PendingKnown = pending[plan.Chain], true
	}
	if head, ok := bestProbeHead(probes); ok && in.Cursor != nil {
		mon.record(plan.Chain, in.Cursor.Block, head)
	}
	in.Window = mon.window(plan.Chain)
	return assembleChainStatus(in), nil
}

// ChainProgress reports one chain's scan position: the best node head, the
// confirmed height and how many confirmed blocks the cursor has not covered. The
// backfill job reports the same numbers through this method rather than keeping
// a second copy. Read-only; it shares the page's probe cache, so calling it per
// chain does not multiply RPC traffic.
func (s *Service) ChainProgress(ctx context.Context, chain string) (*ChainProgress, error) {
	if s == nil {
		return nil, ErrChainNotConfigured
	}
	var plan *chainPlan
	for _, p := range s.chainPlans() {
		if p.Chain == chain {
			plan = &p
			break
		}
	}
	if plan == nil || len(plan.Endpoints) == 0 {
		return nil, ErrChainNotConfigured
	}
	probes, err := s.monitor().probe(ctx, chain, uint64(chainlistIDs[chain]), plan.Endpoints)
	if err != nil {
		return nil, err
	}
	head, ok := bestProbeHead(probes)
	if !ok {
		return nil, ErrChainHeadUnavailable
	}
	cursors, err := s.readChainCursors(ctx)
	if err != nil {
		return nil, err
	}
	confirmed := confirmedHeadOf(head, uint64(max(plan.Confirmations, 0)))
	out := &ChainProgress{Chain: chain, Head: head, ConfirmedHead: confirmed}
	if c, ok := cursors[chain]; ok {
		out.Cursor, out.CursorKnown = c.Block, true
	}
	out.Unscanned = unscannedOf(confirmed, out.Cursor)
	return out, nil
}
