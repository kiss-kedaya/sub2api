package mihomo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"gopkg.in/yaml.v3"
	"os"
	"path/filepath"
	"time"
)

// Download and validate one subscription before accepting its chosen route.
// Only auto mode changes download routes, including on empty/non-Clash bodies.
func (m *Manager) downloadSubscriptionNodes(ctx context.Context, address string, mode SubscriptionDownloadMode, proxy string) ([]map[string]any, error) {
	fetch := func(route string) ([]map[string]any, error) {
		raw, err := m.getViaProxy(ctx, address, 4<<20, "clash.meta", route)
		if err != nil {
			return nil, err
		}
		if len(raw) == 0 {
			return nil, errors.New("empty subscription response")
		}
		var doc struct{ Proxies []map[string]any }
		if yaml.Unmarshal(raw, &doc) != nil || len(doc.Proxies) == 0 {
			return nil, errors.New("subscription must contain Clash/Mihomo YAML proxies")
		}
		return doc.Proxies, nil
	}
	normalized, modeErr := normalizeSubscriptionDownloadMode(mode)
	if modeErr != nil {
		return nil, modeErr
	}
	mode = normalized
	if mode == SubscriptionDownloadProxy && proxy == "" {
		return nil, errors.New("subscription proxy is unavailable")
	}
	if mode == SubscriptionDownloadDirect || proxy == "" {
		return fetch("")
	}
	nodes, err := fetch(proxy)
	if err == nil || mode == SubscriptionDownloadProxy || ctx.Err() != nil {
		return nodes, err
	}
	nodes, directErr := fetch("")
	if directErr != nil {
		return nil, fmt.Errorf("subscription download failed via proxy (%v) and direct (%v)", err, directErr)
	}
	return nodes, nil
}

// SetSubscriptionDownloadMode updates only persistent downloader settings.
// It never fetches a subscription, restarts Mihomo or alters account routing.
func (m *Manager) SetSubscriptionDownloadMode(ctx context.Context, mode SubscriptionDownloadMode) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	mode, err := normalizeSubscriptionDownloadMode(mode)
	if err != nil {
		return err
	}
	m.mu.Lock()
	if m.closed || m.state.Busy {
		m.mu.Unlock()
		return errors.New("another operation is running or service is stopping")
	}
	m.state.Busy = true
	m.wg.Add(1)
	m.mu.Unlock()
	defer m.wg.Done()
	defer func() { m.mu.Lock(); m.state.Busy = false; m.mu.Unlock() }()
	if err = m.acquire(ctx); err != nil {
		return err
	}
	defer m.release()
	m.mu.Lock()
	next := m.saved
	closed := m.closed
	m.mu.Unlock()
	if closed {
		return errors.New("service is stopping")
	}
	next.DownloadMode = mode
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = os.MkdirAll(m.dir, 0700); err != nil {
		return errors.New("cannot prepare download settings")
	}
	raw, err := json.Marshal(next)
	if err != nil {
		return errors.New("cannot encode download settings")
	}
	if err = atomicWrite(filepath.Join(m.dir, "settings.json"), raw, 0600); err != nil {
		return errors.New("cannot save download settings")
	}
	m.mu.Lock()
	m.saved = next
	m.mu.Unlock()
	return nil
}
