package requestcapture

import (
	"container/heap"
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

const ID1DiagnosticMode = "id1_diagnostic"
const ID1DiagnosticRetention = 8 * time.Hour
const ID1DiagnosticRecordLimit = 100000

type captureExpiry struct {
	at        time.Time
	task, id  string
	record    *recordState
	persisted bool
	bytes     int64
	partial   bool
}
type captureExpiryHeap []*captureExpiry

func (h captureExpiryHeap) Len() int           { return len(h) }
func (h captureExpiryHeap) Less(i, j int) bool { return h[i].at.Before(h[j].at) }
func (h captureExpiryHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *captureExpiryHeap) Push(x any) {
	v, ok := x.(*captureExpiry)
	if ok {
		*h = append(*h, v)
	}
}
func (h *captureExpiryHeap) Pop() any {
	old := *h
	v := old[len(old)-1]
	old[len(old)-1] = nil
	*h = old[:len(old)-1]
	return v
}
func recordExpired(r *Record, now time.Time) bool {
	return r != nil && r.ExpiresAt != nil && !now.Before(*r.ExpiresAt)
}

func (m *Manager) noteSkipped(reason string) {
	value, _ := m.skippedByReason.LoadOrStore(reason, &atomic.Int64{})
	if count, ok := value.(*atomic.Int64); ok {
		count.Add(1)
	}
}

// Called only by startup/the single capture worker. Every record is scheduled
// from its first captured byte, so an indefinitely active stream cannot extend
// retention. Deadlines are independent of the continuous task's lifetime.
func (m *Manager) scheduleExpiry(r *Record, state *recordState) bool {
	if r.ExpiresAt == nil {
		return true
	}
	if len(m.expirations) >= ID1DiagnosticRecordLimit {
		m.noteSkipped("record_capacity")
		return false
	}
	entry := &captureExpiry{at: *r.ExpiresAt, task: r.TaskID, id: r.ID, record: state}
	if state == nil {
		entry.persisted = true
		entry.bytes = r.Bytes
		entry.partial = r.Partial
	}
	if state != nil {
		state.expiry = entry
	}
	heap.Push(&m.expirations, entry)
	return true
}
func (m *Manager) prepareDiagnosticRecord(r *recordState, t *runtimeTask) bool {
	if t.task.Mode != ID1DiagnosticMode {
		return true
	}
	expires := r.CreatedAt.Add(ID1DiagnosticRetention)
	r.ExpiresAt = &expires
	return m.scheduleExpiry(&r.Record, r)
}
func (m *Manager) expireDue(now time.Time) {
	for len(m.expirations) > 0 && !now.Before(m.expirations[0].at) {
		value := heap.Pop(&m.expirations)
		entry, ok := value.(*captureExpiry)
		if !ok {
			continue
		}
		if r := entry.record; r != nil {
			r.expired = true
			r.blocked = true
			for _, p := range r.parts {
				m.buffer.Add(-p.charge)
				p.charge = 0
				p.filter = nil
			}
			if s := r.owner; s != nil {
				s.mu.Lock()
				diagnosticOnly := true
				for _, t := range s.candidates {
					if t.task.Mode != ID1DiagnosticMode {
						diagnosticOnly = false
					}
				}
				var streams []*Stream
				if diagnosticOnly && s.turn == r.Turn {
					s.expiredTurn = true
					s.reason = "retention_expired"
					s.releaseInputLocked()
					for st := range s.streams {
						if st.part.Turn == r.Turn {
							streams = append(streams, st)
						}
					}
				}
				s.mu.Unlock()
				for _, st := range streams {
					st.discard()
				}
			}
		}
		m.storageMu.Lock()
		dir := filepath.Join(m.dir, entry.task, entry.id)
		var size int64
		_ = filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if !d.IsDir() {
				if info, e := d.Info(); e == nil {
					size += info.Size()
				}
			}
			return nil
		})
		err := os.RemoveAll(dir)
		if err == nil {
			m.used.Add(-size)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		if err == nil {
			err = m.store.DeleteRecord(ctx, entry.task, entry.id)
		}
		cancel()
		m.storageMu.Unlock()
		if err != nil {
			m.unhealthy.Store(true)
			m.stopAll("privacy_cleanup_failed")
			entry.at = now.Add(time.Second)
			heap.Push(&m.expirations, entry)
			return
		}
		m.expiredRecords.Add(1)
		if entry.persisted {
			m.mu.Lock()
			if t := m.tasks[entry.task]; t != nil {
				t.task.Requests = max(t.task.Requests-1, 0)
				t.task.Bytes = max(t.task.Bytes-entry.bytes, 0)
				if entry.partial {
					t.task.Partial = max(t.task.Partial-1, 0)
				}
				t.dirty = true
			}
			m.mu.Unlock()
		}
	}
}

func (m *Manager) syncID1Diagnostic(ctx context.Context) error {
	m.mu.Lock()
	enabled := m.config.ID1DiagnosticEnabled
	var current *runtimeTask
	for _, t := range m.tasks {
		if t.task.Mode == ID1DiagnosticMode {
			current = t
			break
		}
	}
	if current != nil {
		if enabled && !m.unhealthy.Load() && !m.stopping.Load() && m.used.Load() < m.config.QuotaMiB<<20 && current.task.Status != "running" {
			current.task.Status = "running"
			current.task.Reason = ""
			current.task.EndedAt = nil
			current.active.Store(true)
			current.dirty = true
		}
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()
	if !enabled || len(m.config.DiagnosticTargets()) == 0 || m.unhealthy.Load() || m.stopping.Load() {
		return nil
	}
	now := time.Now().UTC()
	t := Task{ID: uuid.NewString(), Mode: ID1DiagnosticMode, InstanceID: m.instance, TargetType: "user", TargetID: 1, TargetName: "ID1 continuous diagnostics", SaveMedia: true, CreatedAt: now, Status: "running"}
	if err := m.store.SaveTask(ctx, &t); err != nil {
		return err
	}
	rt := &runtimeTask{task: t}
	rt.active.Store(true)
	m.mu.Lock()
	m.tasks[t.ID] = rt
	if !m.config.ID1DiagnosticEnabled || m.unhealthy.Load() || m.stopping.Load() {
		m.stopLocked(rt, "feature_disabled")
	}
	m.mu.Unlock()
	return nil
}
