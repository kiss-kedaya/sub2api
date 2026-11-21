package cryptopay

import (
	"context"
	"encoding/json"
	"log/slog"
	"slices"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/scanner"
)

const (
	depositStatusAvailable = "available"
	depositStatusDelayed   = "delayed"
	depositStatusPaused    = "paused"
	depositStatusStalled   = "stalled"
	depositStatusUnknown   = "unknown"

	// A long or missing pass loses its evidence. Do not carry a stopped state
	// through an unobserved interval just to accommodate a slow scanner.
	depositNetworkTTL       = 2 * time.Minute
	depositNetworkIOTimeout = 350 * time.Millisecond
)

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
}

type networkStatusSnapshot struct {
	Status      string `json:"status"`
	ObservedMS  int64  `json:"at"`
	EvidenceMS  int64  `json:"since"`
	Fingerprint string `json:"config"`
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
		snapshot := networkStatusSnapshot{Status: status, ObservedMS: o.At.UnixMilli(), EvidenceMS: since.UnixMilli(), Fingerprint: fingerprint}
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
	lastAdvance  time.Time
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
		e.lastAdvance = o.At
		e.advanceProof = o.At
		if continuous {
			e.advanceProof = prev.At
		}
	}
	if o.Failed {
		e.lastAdvance, e.advanceProof = time.Time{}, time.Time{}
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
	if !o.Failed && !behind && !e.lastAdvance.IsZero() && o.At.Sub(e.lastAdvance) < depositNetworkTTL {
		return depositStatusAvailable, e.advanceProof
	}
	return depositStatusDelayed, o.At
}

type networkStatusPolicy struct {
	enabled        bool
	chains         []string
	explicitChains bool
	updatedAt      time.Time
	fingerprint    string
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
	return networkStatusPolicy{enabled: *saved.Enabled, chains: cfg.Chains, explicitChains: saved.Chains != nil, updatedAt: updatedAt, fingerprint: fingerprint}, true
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
		listed := slices.Contains(policy.chains, row.Chain)
		if !policy.enabled || (policy.explicitChains && !listed) {
			row.setStatus(depositStatusPaused, now)
		} else if listed {
			snap := snapshots[row.Chain]
			if at, fresh := snap.fresh(policy, now); fresh {
				row.setStatus(snap.Status, at)
			}
		}
	}
	return report
}

func (s networkStatusSnapshot) fresh(policy networkStatusPolicy, now time.Time) (time.Time, bool) {
	switch s.Status {
	case depositStatusAvailable, depositStatusDelayed, depositStatusStalled:
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
