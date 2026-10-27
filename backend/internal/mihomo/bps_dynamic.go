package mihomo

import "time"

const (
	// Bounds the age of observations for the owner's 20-minute rotating pool.
	// This is a local reassessment window, NOT the supplier's rotation clock.
	bpsDynamicWindow         = 20 * time.Minute
	bpsDynamicStreamCooldown = 30 * time.Second
	bpsDynamicMaxCooldown    = 2 * time.Minute
)

// Call with bpsMu held. A dynamic endpoint/credential can stay unchanged while
// its exit rotates. Do not carry old observations indefinitely into later exits.
// Keep the health object and pending probe: waiters must still be released.
func (m *Manager) bpsHealthAtLocked(node string, now time.Time) *bpsNodeHealth {
	h := m.bpsHealthLocked(node)
	if !m.bpsDynamic[node] {
		return h
	}
	if h.windowStarted.IsZero() || now.Sub(h.windowStarted) >= bpsDynamicWindow {
		h.windowStarted = now
		h.generation++
		h.revision++
		h.modelQuality = bpsQualityRate{}
		h.connectQuality = bpsQualityRate{}
		h.verifiedUntil = time.Time{}
		h.retryAfter = time.Time{}
		h.failures = 0
		h.streamFailures = 0
		h.lastStreamFailure = time.Time{}
	}
	return h
}

// Results from a previous dynamic window cannot quarantine or reward a new
// window, including requests that finish after a long-running response closes.
func (l *BPSLease) feedbackHealthLocked(now time.Time) *bpsNodeHealth {
	h := l.manager.bpsHealthAtLocked(l.node, now)
	if h.generation != l.generation {
		return nil
	}
	return h
}
