package mihomo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"time"
)

type ManagedExposure struct {
	Listen       string   `json:"listen"`
	Advertise    string   `json:"advertise"`
	AllowedCIDRs []string `json:"allowed_cidrs"`
}

func (m *Manager) HostID() string {
	host, _ := os.Hostname()
	dir, _ := filepath.Abs(m.dir)
	sum := sha256.Sum256([]byte(host + "\x00" + dir))
	return hex.EncodeToString(sum[:12])
}

func validateManagedExposure(e ManagedExposure) error {
	if e.Listen == "" && e.Advertise == "" && len(e.AllowedCIDRs) == 0 {
		return nil
	}
	ip, err := netip.ParseAddr(e.Listen)
	if err != nil || !ip.IsPrivate() || ip.IsLoopback() || ip.Zone() != "" || e.Advertise != e.Listen {
		return errors.New("listener and advertised address must be the same explicit private IP")
	}
	if len(e.AllowedCIDRs) == 0 || len(e.AllowedCIDRs) > 64 {
		return errors.New("allow between 1 and 64 private source CIDRs")
	}
	for _, raw := range e.AllowedCIDRs {
		p, err := netip.ParsePrefix(raw)
		if err != nil || !p.Addr().IsPrivate() || p != p.Masked() {
			return errors.New("source CIDRs must be canonical private networks")
		}
		inside := false
		for _, private := range []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "fc00::/7"} {
			allowed := netip.MustParsePrefix(private)
			if allowed.Contains(p.Addr()) && p.Bits() >= allowed.Bits() {
				inside = true
			}
		}
		if !inside {
			return errors.New("public source networks are not permitted")
		}
	}
	return nil
}

func (m *Manager) SetExposure(ctx context.Context, nextExposure ManagedExposure) error {
	if err := validateManagedExposure(nextExposure); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	m.mu.Lock()
	if m.closed || m.state.Busy {
		m.mu.Unlock()
		return errors.New("managed kernel is busy")
	}
	for _, binding := range m.saved.StaticBindings {
		if binding.Enabled {
			m.mu.Unlock()
			return errors.New("remove active static proxy bindings before changing network settings")
		}
	}
	m.state.Busy = true
	m.wg.Add(1)
	m.mu.Unlock()
	defer m.wg.Done()
	defer m.clearStaticBindingBusy()
	if err := m.acquire(ctx); err != nil {
		return err
	}
	defer m.release()
	m.mu.Lock()
	next := m.saved
	m.mu.Unlock()
	next.Exposure = nextExposure
	raw, err := json.Marshal(next)
	if err != nil {
		return errors.New("cannot encode network settings")
	}
	if err = atomicWrite(filepath.Join(m.dir, "settings.json"), raw, 0600); err != nil {
		return errors.New("cannot save network settings")
	}
	m.mu.Lock()
	m.saved = next
	m.mu.Unlock()
	return nil
}
