package mihomo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// These listeners never participate in ticket collection or CODEX-ROTATE.
const AstraLanes = 8
const astraPort = collectPort + defaultCollectLanes

func astraGroup(lane int) string { return fmt.Sprintf("ASTRA-PIN-%d", lane) }

type AstraNode struct {
	ID       string
	Identity string
	Name     string
	Country  string
}

func astraNodes(s saved) []AstraNode {
	var result []AstraNode
	for _, node := range s.Nodes {
		id, _ := node["name"].(string)
		if id == "" || s.Disabled[id] != "" || !countryAllowed(s, id) {
			continue
		}
		name := s.NodeNames[id]
		if name == "" {
			name = id
		}
		result = append(result, AstraNode{ID: id, Identity: harvestDigest(node), Name: name, Country: s.Countries[id].Code})
	}
	return result
}

func AstraNodes() ([]AstraNode, error) {
	m := managedManager()
	if m == nil {
		return nil, errors.New("astra_rotation_unavailable")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	// Never bypass the harvest pool's persisted use-once reservation policy.
	if m.saved.UseOnce {
		return nil, errors.New("astra_rotation_use_once")
	}
	nodes := astraNodes(m.saved)
	if len(nodes) == 0 {
		return nil, errors.New("astra_rotation_no_nodes")
	}
	return nodes, nil
}

func waitAstraLock(ctx context.Context, attempt func() bool) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if attempt() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}

// PinAstraNode holds a private listener through validation and response closure.
// Management reloads wait for leases; ticket collection uses different listeners.
// Callers must use fresh HTTP CONNECTs and release after WS handshake/first turn.
func PinAstraNode(ctx context.Context, node AstraNode) (string, func(), error) {
	m := managedManager()
	if m == nil {
		return "", func() {}, errors.New("astra_rotation_unavailable")
	}
	return m.pinAstraNode(ctx, node)
}
func (m *Manager) pinAstraNode(ctx context.Context, node AstraNode) (string, func(), error) {
	noop := func() {}
	m.mu.Lock()
	if m.astraLanes == nil {
		m.astraLanes = make(chan int, AstraLanes)
		for lane := 0; lane < AstraLanes; lane++ {
			m.astraLanes <- lane
		}
	}
	lanes := m.astraLanes
	m.mu.Unlock()
	var lane int
	select {
	case lane = <-lanes:
	case <-ctx.Done():
		return "", noop, ctx.Err()
	}
	if err := waitAstraLock(ctx, m.astraGate.TryRLock); err != nil {
		lanes <- lane
		return "", noop, err
	}
	var once sync.Once
	release := func() {
		once.Do(func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			m.mu.Lock()
			secret := m.saved.Secret
			m.mu.Unlock()
			payload, _ := json.Marshal(map[string]string{"name": "REJECT"})
			_ = m.control(cleanup, http.MethodPut, "/proxies/"+astraGroup(lane), secret, payload)
			m.astraGate.RUnlock()
			lanes <- lane
		})
	}
	m.mu.Lock()
	available := m.state.Running && !m.closed && !m.saved.UseOnce
	nodes := astraNodes(m.saved)
	secret := m.saved.Secret
	m.mu.Unlock()
	found := false
	for _, n := range nodes {
		if n.ID == node.ID && n.Identity == node.Identity {
			found = true
			break
		}
	}
	if !available || !found {
		release()
		return "", noop, errors.New("astra_rotation_node_unavailable")
	}
	payload, _ := json.Marshal(map[string]string{"name": node.ID})
	if err := m.control(ctx, http.MethodPut, "/proxies/"+astraGroup(lane), secret, payload); err != nil {
		release()
		return "", noop, errors.New("astra_rotation_node_unavailable")
	}
	return fmt.Sprintf("http://127.0.0.1:%d", astraPort+lane), release, nil
}
