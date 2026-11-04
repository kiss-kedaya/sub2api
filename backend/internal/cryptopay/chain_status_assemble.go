package cryptopay

import (
	"sort"
	"time"
)

// chainPlan is one row to report: a chain, whether the scanner covers it, and the
// nodes to probe.
type chainPlan struct {
	Chain         string
	Enabled       bool
	Confirmations int
	ScanRange     int
	Endpoints     []string
}

type chainCursor struct {
	Block     uint64
	UpdatedAt time.Time
}

// chainAssembleInput is everything one row is built from. Building it is the
// only impure step; assembleChainStatus itself is a pure function of this value.
type chainAssembleInput struct {
	Plan         chainPlan
	Probes       []nodeProbe
	SlowAfter    time.Duration
	Cursor       *chainCursor
	CursorFailed bool
	MissingFor   time.Duration
	Pending      int64
	PendingKnown bool
	Window       []chainSample
	Now          time.Time
}

func assembleChainStatus(in chainAssembleInput) ChainStatus {
	plan := in.Plan
	nodes, usable := buildNodeStatuses(plan.Endpoints, in.Probes, in.SlowAfter)
	st := ChainStatus{
		Chain: plan.Chain, Enabled: plan.Enabled,
		Confirmations: plan.Confirmations, ScanRange: plan.ScanRange,
		Nodes: nodes,
	}
	confirmations := uint64(max(plan.Confirmations, 0))

	best, haveHead := bestProbeHead(in.Probes)
	if usable == 0 {
		haveHead = false
	}
	if haveHead {
		confirmed := confirmedHeadOf(best, confirmations)
		st.Head, st.ConfirmedHead = chainPtr(best), chainPtr(confirmed)
	}

	cursorOK := in.Cursor != nil && !in.CursorFailed
	stalled := int64(in.MissingFor / time.Second)
	if cursorOK {
		st.Cursor = chainPtr(in.Cursor.Block)
		st.CursorUpdatedAt = chainPtr(in.Cursor.UpdatedAt.UTC().Format(time.RFC3339))
		stalled = secondsSince(in.Cursor.UpdatedAt, in.Now)
		st.StalledSeconds = chainPtr(stalled)
	}
	if in.PendingKnown {
		st.PendingDeposits = chainPtr(in.Pending)
	}

	lagKnown := haveHead && cursorOK
	var lag uint64
	if lagKnown {
		lag = unscannedOf(*st.ConfirmedHead, in.Cursor.Block)
		st.Unscanned = chainPtr(lag)
	}

	// Speeds describe a live comparison, so they are only reported while both
	// the head and the cursor are currently known.
	if lagKnown {
		scan, head := windowSpeeds(in.Window)
		if scan != nil && head != nil {
			st.ScanSpeedBPM, st.HeadSpeedBPM = chainPtr(round1(*scan)), chainPtr(round1(*head))
		}
	}
	threshold := chainBacklogThreshold(st.HeadSpeedBPM)

	st.State = classifyChainState(chainStateInput{
		Enabled: plan.Enabled, HasEndpoints: len(plan.Endpoints) > 0,
		UsableNodes: usable, CursorFailed: in.CursorFailed,
		CursorMissing: in.Cursor == nil && !in.CursorFailed,
		Unscanned:     lag, Threshold: threshold, StalledSeconds: stalled,
	})

	if lagKnown {
		windowLags := make([]uint64, 0, len(in.Window))
		for _, s := range in.Window {
			windowLags = append(windowLags, unscannedOf(confirmedHeadOf(s.Head, confirmations), s.Cursor))
		}
		st.ProgressPct = chainPtr(progressPct(lag, windowLags, threshold))
		// Use the unrounded speeds so a rounded value cannot flip the sign of
		// the net catch-up rate.
		scan, head := windowSpeeds(in.Window)
		st.ETASeconds = etaSeconds(st.State, lag, scan, head)
	}

	switch {
	case len(plan.Endpoints) > 0 && usable == 0:
		kind := dominantNodeError(in.Probes)
		if kind == "" {
			kind = NodeErrConnection
		}
		st.LastError = &ChainLastError{Kind: kind, Stage: ErrStageHead}
	case in.CursorFailed:
		st.LastError = &ChainLastError{Kind: ErrKindDB, Stage: ErrStageCursor}
	}
	return st
}

// sortChainStatuses keeps enabled chains first (in the configured order) and
// disabled ones after, so a chain that needs attention is never buried.
func sortChainStatuses(out []ChainStatus) {
	sort.SliceStable(out, func(i, j int) bool { return out[i].Enabled && !out[j].Enabled })
}

func summarizeChains(chains []ChainStatus) ChainStatusSummary {
	var s ChainStatusSummary
	for _, c := range chains {
		if !c.Enabled {
			continue
		}
		s.Enabled++
		switch c.State {
		case ChainStateCatchingUp:
			s.Lagging++
		case ChainStateStalled:
			s.Stalled++
		case ChainStateError, ChainStateNoRPC:
			s.Error++
		}
	}
	return s
}
