package requestcapture

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

type runtimeTask struct {
	task   Task
	refs   int
	dirty  bool
	active atomic.Bool
}
type Manager struct {
	store            Store
	dir, instance    string
	mu               sync.Mutex
	adminMu          sync.Mutex
	config           Config
	tasks            map[string]*runtimeTask
	sessions         map[*Session]struct{}
	queue            chan event
	wake             chan struct{}
	done             chan struct{}
	closed           chan struct{}
	closeOnce        sync.Once
	enabled          atomic.Bool
	admissionSkipped atomic.Int64
	buffer           atomic.Int64
	peak             atomic.Int64
	used             atomic.Int64
	exportMu         sync.RWMutex
	exports          chan struct{}
	storageMu        sync.Mutex
	unhealthy        atomic.Bool
	stopping         atomic.Bool
}

func New(store Store, dir string, config Config) (*Manager, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return nil, err
	}
	instanceFile := filepath.Join(dir, ".instance")
	if info, e := os.Lstat(instanceFile); e == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("symlink capture identity")
	}
	raw, err := os.ReadFile(instanceFile)
	if errors.Is(err, os.ErrNotExist) {
		raw = []byte(uuid.NewString())
		err = os.WriteFile(instanceFile, raw, 0600)
	}
	if err != nil {
		return nil, err
	}
	if _, err = uuid.Parse(string(raw)); err != nil {
		return nil, fmt.Errorf("invalid capture instance identity: %w", err)
	}
	m := &Manager{store: store, dir: dir, instance: string(raw), config: config, tasks: map[string]*runtimeTask{}, sessions: map[*Session]struct{}{}, exports: make(chan struct{}, 2), queue: make(chan event, 1024), wake: make(chan struct{}, 1), done: make(chan struct{}), closed: make(chan struct{})}
	// Reserve fixed worker scratch before admitting payload copies.
	m.buffer.Store(1 << 20)
	m.enabled.Store(config.Enabled)
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return errors.New("symlink in capture directory")
		}
		if !d.IsDir() && d.Name() != ".instance" {
			info, e := d.Info()
			if e != nil {
				return e
			}
			m.used.Add(info.Size())
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for offset := 0; ; offset += 100 {
		tasks, e := store.Tasks(ctx, m.instance, 100, offset)
		if e != nil {
			return nil, e
		}
		for _, t := range tasks {
			if t.Status == "running" {
				now := time.Now().UTC()
				t.Status = "interrupted"
				t.Reason = "server_restart"
				t.EndedAt = &now
				if e = store.SaveTask(ctx, &t); e != nil {
					return nil, e
				}
			}
			var requests, partial, bytes int64
			for ro := 0; ; {
				records, e := store.Records(ctx, t.ID, "", false, 100, ro)
				if e != nil {
					return nil, e
				}
				kept := 0
				for _, r := range records {
					if !r.IsError || r.FinishedAt == nil {
						if _, e = uuid.Parse(r.ID); e != nil {
							return nil, e
						}
						if e = os.RemoveAll(filepath.Join(dir, t.ID, r.ID)); e != nil {
							return nil, e
						}
						if e = store.DeleteRecord(ctx, t.ID, r.ID); e != nil {
							return nil, e
						}
						continue
					}
					kept++
					requests++
					bytes += r.Bytes
					if r.Partial {
						partial++
					}
				}
				if len(records) < 100 {
					break
				}
				ro += kept
			}
			// A crash can occur between record persistence and task counter flush.
			if t.Requests != requests || t.Partial != partial || t.Bytes != bytes {
				t.Requests, t.Partial, t.Bytes = requests, partial, bytes
				if e = store.SaveTask(ctx, &t); e != nil {
					return nil, e
				}
			}
		}
		if len(tasks) < 100 {
			break
		}
	}
	// Remove bodies whose final error index was never committed, including crashes
	// between a successful response and its asynchronous deletion.
	if err = m.cleanOrphanBodies(ctx); err != nil {
		return nil, err
	}
	m.used.Store(0)
	if err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && d.Name() != ".instance" {
			info, err := d.Info()
			if err != nil {
				return err
			}
			m.used.Add(info.Size())
		}
		return nil
	}); err != nil {
		return nil, err
	}
	go m.run()
	return m, nil
}
func (m *Manager) InstanceID() string { return m.instance }
func (m *Manager) Enabled() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.config.Enabled && !m.unhealthy.Load()
}
func (m *Manager) Config() Config { m.mu.Lock(); defer m.mu.Unlock(); return m.config }
func (m *Manager) ApplyConfig(c Config) {
	if c.Validate() != nil {
		return
	}
	m.mu.Lock()
	m.config = c
	m.enabled.Store(c.Enabled)
	reason := ""
	if !c.Enabled {
		reason = "feature_disabled"
	} else if m.used.Load() >= c.QuotaMiB<<20 {
		reason = "quota_exceeded"
	}
	if reason != "" {
		for _, t := range m.tasks {
			m.stopLocked(t, reason)
		}
	}
	m.mu.Unlock()
	m.signal()
}
func (m *Manager) reserve(n int64) bool {
	for {
		old := m.buffer.Load()
		if n < 0 || old+n > BufferLimit {
			return false
		}
		if m.buffer.CompareAndSwap(old, old+n) {
			for peak := m.peak.Load(); old+n > peak; peak = m.peak.Load() {
				if m.peak.CompareAndSwap(peak, old+n) {
					break
				}
			}
			return true
		}
	}
}
func (m *Manager) signal() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}
func (m *Manager) stopLocked(t *runtimeTask, reason string) {
	if t.task.Status != "running" {
		return
	}
	t.active.Store(false)
	now := time.Now().UTC()
	t.task.EndedAt = &now
	t.task.Status = "stopped"
	if reason == "expired" {
		t.task.Status = "completed"
	}
	t.task.Reason = reason
	t.dirty = true
}
func (m *Manager) stopAll(reason string) {
	m.mu.Lock()
	for _, t := range m.tasks {
		m.stopLocked(t, reason)
	}
	m.mu.Unlock()
	m.signal()
}

func (m *Manager) Create(ctx context.Context, req CreateTask, name string) (*Task, error) {
	if req.TargetID <= 0 || req.DurationMinutes < 1 || req.DurationMinutes > 1440 {
		return nil, errors.New("invalid target or capture duration (1-1440 minutes)")
	}
	if req.TargetType != "user" && req.TargetType != "account" && req.TargetType != "group" {
		return nil, errors.New("invalid capture target type")
	}
	m.adminMu.Lock()
	defer m.adminMu.Unlock()
	m.mu.Lock()
	enabled := m.config.Enabled
	count := 0
	for _, t := range m.tasks {
		if t.task.Status == "running" {
			count++
		}
	}
	quota := m.config.QuotaMiB << 20
	m.mu.Unlock()
	if !enabled {
		return nil, ErrDisabled
	}
	if m.unhealthy.Load() {
		return nil, errors.New("capture storage unavailable")
	}
	if count >= MaxTasks || m.used.Load() >= quota {
		return nil, ErrCapacity
	}
	if len(name) > 256 {
		name = name[:256]
	}
	now := time.Now().UTC()
	t := Task{ID: uuid.NewString(), InstanceID: m.instance, TargetType: req.TargetType, TargetID: req.TargetID, TargetName: name, SaveMedia: req.SaveMedia, CreatedAt: now, ExpiresAt: now.Add(time.Duration(req.DurationMinutes) * time.Minute), Status: "running"}
	if err := m.store.SaveTask(ctx, &t); err != nil {
		return nil, err
	}
	m.mu.Lock()
	rt := &runtimeTask{task: t}
	rt.active.Store(true)
	m.tasks[t.ID] = rt
	if !m.config.Enabled {
		m.stopLocked(rt, "feature_disabled")
	}
	t = rt.task
	m.mu.Unlock()
	return &t, nil
}
func (m *Manager) Tasks(ctx context.Context, limit, offset int) ([]Task, error) {
	tasks, err := m.store.Tasks(ctx, m.instance, limit, offset)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range tasks {
		if t := m.tasks[tasks[i].ID]; t != nil {
			tasks[i] = t.task
		}
	}
	return tasks, nil
}
func (m *Manager) Task(ctx context.Context, id string) (*Task, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, ErrNotFound
	}
	m.mu.Lock()
	if t := m.tasks[id]; t != nil {
		v := t.task
		m.mu.Unlock()
		return &v, nil
	}
	m.mu.Unlock()
	return m.store.Task(ctx, m.instance, id)
}
func (m *Manager) Stop(ctx context.Context, id string) error {
	if _, err := m.Task(ctx, id); err != nil {
		return err
	}
	m.mu.Lock()
	if t := m.tasks[id]; t != nil {
		m.stopLocked(t, "manual_stop")
	}
	m.mu.Unlock()
	m.signal()
	return nil
}
func (m *Manager) Records(ctx context.Context, task, rid string, _ bool, limit, offset int) ([]Record, error) {
	if _, err := m.Task(ctx, task); err != nil {
		return nil, err
	}
	return m.store.Records(ctx, task, rid, true, limit, offset)
}
func (m *Manager) Record(ctx context.Context, task, id string) (*Record, error) {
	if _, err := m.Task(ctx, task); err != nil {
		return nil, err
	}
	r, err := m.store.Record(ctx, task, id)
	if err != nil {
		return nil, err
	}
	if r.InstanceID != m.instance || !r.IsError || r.FinishedAt == nil {
		return nil, ErrNotFound
	}
	return r, nil
}

func (m *Manager) Delete(ctx context.Context, id string) error {
	m.adminMu.Lock()
	defer m.adminMu.Unlock()
	m.exportMu.Lock()
	defer m.exportMu.Unlock()
	if _, err := m.Task(ctx, id); err != nil {
		return err
	}
	m.mu.Lock()
	rt := m.tasks[id]
	if rt != nil {
		m.stopLocked(rt, "deleted")
	}
	m.mu.Unlock()
	m.signal()
	// The worker owns all file writes. A stopped task cannot accept new bytes.
	m.storageMu.Lock()
	defer m.storageMu.Unlock()
	var bytes int64
	dir := filepath.Join(m.dir, id)
	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if !d.IsDir() {
			i, e := d.Info()
			if e != nil {
				return e
			}
			bytes += i.Size()
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err = os.RemoveAll(dir); err != nil {
		return err
	}
	m.used.Add(-bytes)
	if err = m.store.DeleteTask(ctx, m.instance, id); err != nil {
		return err
	}
	m.mu.Lock()
	delete(m.tasks, id)
	m.mu.Unlock()
	return nil
}
func (m *Manager) Close() {
	if m == nil {
		return
	}
	m.closeOnce.Do(func() { m.stopping.Store(true); m.stopAll("server_shutdown"); close(m.done) })
	<-m.closed
}

type Stats struct {
	InstanceID       string `json:"instance_id"`
	UsedBytes        int64  `json:"used_bytes"`
	BufferBytes      int64  `json:"buffer_bytes"`
	PeakBufferBytes  int64  `json:"peak_buffer_bytes"`
	ActiveRequests   int    `json:"active_requests"`
	AdmissionSkipped int64  `json:"admission_skipped"`
	StorageError     bool   `json:"storage_error"`
}

func (m *Manager) Stats() Stats {
	m.mu.Lock()
	n := len(m.sessions)
	m.mu.Unlock()
	return Stats{m.instance, m.used.Load(), m.buffer.Load(), m.peak.Load(), n, m.admissionSkipped.Load(), m.unhealthy.Load()}
}

func (m *Manager) run() {
	defer close(m.closed)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	cleanup := time.NewTicker(time.Minute)
	defer cleanup.Stop()
	for {
		select {
		case e := <-m.queue:
			m.process(e)
		case <-m.wake:
			m.maintenance()
		case <-ticker.C:
			m.maintenance()
		case <-cleanup.C:
			m.cleanExpired()
		case <-m.done:
			for {
				select {
				case e := <-m.queue:
					m.process(e)
				default:
					m.mu.Lock()
					for s := range m.sessions {
						s.done.Store(true)
					}
					m.mu.Unlock()
					m.maintenance()
					return
				}
			}
		}
	}
}
func (m *Manager) maintenance() {
	now := time.Now().UTC()
	m.mu.Lock()
	sessions := make([]*Session, 0, len(m.sessions))
	for s := range m.sessions {
		sessions = append(sessions, s)
	}
	for _, t := range m.tasks {
		if t.task.Status == "running" && !now.Before(t.task.ExpiresAt) {
			m.stopLocked(t, "expired")
		}
	}
	m.mu.Unlock()
	for _, s := range sessions {
		for _, t := range s.candidates {
			if !t.active.Load() && s.pending.Load() == 0 {
				m.finishTarget(s, t)
			}
		}
		m.mu.Lock()
		allStopped := true
		for _, t := range s.candidates {
			if t.task.Status == "running" {
				allStopped = false
			}
		}
		m.mu.Unlock()
		s.mu.Lock()
		allFinished := len(s.finishedTargets) == len(s.candidates)
		failed := s.failed
		s.mu.Unlock()
		if (s.done.Load() || allStopped || allFinished || failed) && s.pending.Load() == 0 {
			m.finish(s)
		}
	}
	m.mu.Lock()
	type save struct {
		id string
		t  Task
	}
	todo := []save{}
	for id, t := range m.tasks {
		if t.dirty {
			todo = append(todo, save{id, t.task})
			t.dirty = false
		}
	}
	m.mu.Unlock()
	for _, v := range todo {
		m.adminMu.Lock()
		m.mu.Lock()
		exists := m.tasks[v.id] != nil
		m.mu.Unlock()
		if !exists {
			m.adminMu.Unlock()
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		err := m.store.SaveTask(ctx, &v.t)
		cancel()
		m.mu.Lock()
		if t := m.tasks[v.id]; t != nil {
			if err != nil {
				t.dirty = true
			} else if t.task.Status != "running" && t.refs == 0 && !t.dirty {
				delete(m.tasks, v.id)
			}
		}
		m.mu.Unlock()
		if err != nil {
			m.unhealthy.Store(true)
			m.stopAll("index_write_failed")
		}
		m.adminMu.Unlock()
		if err != nil {
			break
		}
	}
}
func (m *Manager) cleanExpired() {
	retention := m.Config().RetentionDays
	cutoff := time.Now().Add(-time.Duration(retention) * 24 * time.Hour)
	// Re-read the page after deletion so offsets cannot skip expired tasks.
	for offset := 0; ; {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		tasks, err := m.Tasks(ctx, 100, offset)
		cancel()
		if err != nil {
			return
		}
		deleted := 0
		for _, t := range tasks {
			if t.EndedAt != nil && t.EndedAt.Before(cutoff) {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				err = m.Delete(ctx, t.ID)
				cancel()
				if err == nil {
					deleted++
				}
			}
		}
		if len(tasks) < 100 {
			return
		}
		offset += len(tasks) - deleted
	}
}

// Recovery is fail-closed: only a finished error index authorizes retained files.
func (m *Manager) cleanOrphanBodies(ctx context.Context) error {
	tasks, err := os.ReadDir(m.dir)
	if err != nil {
		return err
	}
	for _, task := range tasks {
		if !task.IsDir() {
			continue
		}
		if _, err := uuid.Parse(task.Name()); err != nil {
			return err
		}
		records, err := os.ReadDir(filepath.Join(m.dir, task.Name()))
		if err != nil {
			return err
		}
		for _, record := range records {
			if !record.IsDir() {
				continue
			}
			if _, err := uuid.Parse(record.Name()); err != nil {
				return err
			}
			r, err := m.store.Record(ctx, task.Name(), record.Name())
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if err != nil || r.InstanceID != m.instance || !r.IsError || r.FinishedAt == nil {
				if err := os.RemoveAll(filepath.Join(m.dir, task.Name(), record.Name())); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
