package mihomo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func (m *Manager) readManagedSettings() (saved, error) {
	var s saved
	raw, err := os.ReadFile(filepath.Join(m.dir, "settings.json"))
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil || json.Unmarshal(raw, &s) != nil {
		return s, errors.New("managed settings cannot be read")
	}
	return s, nil
}

func (m *Manager) refreshManagedStatus() {
	if !m.managed {
		return
	}
	m.mu.Lock()
	if m.state.Busy || m.closed {
		m.mu.Unlock()
		return
	}
	s, err := m.readManagedSettings()
	if err == nil {
		m.saved = s
	}
	m.mu.Unlock()
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	running := s.Secret != "" && m.control(ctx, http.MethodGet, "/version", s.Secret, nil) == nil
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.state.Busy {
		m.state.Running = running
		if running {
			m.state.Phase = "running"
		}
		if fi, e := os.Stat(filepath.Join(m.dir, "mihomo")); e == nil {
			m.state.Installed = fi.Mode().Perm()&0111 != 0
		}
	}
}

func (m *Manager) lockConfiguration(ctx context.Context) (func(), error) {
	if err := os.MkdirAll(m.dir, 0700); err != nil {
		return nil, errors.New("managed directory unavailable")
	}
	unlock, err := lockHostConfiguration(ctx, filepath.Join(m.dir, "reload.lock"))
	if err != nil {
		return nil, err
	}
	next, err := m.readManagedSettings()
	if err != nil {
		unlock()
		return nil, err
	}
	m.mu.Lock()
	oldRaw, _ := json.Marshal(m.saved)
	newRaw, _ := json.Marshal(next)
	m.saved = next
	m.mu.Unlock()
	if !bytes.Equal(oldRaw, newRaw) {
		unlock()
		return nil, errors.New("managed configuration changed in another instance; refresh and retry")
	}
	running := next.Secret != "" && m.control(ctx, http.MethodGet, "/version", next.Secret, nil) == nil
	m.mu.Lock()
	m.state.Running = running
	m.mu.Unlock()
	return unlock, nil
}

func writeKernelOwner(dir string, pid int) error {
	return atomicWrite(filepath.Join(dir, "owner.pid"), []byte(strconv.Itoa(pid)), 0600)
}

// stopManaged is only reachable from an explicit management operation. Rolling
// application shutdown neither signals nor transfers ownership of the kernel.
func (m *Manager) stopManaged(ctx context.Context) error {
	if !m.managed {
		m.stop()
		return nil
	}
	m.mu.Lock()
	secret := m.saved.Secret
	m.mu.Unlock()
	if secret == "" || m.control(ctx, http.MethodGet, "/version", secret, nil) != nil {
		return errors.New("cannot verify managed kernel ownership")
	}
	raw, err := os.ReadFile(filepath.Join(m.dir, "owner.pid"))
	if err != nil {
		return errors.New("managed owner record unavailable")
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 1 {
		return errors.New("invalid managed owner")
	}
	if err := terminateManagedKernel(m.dir, pid); err != nil {
		return err
	}
	for {
		if m.control(ctx, http.MethodGet, "/version", secret, nil) != nil {
			m.mu.Lock()
			m.state.Running = false
			m.state.Phase = "stopped"
			m.mu.Unlock()
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.New("kernel stop is still draining")
		case <-time.After(100 * time.Millisecond):
		}
	}
}
