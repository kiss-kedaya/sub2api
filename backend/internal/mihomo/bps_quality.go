package mihomo

import (
	"math"
	"time"
)

const bpsQualityHalfLife = 30 * time.Minute

// Bounded, decaying observations keep recent reliability relevant without
// treating a new node as perfect or permanently condemning a recovered node.
// All access is protected by bpsMu. Priors add three successes in four samples.
type bpsQualityRate struct {
	successes float64
	samples   float64
	updated   time.Time
}

func (r bpsQualityRate) decayed(now time.Time) (float64, float64) {
	factor := 1.0
	if !r.updated.IsZero() && now.After(r.updated) {
		factor = math.Exp2(-now.Sub(r.updated).Seconds() / bpsQualityHalfLife.Seconds())
	}
	return r.successes * factor, r.samples * factor
}

func (r *bpsQualityRate) observe(success bool, now time.Time) {
	r.successes, r.samples = r.decayed(now)
	if r.samples >= 63 {
		factor := 63 / r.samples
		r.samples *= factor
		r.successes *= factor
	}
	r.samples++
	if success {
		r.successes++
	}
	r.updated = now
}

func (r bpsQualityRate) rate(now time.Time) float64 {
	successes, samples := r.decayed(now)
	return (successes + 3) / (samples + 4)
}

// Completed model requests dominate short connectivity checks. Active requests
// carry a larger load penalty than idle pinned sessions; affinity is retained
// separately and only new/rebound sessions use this ranking.
func (m *Manager) bpsQualityScoreLocked(node string, active, sessions int, now time.Time) float64 {
	var model, connect bpsQualityRate
	h := m.bpsHealthAtLocked(node, now)
	model, connect = h.modelQuality, h.connectQuality
	quality := 0.7*model.rate(now) + 0.3*connect.rate(now)
	return quality / (1 + 0.15*float64(active) + 0.02*float64(sessions))
}

// ReportSuccess records a complete response, not merely HTTP 200 or headers.
// Reporting a result twice, including after a failure, cannot inflate quality.
func (l *BPSLease) ReportSuccess() {
	l.failureOnce.Do(func() {
		l.manager.bpsMu.Lock()
		defer l.manager.bpsMu.Unlock()
		now := time.Now()
		if h := l.feedbackHealthLocked(now); h != nil {
			h.modelQuality.observe(true, now)
		}
	})
}

// HTTP 5xx affects the outcome score but does not prove a broken proxy tunnel.
// Authentication, quota, input errors and client cancellation are excluded.
func (l *BPSLease) ReportUpstreamFailure() {
	l.failureOnce.Do(func() {
		l.manager.bpsMu.Lock()
		defer l.manager.bpsMu.Unlock()
		now := time.Now()
		if h := l.feedbackHealthLocked(now); h != nil {
			h.modelQuality.observe(false, now)
		}
	})
}
