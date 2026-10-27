package mihomo

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"strconv"
	"time"
)

// Static node ports are private HTTP proxy listeners. A port is never assigned
// to another node, even after its proxy row is deleted, to avoid silently
// changing the exit of a previously copied proxy URL.
const (
	staticNodePortStart = 32000
	staticNodePortEnd   = 42000
	maxStaticBindings   = 4096
)

type StaticBinding struct {
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Port     int    `json:"port"`
	Enabled  bool   `json:"enabled"`
}

type StaticNodeBinding struct {
	Host        string
	Username    string
	Password    string
	Name        string
	DisplayName string
	Port        int
	New         bool
}

func copyStaticBindings(in map[string]StaticBinding) map[string]StaticBinding {
	out := make(map[string]StaticBinding, len(in))
	for name, binding := range in {
		out[name] = binding
	}
	return out
}

func staticPortAvailable(port int) bool {
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return false
	}
	_ = listener.Close()
	return true
}

func (m *Manager) staticPortAvailable(port int, exposure ManagedExposure) bool {
	if !m.managed || exposure.Listen == "" {
		return staticPortAvailable(port)
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(exposure.Listen, strconv.Itoa(port)))
	if err != nil {
		return false
	}
	_ = listener.Close()
	return true
}

// BindStaticNodes adds persistent, per-node loopback listeners to the managed
// kernel. All names are validated before its configuration is changed.
func (m *Manager) BindStaticNodes(ctx context.Context, names []string) ([]StaticNodeBinding, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if len(names) == 0 || len(names) > 100 {
		return nil, errors.New("select between 1 and 100 nodes")
	}
	m.mu.Lock()
	if m.closed || m.state.Busy || !m.state.Installed || !m.state.Running {
		m.mu.Unlock()
		return nil, errors.New("managed Mihomo is not ready")
	}
	next := m.saved
	if m.managed && (next.Exposure.Listen == "" || validateManagedExposure(next.Exposure) != nil) {
		m.mu.Unlock()
		return nil, errors.New("configure a private listener, advertised address and allowed source CIDRs before importing shared proxies")
	}
	next.StaticBindings = copyStaticBindings(m.saved.StaticBindings)
	available := make(map[string]bool, len(next.Nodes))
	for _, node := range next.Nodes {
		if name, ok := node["name"].(string); ok {
			available[name] = next.Disabled[name] == "" && countryAllowed(next, name)
		}
	}
	used := make(map[int]bool, len(next.StaticBindings))
	for _, binding := range next.StaticBindings {
		used[binding.Port] = true
	}
	result := make([]StaticNodeBinding, 0, len(names))
	seen := make(map[string]bool, len(names))
	changed := false
	for _, name := range names {
		if seen[name] {
			continue
		}
		seen[name] = true
		if _, ok := available[name]; !ok {
			m.mu.Unlock()
			return nil, ErrUnknownNode
		}
		if !available[name] {
			m.mu.Unlock()
			return nil, errors.New("enable the node and allow its region before adding a static proxy")
		}
		binding, exists := next.StaticBindings[name]
		newBinding := !binding.Enabled
		if !exists {
			if len(next.StaticBindings) >= maxStaticBindings {
				m.mu.Unlock()
				return nil, errors.New("static node binding limit reached")
			}
			for port := staticNodePortStart; port < staticNodePortEnd; port++ {
				if !used[port] && m.staticPortAvailable(port, next.Exposure) {
					binding.Port = port
					used[port] = true
					break
				}
			}
			if binding.Port == 0 {
				m.mu.Unlock()
				return nil, errors.New("no private port is available for a static node")
			}
		} else if !binding.Enabled && !m.staticPortAvailable(binding.Port, next.Exposure) {
			m.mu.Unlock()
			return nil, errors.New("the node's reserved static port is unavailable")
		}
		if newBinding {
			if m.managed && binding.Password == "" {
				secret := make([]byte, 32)
				if _, err := rand.Read(secret); err != nil {
					m.mu.Unlock()
					return nil, errors.New("cannot create proxy credential")
				}
				binding.Username = "mh_" + name
				binding.Password = hex.EncodeToString(secret)
			}
			binding.Enabled = true
			next.StaticBindings[name] = binding
			changed = true
		}
		label := next.NodeNames[name]
		if label == "" {
			label = name
		}
		host := "127.0.0.1"
		if m.managed {
			host = next.Exposure.Advertise
		}
		result = append(result, StaticNodeBinding{Name: name, DisplayName: sanitizeNodeDisplayName(label), Port: binding.Port, New: newBinding, Host: host, Username: binding.Username, Password: binding.Password})
	}
	if !changed {
		m.mu.Unlock()
		return result, nil
	}
	m.state.Busy = true
	m.wg.Add(1)
	m.mu.Unlock()
	defer m.wg.Done()
	defer m.clearStaticBindingBusy()
	if err := m.acquire(ctx); err != nil {
		return nil, err
	}
	defer m.release()
	m.mu.Lock()
	next.Disabled = m.saved.Disabled
	next.UseOnce = m.saved.UseOnce
	next.CollectionLastNode = m.saved.CollectionLastNode
	m.mu.Unlock()
	if err := m.run(ctx, "static_bind", next); err != nil {
		return nil, err
	}
	return result, nil
}

// DeactivateStaticNodes removes listeners but retains their reserved ports.
// The old port cannot later route a different node.
func (m *Manager) DeactivateStaticNodes(ctx context.Context, names []string) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if len(names) == 0 {
		return nil
	}
	m.mu.Lock()
	if m.closed || m.state.Busy {
		m.mu.Unlock()
		return errors.New("managed Mihomo is busy or stopping")
	}
	next := m.saved
	next.StaticBindings = copyStaticBindings(m.saved.StaticBindings)
	changed := false
	for _, name := range names {
		binding := next.StaticBindings[name]
		if binding.Enabled {
			binding.Enabled = false
			next.StaticBindings[name] = binding
			changed = true
		}
	}
	if !changed {
		m.mu.Unlock()
		return nil
	}
	m.state.Busy = true
	m.wg.Add(1)
	running := m.state.Running
	m.mu.Unlock()
	defer m.wg.Done()
	defer m.clearStaticBindingBusy()
	if err := m.acquire(ctx); err != nil {
		return err
	}
	defer m.release()
	m.mu.Lock()
	next.Disabled = m.saved.Disabled
	next.UseOnce = m.saved.UseOnce
	next.CollectionLastNode = m.saved.CollectionLastNode
	m.mu.Unlock()
	if running {
		return m.run(ctx, "static_unbind", next)
	}
	data, err := json.Marshal(next)
	if err != nil {
		return errors.New("cannot save static node bindings")
	}
	if err := atomicWrite(filepath.Join(m.dir, "settings.json"), data, 0600); err != nil {
		return errors.New("cannot save static node bindings")
	}
	m.mu.Lock()
	m.saved = next
	m.mu.Unlock()
	return nil
}

func (m *Manager) clearStaticBindingBusy() {
	m.mu.Lock()
	m.state.Busy = false
	m.mu.Unlock()
}

// DeactivateStaticNode is used by the proxy-list deletion path. The manager
// is created with the system handler even if its kernel is currently stopped.
func DeactivateStaticNode(ctx context.Context, name string) error {
	var selected *Manager
	managers.Range(func(key, _ any) bool {
		manager, ok := key.(*Manager)
		if !ok {
			return true
		}
		manager.mu.Lock()
		valid := !manager.closed && manager.saved.StaticBindings[name].Enabled
		manager.mu.Unlock()
		if valid {
			selected = manager
			return false
		}
		return true
	})
	if selected == nil {
		return nil
	}
	return selected.DeactivateStaticNodes(ctx, []string{name})
}

// RestoreStaticNode compensates a failed database deletion after deactivation.
func RestoreStaticNode(ctx context.Context, name string) {
	managers.Range(func(key, _ any) bool {
		manager, ok := key.(*Manager)
		if !ok {
			return true
		}
		manager.mu.Lock()
		_, reserved := manager.saved.StaticBindings[name]
		valid := reserved && !manager.closed
		manager.mu.Unlock()
		if !valid {
			return true
		}
		_, _ = manager.BindStaticNodes(ctx, []string{name})
		return false
	})
}
