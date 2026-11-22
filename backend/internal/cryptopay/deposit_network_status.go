package cryptopay

import (
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"slices"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/scanner"
)

const (
	depositStatusAvailable = "available"
	// depositStatusSlow is a still-caught-up scanner with a moderate backlog:
	// the network is slower than usual, but nothing is broken. It is kept
	// separate from delayed so the page can say so calmly in neutral words.
	depositStatusSlow    = "slow"
	depositStatusDelayed = "delayed"
	depositStatusPaused  = "paused"
	depositStatusStalled = "stalled"
	depositStatusUnknown = "unknown"

	// A long or missing pass loses its evidence. Do not carry a stopped state
	// through an unobserved interval just to accommodate a slow scanner.
	depositNetworkTTL       = 2 * time.Minute
	depositNetworkIOTimeout = 350 * time.Millisecond
)

// Customer-facing backlog thresholds. A scan cursor is almost always a little
// behind the confirmed head: BSC mines one block every ~3s while the scanner
// covers the interval in batches, so "tens of blocks behind" is the normal,
// healthy state and must never raise a notice. Only a backlog measured in
// chain time is user-relevant, so a block count is converted with the network's
// own block interval before it is classified:
//
//	100 BSC blocks (3s each)   = 5 minutes
//	100 ETH blocks (12s each)  = 20 minutes
//	100 Arbitrum blocks (.25s) = 25 seconds
//
// Bands (a backlog exactly at a boundary stays in the calmer band, so the
// notice never appears one second earlier than the stated window):
//
//	<= 5 min   -> available, the page shows no notice at all
//	5–30 min   -> slow, a calm "network is slower" note
//	> 30 min   -> delayed, a real delay
//
// Scan/RPC failures keep their own path (delayed, then stalled) because they
// are abnormal regardless of how many blocks the cursor reached.
const (
	depositLagNoticeAfter  = 5 * time.Minute
	depositLagDelayedAfter = 30 * time.Minute
)

// depositChainBlockTime is the nominal block interval of each deposit network.
// It is used only to translate a cursor backlog into chain time for the bands
// above; it never affects crediting, confirmations or money. Values are kept at
// or above the observed interval: overestimating the interval can only hide a
// borderline notice, underestimating it could invent one.
var depositChainBlockTime = map[string]time.Duration{
	ChainBSC:      3 * time.Second,
	ChainEthereum: 12 * time.Second,
	ChainPolygon:  2 * time.Second,
	ChainPlasma:   2 * time.Second,
	ChainArbitrum: 250 * time.Millisecond,
	ChainBase:     2 * time.Second,
	ChainXLayer:   3 * time.Second,
}

// depositChainBlockTimeDefault applies to a network without an entry above.
const depositChainBlockTimeDefault = 3 * time.Second

// depositBacklogChainTime converts an unscanned block count into how much chain
// time it represents.
func depositBacklogChainTime(chain string, blocks uint64) time.Duration {
	if blocks == 0 {
		return 0
	}
	interval := depositChainBlockTime[chain]
	if interval <= 0 {
		interval = depositChainBlockTimeDefault
	}
	if maxBlocks := uint64(math.MaxInt64 / int64(interval)); blocks > maxBlocks {
		return time.Duration(math.MaxInt64)
	}
	return time.Duration(blocks) * interval
}

// ETA model. The scanner polls every 5s, so even a fully caught-up network pays
// one scan period before a transfer is seen. The rest is waiting:
//
//	instant credit : backlog + scan period
//	final credit   : confirmations × block time + backlog + scan period
//
// The result is ceiled to whole seconds and clamped so a stuck cursor cannot
// publish an hour-long "estimate" and an empty backlog cannot publish zero.
const (
	depositScanPeriod = 5 * time.Second
	depositETAFloor   = 5 * time.Second
	depositETACeiling = 30 * time.Minute
)

// depositBacklogSeconds measures how far the durable cursor lags the confirmed
// head in chain time. ok=false means this pass could not prove the distance
// (no cursor yet, unmeasurable chain, or a rewind), and callers must publish
// null rather than a fabricated number.
func depositBacklogSeconds(o scanner.DepositScanObservation) (int64, bool) {
	if !o.CanMeasure || o.ConfirmedHead == 0 || o.Cursor == 0 || o.Cursor > o.ConfirmedHead {
		return 0, false
	}
	seconds := int64(math.Ceil(depositBacklogChainTime(o.Chain, o.ConfirmedHead-o.Cursor).Seconds()))
	return seconds, true
}

// depositETA estimates the normal-path time to credit. backlogSeconds must be a
// real measurement; a nil backlog yields a nil ETA.
func depositETA(chain string, confirmations int64, instant bool, backlogSeconds *int64) *int64 {
	if backlogSeconds == nil || confirmations < 0 {
		return nil
	}
	wait := depositScanPeriod + time.Duration(*backlogSeconds)*time.Second
	if !instant {
		interval := depositChainBlockTime[chain]
		if interval <= 0 {
			interval = depositChainBlockTimeDefault
		}
		wait += time.Duration(confirmations) * interval
	}
	if wait < depositETAFloor {
		wait = depositETAFloor
	}
	if wait > depositETACeiling {
		wait = depositETACeiling
	}
	seconds := int64(math.Ceil(wait.Seconds()))
	return &seconds
}

// DepositNetworkStatusReport is the customer-safe chain status read model.
type DepositNetworkStatusReport struct {
	Networks []DepositNetworkStatusRow `json:"networks"`
}

// DepositNetworkStatusRow exposes only the network, decision, and evidence age.
type DepositNetworkStatusRow struct {
	Chain      string     `json:"chain"`
	Status     string     `json:"status"`
	ObservedAt *time.Time `json:"observed_at,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	// BacklogSeconds is how far the scanner cursor sits behind the confirmed
	// head, measured in chain time. Null when this instance has no fresh
	// measurement: an absent number is honest, a guessed one is not.
	BacklogSeconds *int64 `json:"backlog_seconds"`
	// EtaSeconds is the normal-path time from "network accepts the transfer" to
	// "balance credited". Null when it cannot be computed from fresh data.
	EtaSeconds *int64 `json:"eta_seconds"`
	// RequiredConfirmations is the configured final depth for this network.
	// Null only when the stored config is unreadable.
	RequiredConfirmations *int64 `json:"required_confirmations"`
	// InstantCredit reports whether this network currently credits on first
	// detection (0 confirmations) instead of waiting for final depth.
	InstantCredit bool `json:"instant_credit"`
}

type networkStatusSnapshot struct {
	Status      string `json:"status"`
	ObservedMS  int64  `json:"at"`
	EvidenceMS  int64  `json:"since"`
	Fingerprint string `json:"config"`
	// BacklogSec is the durable cursor's distance from the confirmed head in
	// chain time; BacklogKnown is false when this pass could not measure it.
	BacklogSec   int64 `json:"backlog_sec,omitempty"`
	BacklogKnown bool  `json:"backlog_known,omitempty"`
}

type networkStatusStore interface {
	load(context.Context, []string) (map[string]networkStatusSnapshot, error)
	save(context.Context, string, networkStatusSnapshot, time.Duration) error
}

type depositNetworkMonitor struct {
	store networkStatusStore
	now   func() time.Time
	// One write per chain can be in flight. Slow Redis cannot delay a scan or
	// accumulate retries or goroutines across reloads.
	writers map[string]chan struct{}
}

func newDepositNetworkMonitor(store networkStatusStore, now func() time.Time) *depositNetworkMonitor {
	m := &depositNetworkMonitor{store: store, now: now, writers: make(map[string]chan struct{}, len(evmChains))}
	for _, chain := range evmChains {
		m.writers[chain] = make(chan struct{}, 1)
	}
	return m
}

// observer is allocated per scanner instance. Its evidence cannot cross a
// worker reload, a skipped round, a rewind, or a gap in observations.
func (m *depositNetworkMonitor) observer(fingerprint string) func(scanner.DepositScanObservation) {
	if m == nil || m.store == nil || fingerprint == "" {
		return nil
	}
	history := make(map[string]*networkScanEvidence, len(evmChains))
	return func(o scanner.DepositScanObservation) {
		slot := m.writers[o.Chain]
		if slot == nil {
			return
		}
		state := history[o.Chain]
		if state == nil {
			state = &networkScanEvidence{}
			history[o.Chain] = state
		}
		status, since := state.observe(o)
		backlog, backlogKnown := depositBacklogSeconds(o)
		snapshot := networkStatusSnapshot{Status: status, ObservedMS: o.At.UnixMilli(), EvidenceMS: since.UnixMilli(), Fingerprint: fingerprint, BacklogSec: backlog, BacklogKnown: backlogKnown}
		select {
		case slot <- struct{}{}:
			go func() {
				defer func() { <-slot }()
				ttl := o.At.Add(depositNetworkTTL).Sub(m.now())
				if ttl < time.Millisecond {
					return
				}
				ctx, cancel := context.WithTimeout(context.Background(), depositNetworkIOTimeout)
				defer cancel()
				if err := m.store.save(ctx, o.Chain, snapshot, ttl); err != nil {
					slog.Debug("[cryptopay] deposit network snapshot write unavailable", "chain", o.Chain)
				}
			}()
		default:
		}
	}
}

type networkScanEvidence struct {
	previous     scanner.DepositScanObservation
	advanceProof time.Time
	blockedAt    time.Time
	blockedHead  uint64
}

func (e *networkScanEvidence) observe(o scanner.DepositScanObservation) (string, time.Time) {
	if !o.CanMeasure || o.ConfirmedHead == 0 || o.CursorBefore == 0 || o.Cursor < o.CursorBefore {
		*e = networkScanEvidence{}
		return depositStatusUnknown, o.At
	}
	prev := e.previous
	gap := o.At.Sub(prev.At)
	continuous := prev.Round > 0 && o.Round == prev.Round+1 && gap > 0 && gap < depositNetworkTTL &&
		o.CursorBefore == prev.Cursor && o.ConfirmedHead >= prev.ConfirmedHead
	if !continuous {
		*e = networkScanEvidence{}
	}
	e.previous = o
	advanced := o.Cursor > o.CursorBefore
	if advanced && !o.Failed {
		e.advanceProof = o.At
		if continuous {
			e.advanceProof = prev.At
		}
	}
	if o.Failed {
		e.advanceProof = time.Time{}
	}
	behind := o.Cursor < o.ConfirmedHead
	if o.ForwardFailed && behind && !advanced && !o.NearHeadScanned {
		if e.blockedAt.IsZero() {
			e.blockedAt, e.blockedHead = o.At, o.ConfirmedHead
		}
		// Three minutes of contiguous, known failed scans with a growing
		// confirmed head. An idle DB timestamp or head probe cannot prove it.
		if o.At.Sub(e.blockedAt) >= chainStalledAfter && o.ConfirmedHead > e.blockedHead {
			return depositStatusStalled, e.blockedAt
		}
		return depositStatusDelayed, o.At
	}
	e.blockedAt, e.blockedHead = time.Time{}, 0
	if !continuous {
		return depositStatusUnknown, o.At
	}
	if o.Failed {
		// A failed pass is abnormal evidence even when the cursor did not move.
		return depositStatusDelayed, o.At
	}
	var lag uint64
	if behind {
		lag = o.ConfirmedHead - o.Cursor
	}
	switch chainTime := depositBacklogChainTime(o.Chain, lag); {
	case chainTime <= depositLagNoticeAfter:
		// The scanner is inside the normal lag of a healthy network. This is the
		// case that used to show "may be delayed" for a few dozen BSC blocks.
		proof := e.advanceProof
		if proof.IsZero() {
			proof = prev.At
		}
		return depositStatusAvailable, proof
	case chainTime <= depositLagDelayedAfter:
		// 5–30 minutes of chain time: worth a calm note, not an alarm.
		return depositStatusSlow, o.At
	default:
		return depositStatusDelayed, o.At
	}
}

type networkStatusPolicy struct {
	enabled        bool
	chains         []string
	explicitChains bool
	updatedAt      time.Time
	fingerprint    string
	// confirmations / instantCredit describe the credit rules the ETA and the
	// instant-credit column are derived from; they travel with the fingerprint,
	// so a config edit invalidates old observations instead of mixing them.
	confirmations map[string]int
	instantCredit bool
}

// requiredConfirmations returns the configured final depth for a network. A
// chain missing from the stored config falls back to the shipped default so a
// not-yet-enabled network still reports a truthful depth.
func (p networkStatusPolicy) requiredConfirmations(chain string) (int64, bool) {
	if depth, ok := p.confirmations[chain]; ok {
		return int64(depth), true
	}
	if depth, ok := DefaultCryptoConfig().Confirmations[chain]; ok {
		return int64(depth), true
	}
	return 0, false
}

func decodeNetworkStatusPolicy(raw string, updatedAt time.Time) (networkStatusPolicy, bool) {
	// Preserve explicit administrative intent. LoadConfig's first-run or invalid
	// defaults must never become a user-facing pause.
	var saved struct {
		Enabled *bool     `json:"enabled"`
		Chains  *[]string `json:"chains"`
	}
	if updatedAt.IsZero() || json.Unmarshal([]byte(raw), &saved) != nil || saved.Enabled == nil {
		return networkStatusPolicy{}, false
	}
	cfg := DefaultCryptoConfig()
	if json.Unmarshal([]byte(raw), &cfg) != nil {
		return networkStatusPolicy{}, false
	}
	cfg, err := NormalizeConfig(cfg)
	if err != nil {
		return networkStatusPolicy{}, false
	}
	fingerprint, err := configFingerprint(cfg)
	if err != nil {
		return networkStatusPolicy{}, false
	}
	return networkStatusPolicy{
		enabled:        *saved.Enabled,
		chains:         cfg.Chains,
		explicitChains: saved.Chains != nil,
		updatedAt:      updatedAt,
		fingerprint:    fingerprint,
		confirmations:  cfg.Confirmations,
		instantCredit:  cfg.InstantCreditOn(),
	}, true
}

// DepositNetworkStatus reads the site policy and shared observations, without
// consulting RPC, allocating an address, or starting background work.
func (s *Service) DepositNetworkStatus(ctx context.Context) DepositNetworkStatusReport {
	report := DepositNetworkStatusReport{Networks: make([]DepositNetworkStatusRow, len(evmChains))}
	for i, chain := range evmChains {
		report.Networks[i] = DepositNetworkStatusRow{Chain: chain, Status: depositStatusUnknown}
	}
	if s == nil || s.db == nil {
		return report
	}
	clock := time.Now
	if m := s.depositNetworkStatus; m != nil {
		clock = m.now
	}
	ctx, cancel := context.WithTimeout(ctx, depositNetworkIOTimeout)
	defer cancel()
	var raw string
	var updatedAt time.Time
	if err := s.db.QueryRowContext(ctx, `SELECT value, updated_at FROM settings WHERE key = $1`, SettingKeyCryptoConfig).Scan(&raw, &updatedAt); err != nil {
		return report
	}
	policy, ok := decodeNetworkStatusPolicy(raw, updatedAt)
	if !ok || updatedAt.After(clock()) {
		return report
	}
	var snapshots map[string]networkStatusSnapshot
	if m := s.depositNetworkStatus; policy.enabled && len(policy.chains) > 0 && m != nil && m.store != nil {
		loaded, err := m.store.load(ctx, policy.chains)
		if err == nil {
			snapshots = loaded
		}
	}
	now := clock().UTC()
	for i := range report.Networks {
		row := &report.Networks[i]
		required, requiredKnown := policy.requiredConfirmations(row.Chain)
		if requiredKnown {
			row.RequiredConfirmations = &required
		}
		row.InstantCredit = policy.instantCredit
		listed := slices.Contains(policy.chains, row.Chain)
		if !policy.enabled || (policy.explicitChains && !listed) {
			row.setStatus(depositStatusPaused, now)
			continue
		}
		if !listed {
			continue
		}
		snap := snapshots[row.Chain]
		at, fresh := snap.fresh(policy, now)
		if !fresh {
			continue
		}
		row.setStatus(snap.Status, at)
		if !snap.BacklogKnown || !requiredKnown {
			// Missing evidence stays null rather than becoming a guess.
			continue
		}
		backlog := snap.BacklogSec
		row.BacklogSeconds = &backlog
		row.EtaSeconds = depositETA(row.Chain, required, row.InstantCredit, &backlog)
	}
	return report
}

func (s networkStatusSnapshot) fresh(policy networkStatusPolicy, now time.Time) (time.Time, bool) {
	switch s.Status {
	case depositStatusAvailable, depositStatusSlow, depositStatusDelayed, depositStatusStalled:
	default:
		return time.Time{}, false
	}
	at := time.UnixMilli(s.ObservedMS).UTC()
	proof := time.UnixMilli(s.EvidenceMS)
	if (s.Status == depositStatusAvailable && !proof.Before(at)) ||
		(s.Status == depositStatusStalled && at.Sub(proof) < chainStalledAfter) {
		return time.Time{}, false
	}
	return at, s.Fingerprint == policy.fingerprint && s.EvidenceMS > 0 && !proof.After(at) &&
		!proof.Before(policy.updatedAt) && !at.After(now) && now.Before(at.Add(depositNetworkTTL))
}

func (r *DepositNetworkStatusRow) setStatus(status string, observed time.Time) {
	expires := observed.Add(depositNetworkTTL)
	r.Status, r.ObservedAt, r.ExpiresAt = status, &observed, &expires
}
