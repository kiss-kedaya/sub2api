package mihomo

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

const (
	// Every check owns a private kernel process; batches queue behind this bound.
	nodeProbeParallelism = 4
	// Covers kernel startup plus the slowest diagnostic, a quality check with
	// sequential target requests.
	nodeProbeTimeout = 2 * time.Minute
	// A kernel that exits is reported at once; this only bounds a slow start.
	isolatedKernelStartTimeout = 5 * time.Second
)

var nodeProbeSlots = make(chan struct{}, nodeProbeParallelism)

// ErrUnknownNode reports a node name that is not in the saved configuration.
var ErrUnknownNode = errors.New("unknown node")

// NodeCheck is the latest administrator-initiated connection or quality check
// of one node, shaped like the static proxy list's latency fields. It is kept
// in memory only and never changes rotation, node state or region rules. For a
// dynamic proxy it describes the measured connection, not the next one.
type NodeCheck struct {
	CheckedAt      int64  `json:"checked_at"`
	LatencyStatus  string `json:"latency_status,omitempty"`
	LatencyMs      *int64 `json:"latency_ms,omitempty"`
	LatencyMessage string `json:"latency_message,omitempty"`
	IPAddress      string `json:"ip_address,omitempty"`
	Country        string `json:"country,omitempty"`
	CountryCode    string `json:"country_code,omitempty"`
	Region         string `json:"region,omitempty"`
	City           string `json:"city,omitempty"`
	QualityStatus  string `json:"quality_status,omitempty"`
	QualityScore   *int   `json:"quality_score,omitempty"`
	QualityGrade   string `json:"quality_grade,omitempty"`
	QualitySummary string `json:"quality_summary,omitempty"`
	QualityChecked *int64 `json:"quality_checked,omitempty"`
}

func (c NodeCheck) clone() *NodeCheck {
	if c.LatencyMs != nil {
		latency := *c.LatencyMs
		c.LatencyMs = &latency
	}
	if c.QualityScore != nil {
		score := *c.QualityScore
		c.QualityScore = &score
	}
	if c.QualityChecked != nil {
		checked := *c.QualityChecked
		c.QualityChecked = &checked
	}
	return &c
}

// ProbeNode runs check through a private kernel that routes only the named
// node, whether or not the managed kernel is running. Production groups, BPS
// listeners and harvest lanes are untouched, so disabled, failed and
// region-excluded nodes can be checked without re-entering rotation. The
// proxy URL carries a one-time credential and stops working when check returns.
func (m *Manager) ProbeNode(ctx context.Context, name string, check func(ctx context.Context, proxyURL string)) error {
	m.mu.Lock()
	closed, installed := m.closed, m.state.Installed
	var node json.RawMessage
	var err error
	for _, n := range m.saved.Nodes {
		if n["name"] == name {
			// Snapshot under the lock; sources may be replaced while the check runs.
			node, err = json.Marshal(n)
			break
		}
	}
	m.mu.Unlock()
	switch {
	case closed:
		return errors.New("service is stopping")
	case !installed:
		return errors.New("install the kernel first")
	case err != nil:
		return errors.New("invalid saved node")
	case node == nil:
		return ErrUnknownNode
	}
	select {
	case nodeProbeSlots <- struct{}{}:
		defer func() { <-nodeProbeSlots }()
	case <-ctx.Done():
		return ctx.Err()
	}
	ctx, cancel := context.WithTimeout(ctx, nodeProbeTimeout)
	defer cancel()
	return m.withIsolatedNode(ctx, name, node, func(proxy *url.URL) { check(ctx, proxy.String()) })
}

// RecordNodeCheck keeps the latest check of a node that still exists and drops
// results of removed nodes. A connection-only result keeps the previous quality
// grade, matching the static proxy list.
func (m *Manager) RecordNodeCheck(name string, check NodeCheck) {
	m.mu.Lock()
	defer m.mu.Unlock()
	present := make(map[string]bool, len(m.saved.Nodes))
	for _, n := range m.saved.Nodes {
		if id, ok := n["name"].(string); ok {
			present[id] = true
		}
	}
	if !present[name] {
		return
	}
	for id := range m.nodeChecks {
		if !present[id] {
			delete(m.nodeChecks, id)
		}
	}
	if m.nodeChecks == nil {
		m.nodeChecks = make(map[string]NodeCheck)
	}
	if previous, ok := m.nodeChecks[name]; ok && check.QualityChecked == nil {
		check.QualityStatus = previous.QualityStatus
		check.QualityScore = previous.QualityScore
		check.QualityGrade = previous.QualityGrade
		check.QualitySummary = previous.QualitySummary
		check.QualityChecked = previous.QualityChecked
	}
	m.nodeChecks[name] = check
}

// withIsolatedNode starts a private kernel that exposes exactly one outbound on
// an authenticated loopback listener and calls use once it accepts connections.
// The process and its configuration are removed before returning. Errors are
// fixed text and never contain the outbound definition or its credentials.
func (m *Manager) withIsolatedNode(ctx context.Context, name string, node any, use func(proxy *url.URL)) error {
	dir, err := os.MkdirTemp(m.dir, ".node-probe-*")
	if err != nil {
		return errors.New("cannot prepare isolated kernel")
	}
	defer func() { _ = os.RemoveAll(dir) }()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return errors.New("cannot reserve isolated kernel port")
	}
	address := listener.Addr().String()
	_, portText, _ := net.SplitHostPort(address)
	_ = listener.Close()
	port, err := strconv.Atoi(portText)
	if err != nil {
		return errors.New("cannot reserve isolated kernel port")
	}
	secret := make([]byte, 24)
	if _, err = rand.Read(secret); err != nil {
		return errors.New("cannot generate isolated kernel credential")
	}
	password := hex.EncodeToString(secret)
	config, err := json.Marshal(map[string]any{"mixed-port": port, "allow-lan": false, "bind-address": "127.0.0.1", "mode": "rule", "log-level": "silent", "authentication": []string{"probe:" + password}, "proxies": []any{node}, "rules": []string{"MATCH," + name}})
	if err != nil {
		return errors.New("invalid saved node")
	}
	path := filepath.Join(dir, "config.json")
	if atomicWrite(path, config, 0600) != nil {
		return errors.New("cannot write isolated kernel configuration")
	}
	cmd := exec.CommandContext(ctx, filepath.Join(m.dir, "mihomo"), "-d", dir, "-f", path)
	configureChild(cmd)
	if cmd.Start() != nil {
		return errors.New("isolated kernel failed to start")
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	defer func() { _ = cmd.Process.Kill(); <-exited }()
	deadline := time.NewTimer(isolatedKernelStartTimeout)
	defer deadline.Stop()
	for {
		conn, dialErr := (&net.Dialer{Timeout: 100 * time.Millisecond}).DialContext(ctx, "tcp", address)
		if dialErr == nil {
			_ = conn.Close()
			use(&url.URL{Scheme: "http", Host: address, User: url.UserPassword("probe", password)})
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-exited:
			// The kernel rejected the outbound definition or could not bind.
			return errors.New("isolated kernel exited during startup")
		case <-deadline.C:
			return errors.New("isolated kernel did not become ready")
		case <-time.After(50 * time.Millisecond):
		}
	}
}
