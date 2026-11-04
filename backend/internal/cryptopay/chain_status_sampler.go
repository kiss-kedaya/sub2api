package cryptopay

import "time"

// sampleRing keeps the newest chainSampleCapacity observations of one chain.
// It lives in process memory only: a restart starts with an empty window, and
// the page then says "calculating" instead of guessing.
type sampleRing struct {
	buf   [chainSampleCapacity]chainSample
	start int
	size  int
}

func (r *sampleRing) push(s chainSample) {
	if r.size < len(r.buf) {
		r.buf[(r.start+r.size)%len(r.buf)] = s
		r.size++
		return
	}
	r.buf[r.start] = s
	r.start = (r.start + 1) % len(r.buf)
}

func (r *sampleRing) last() (chainSample, bool) {
	if r.size == 0 {
		return chainSample{}, false
	}
	return r.buf[(r.start+r.size-1)%len(r.buf)], true
}

// since returns the samples taken at or after cutoff, oldest first.
func (r *sampleRing) since(cutoff time.Time) []chainSample {
	out := make([]chainSample, 0, r.size)
	for i := 0; i < r.size; i++ {
		s := r.buf[(r.start+i)%len(r.buf)]
		if !s.At.Before(cutoff) {
			out = append(out, s)
		}
	}
	return out
}

func (r *sampleRing) reset() { r.start, r.size = 0, 0 }

// record stores one observation. Requests and the background ticker both call
// it, so observations closer than chainSampleMinSpacing are dropped to keep the
// window evenly spaced however often the page polls. A cursor that moved
// backwards (an operator rewound it for a rescan) or a clock that moved
// backwards makes the old baseline meaningless, so the window starts over.
func (m *chainMonitor) record(chain string, cursor, head uint64) {
	m.init()
	now := m.now()
	m.mu.Lock()
	defer m.mu.Unlock()
	ring := m.rings[chain]
	if ring == nil {
		ring = &sampleRing{}
		m.rings[chain] = ring
	}
	if prev, ok := ring.last(); ok {
		switch {
		case cursor < prev.Cursor, now.Before(prev.At):
			ring.reset()
		case now.Sub(prev.At) < chainSampleMinSpacing:
			return
		}
	}
	ring.push(chainSample{At: now, Cursor: cursor, Head: head})
}

// window returns the retained samples of a chain, oldest first.
func (m *chainMonitor) window(chain string) []chainSample {
	m.init()
	cutoff := m.now().Add(-chainSampleRetention)
	m.mu.Lock()
	defer m.mu.Unlock()
	ring := m.rings[chain]
	if ring == nil {
		return nil
	}
	return ring.since(cutoff)
}

// cursorMissingFor tracks how long an enabled chain has had no cursor row. The
// first call that sees the row missing starts the clock (and returns 0); a call
// that sees the row clears it. A chain whose scanner never wrote a cursor would
// otherwise look like it was merely catching up forever.
func (m *chainMonitor) cursorMissingFor(chain string, present bool) time.Duration {
	m.init()
	now := m.now()
	m.mu.Lock()
	defer m.mu.Unlock()
	if present {
		delete(m.missing, chain)
		return 0
	}
	since, seen := m.missing[chain]
	if !seen {
		m.missing[chain] = now
		return 0
	}
	if d := now.Sub(since); d > 0 {
		return d
	}
	return 0
}
