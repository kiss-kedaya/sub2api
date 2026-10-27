package mihomo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"sync"
	"time"
)

// Keep the existing idle listeners stable for directed harvest and ticket pinning.
// Parallel tasks provision additional listeners on demand.
const defaultCollectLanes = 32
const collectPort = 17893

func collectionGroup(lane int) string { return fmt.Sprintf("CODEX-COLLECT-%d", lane) }
func collectionProxy(lane int) string { return fmt.Sprintf("http://127.0.0.1:%d", collectPort+lane) }

// Collection owns the management gate for the whole round. Each worker owns
// one selector/listener; management and use-once probes cannot change its exit.
type Collection struct {
	m        *Manager
	mu       sync.Mutex
	nodes    []string
	next     int
	ports    []int
	extended bool
	closed   bool
	closeErr error
	once     sync.Once
}

func managedManager() *Manager {
	var selected *Manager
	managers.Range(func(key, _ any) bool {
		m, ok := key.(*Manager)
		if !ok {
			return true
		}
		m.mu.Lock()
		ready := !m.closed && m.state.Running
		m.mu.Unlock()
		if ready {
			selected = m
			return false
		}
		return true
	})
	return selected
}

func BeginCollection(ctx context.Context, proxy string, requestedLanes ...int) (*Collection, error) {
	if proxy != Endpoint {
		return nil, errors.New("parallel collection requires the managed Mihomo proxy")
	}
	m := managedManager()
	if m == nil {
		return nil, errors.New("managed Mihomo is not running")
	}
	if err := m.acquire(ctx); err != nil {
		return nil, err
	}
	m.mu.Lock()
	if m.closed || !m.state.Running {
		m.mu.Unlock()
		m.release()
		return nil, errors.New("managed Mihomo is not running")
	}
	c := &Collection{m: m}
	start := 0
	for i, n := range m.saved.Nodes {
		if n["name"] == m.saved.CollectionLastNode {
			start = i + 1
			break
		}
	}
	for i := range m.saved.Nodes {
		n := m.saved.Nodes[(start+i)%len(m.saved.Nodes)]
		name, _ := n["name"].(string)
		if name != "" && m.saved.Disabled[name] == "" && countryAllowed(m.saved, name) {
			c.nodes = append(c.nodes, name)
		}
	}
	snapshot := m.saved
	m.mu.Unlock()
	if len(c.nodes) == 0 {
		m.release()
		return nil, errors.New("no eligible proxy nodes")
	}
	lanes := defaultCollectLanes
	if len(requestedLanes) > 0 {
		lanes = requestedLanes[0]
	}
	lanes = min(lanes, len(c.nodes))
	if lanes < 1 {
		m.release()
		return nil, errors.New("collection requires at least one lane")
	}
	c.ports = make([]int, lanes)
	for lane := range c.ports {
		c.ports[lane] = collectPort + lane
	}
	if lanes > defaultCollectLanes {
		if err := c.extend(ctx, snapshot); err != nil {
			return nil, errors.Join(err, c.Close())
		}
	}
	return c, nil
}

// LaneCount reflects useful concurrency, bounded by the available node snapshot.
func (c *Collection) LaneCount() int { return len(c.ports) }

func (c *Collection) extend(ctx context.Context, snapshot saved) error {
	// Reserve OS-assigned ports together so they cannot collide with each other
	// or existing business listeners. Release only immediately before handoff.
	var reservations []net.Listener
	defer func() {
		for _, listener := range reservations {
			_ = listener.Close()
		}
	}()
	for lane := defaultCollectLanes; lane < len(c.ports); lane++ {
		listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
		if err != nil {
			return errors.New("cannot allocate parallel collection ports")
		}
		reservations = append(reservations, listener)
		address, ok := listener.Addr().(*net.TCPAddr)
		if !ok {
			return errors.New("invalid parallel collection listener address")
		}
		c.ports[lane] = address.Port
	}
	config, err := c.m.collectionConfig(snapshot, c.ports)
	if err != nil {
		return err
	}
	for _, listener := range reservations {
		_ = listener.Close()
	}
	// A rejected/partial reload also needs cleanup before releasing the gate.
	c.extended = true
	if err := c.m.reload(ctx, config, snapshot.Secret); err != nil {
		return errors.New("cannot provision parallel collection listeners")
	}
	return nil
}

// Next persists use-once reservations before exposing an exit to a worker.
func (c *Collection) Next(ctx context.Context, lane int) (node, proxy string, err error) {
	if lane < 0 || lane >= len(c.ports) {
		return "", "", errors.New("invalid collection lane")
	}
	if err = ctx.Err(); err != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return "", "", errors.New("collection is closed")
	}
	if err = ctx.Err(); err != nil {
		return
	}
	if c.next >= len(c.nodes) {
		return "", "", nil
	}
	node = c.nodes[c.next]
	c.next++
	c.m.mu.Lock()
	next := c.m.saved
	if next.UseOnce {
		next.Disabled = copyNodeStates(next.Disabled)
		next.Disabled[node] = "used"
	}
	// Persist the cursor even for reusable nodes, before handing out the exit.
	next.CollectionLastNode = node
	data, marshalErr := json.Marshal(next)
	err = marshalErr
	if err == nil {
		err = atomicWrite(filepath.Join(c.m.dir, "settings.json"), data, 0600)
	}
	if err == nil {
		c.m.saved = next
	}
	c.m.mu.Unlock()
	if err != nil {
		return "", "", errors.New("cannot reserve collection node")
	}
	payload, _ := json.Marshal(map[string]string{"name": node})
	if err = c.m.control(ctx, http.MethodPut, "/proxies/"+collectionGroup(lane), next.Secret, payload); err != nil {
		return "", "", err
	}
	return node, fmt.Sprintf("http://127.0.0.1:%d", c.ports[lane]), nil
}

func copyNodeStates(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func (c *Collection) Close() error {
	c.once.Do(func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.closed = true
		defer c.m.release()
		c.m.mu.Lock()
		next := c.m.saved
		c.m.mu.Unlock()
		if !next.UseOnce && !c.extended {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		config, err := c.m.config(next)
		if err == nil {
			err = c.m.reload(ctx, config, next.Secret)
		}
		if err != nil {
			c.m.stop()
			c.closeErr = errors.New("cannot restore collection configuration; managed proxy stopped")
		}
	})
	return c.closeErr
}

// PinNode uses a dedicated lane, even when the node was retired from harvesting.
// Explicitly disabled/failed nodes and country exclusions remain enforced.
func PinNode(ctx context.Context, name string) (string, func(), error) {
	noop := func() {}
	m := managedManager()
	if m == nil {
		return "", noop, errors.New("managed Mihomo is not running")
	}
	if err := m.acquire(ctx); err != nil {
		return "", noop, err
	}
	m.mu.Lock()
	snapshot := m.saved
	closed := m.closed
	running := m.state.Running
	m.mu.Unlock()
	allowed := false
	for _, n := range snapshot.Nodes {
		if n["name"] == name && countryAllowed(snapshot, name) && (snapshot.Disabled[name] == "" || snapshot.Disabled[name] == "used") {
			allowed = true
		}
	}
	if !allowed || closed || !running {
		m.release()
		return "", noop, errors.New("ticket exit is unavailable")
	}
	payload, _ := json.Marshal(map[string]string{"name": name})
	if err := m.control(ctx, http.MethodPut, "/proxies/"+collectionGroup(0), snapshot.Secret, payload); err != nil {
		m.release()
		return "", noop, err
	}
	var once sync.Once
	return collectionProxy(0), func() { once.Do(m.release) }, nil
}
