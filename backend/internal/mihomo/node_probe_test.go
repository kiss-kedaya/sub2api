package mihomo

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const fakeIsolatedKernelEnv = "MIHOMO_FAKE_ISOLATED_KERNEL"

// TestFakeIsolatedKernel is a helper process, not a test: ProbeNode tests run
// the test binary as the kernel. It serves the configured authenticated
// listener until killed and answers each proxied request with the outbound
// names and rules it was given, so tests can assert what the kernel routes.
func TestFakeIsolatedKernel(t *testing.T) {
	if os.Getenv(fakeIsolatedKernelEnv) != "1" {
		t.Skip("helper process for ProbeNode tests")
	}
	args := flag.Args()
	path := ""
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-f" {
			path = args[i+1]
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		os.Exit(3)
	}
	var cfg struct {
		Port           int              `json:"mixed-port"`
		Bind           string           `json:"bind-address"`
		AllowLAN       bool             `json:"allow-lan"`
		Authentication []string         `json:"authentication"`
		Proxies        []map[string]any `json:"proxies"`
		Rules          []string         `json:"rules"`
	}
	if json.Unmarshal(raw, &cfg) != nil || len(cfg.Authentication) != 1 || cfg.AllowLAN {
		os.Exit(3)
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(cfg.Bind, strconv.Itoa(cfg.Port)))
	if err != nil {
		os.Exit(3)
	}
	expected := "Basic " + base64.StdEncoding.EncodeToString([]byte(cfg.Authentication[0]))
	_ = http.Serve(listener, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Proxy-Authorization") != expected {
			w.WriteHeader(http.StatusProxyAuthRequired)
			return
		}
		names := []string{}
		for _, proxy := range cfg.Proxies {
			name, _ := proxy["name"].(string)
			names = append(names, name)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"proxies": names, "rules": cfg.Rules, "target": r.URL.String()})
	}))
	os.Exit(3)
}

// fakeKernelManager installs the test binary as the kernel.
func fakeKernelManager(t *testing.T) *Manager {
	t.Helper()
	executable, err := os.Executable()
	require.NoError(t, err)
	t.Setenv(fakeIsolatedKernelEnv, "1")
	m := New(t.TempDir())
	t.Cleanup(m.Close)
	script := fmt.Sprintf("#!/bin/sh\nexec '%s' '-test.run=^TestFakeIsolatedKernel$' -- \"$@\"\n", executable)
	require.NoError(t, os.WriteFile(filepath.Join(m.dir, "mihomo"), []byte(script), 0700))
	m.state.Installed = true
	m.state.Supported = true
	return m
}

type fakeKernelAnswer struct {
	Proxies []string `json:"proxies"`
	Rules   []string `json:"rules"`
	Target  string   `json:"target"`
}

func getThroughProxy(ctx context.Context, proxyURL string) (int, fakeKernelAnswer, error) {
	proxy, err := url.Parse(proxyURL)
	if err != nil {
		return 0, fakeKernelAnswer{}, err
	}
	transport := &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://exit.invalid/check", nil)
	if err != nil {
		return 0, fakeKernelAnswer{}, err
	}
	resp, err := (&http.Client{Transport: transport, Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return 0, fakeKernelAnswer{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	var answer fakeKernelAnswer
	if resp.StatusCode == http.StatusOK {
		err = json.NewDecoder(resp.Body).Decode(&answer)
	}
	return resp.StatusCode, answer, err
}

func TestProbeNodeIsolatesOneNodeWithoutChangingState(t *testing.T) {
	m := fakeKernelManager(t)
	m.saved = saved{
		Nodes: []map[string]any{
			{"name": "node-one", "type": "http", "server": "127.0.0.1", "port": 1, "password": "node-one-secret"},
			{"name": "node-two", "type": "http", "server": "127.0.0.1", "port": 2},
		},
		Disabled:      map[string]string{"node-one": "disabled"},
		Countries:     map[string]CountryObservation{"node-one": {Code: "HK"}},
		CountryFilter: CountryFilter{Mode: "exclude", Codes: []string{"HK"}},
	}
	before, err := json.Marshal(m.saved)
	require.NoError(t, err)

	var proxyURL string
	var answer fakeKernelAnswer
	calls := 0
	err = m.ProbeNode(t.Context(), "node-one", func(ctx context.Context, proxy string) {
		calls++
		proxyURL = proxy
		parsed, parseErr := url.Parse(proxy)
		require.NoError(t, parseErr)
		require.Equal(t, "127.0.0.1", parsed.Hostname())
		require.NotContains(t, proxy, "node-one-secret")

		anonymous := *parsed
		anonymous.User = nil
		status, _, getErr := getThroughProxy(ctx, anonymous.String())
		require.NoError(t, getErr)
		require.Equal(t, http.StatusProxyAuthRequired, status, "the isolated listener requires its one-time credential")

		status, answer, getErr = getThroughProxy(ctx, proxy)
		require.NoError(t, getErr)
		require.Equal(t, http.StatusOK, status)
	})
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.Equal(t, []string{"node-one"}, answer.Proxies, "only the selected node is routed, even while disabled and region-excluded")
	require.Equal(t, []string{"MATCH,node-one"}, answer.Rules)

	_, _, err = getThroughProxy(t.Context(), proxyURL)
	require.Error(t, err, "the kernel stops when the check returns")
	after, err := json.Marshal(m.saved)
	require.NoError(t, err)
	require.JSONEq(t, string(before), string(after))
	require.Equal(t, "disabled", m.Status().NodeStates[0].State)
	require.NoFileExists(t, filepath.Join(m.dir, "settings.json"))
	leftovers, err := filepath.Glob(filepath.Join(m.dir, ".node-probe-*"))
	require.NoError(t, err)
	require.Empty(t, leftovers)
}

func TestProbeNodeRejectsUnknownNodesAndMissingKernel(t *testing.T) {
	check := func(context.Context, string) { t.Fatal("check must not run") }
	m := New(t.TempDir())
	t.Cleanup(m.Close)
	m.saved.Nodes = []map[string]any{{"name": "node-one", "type": "http", "server": "127.0.0.1", "port": 1}}
	require.ErrorContains(t, m.ProbeNode(t.Context(), "node-one", check), "install the kernel first")

	m.state.Installed = true
	require.ErrorIs(t, m.ProbeNode(t.Context(), "node-two", check), ErrUnknownNode)
	require.ErrorIs(t, m.ProbeNode(t.Context(), "", check), ErrUnknownNode)

	m.Close()
	require.ErrorContains(t, m.ProbeNode(t.Context(), "node-one", check), "stopping")
}

func TestProbeNodeWaitsForAFreeKernelSlot(t *testing.T) {
	m := fakeKernelManager(t)
	m.saved.Nodes = []map[string]any{{"name": "node-one", "type": "http", "server": "127.0.0.1", "port": 1}}
	for i := 0; i < cap(nodeProbeSlots); i++ {
		nodeProbeSlots <- struct{}{}
	}
	drained := false
	drain := func() {
		if !drained {
			drained = true
			for i := 0; i < cap(nodeProbeSlots); i++ {
				<-nodeProbeSlots
			}
		}
	}
	t.Cleanup(drain)

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	var calls atomic.Int32
	err := m.ProbeNode(ctx, "node-one", func(context.Context, string) { calls.Add(1) })
	require.ErrorIs(t, err, context.DeadlineExceeded, "no kernel starts while every slot is busy")
	require.Zero(t, calls.Load())

	drain()
	require.NoError(t, m.ProbeNode(t.Context(), "node-one", func(context.Context, string) { calls.Add(1) }))
	require.EqualValues(t, 1, calls.Load())
	require.Zero(t, len(nodeProbeSlots), "a finished check releases its slot")
}

func TestProbeNodeReportsKernelExitWithoutCredentials(t *testing.T) {
	m := New(t.TempDir())
	t.Cleanup(m.Close)
	m.state.Installed = true
	require.NoError(t, os.WriteFile(filepath.Join(m.dir, "mihomo"), []byte("#!/bin/sh\nexit 1\n"), 0700))
	m.saved.Nodes = []map[string]any{{"name": "node-one", "type": "ss", "server": "127.0.0.1", "port": 1, "password": "node-one-secret"}}
	started := time.Now()
	err := m.ProbeNode(t.Context(), "node-one", func(context.Context, string) { t.Fatal("check must not run") })
	require.ErrorContains(t, err, "exited during startup")
	require.NotContains(t, err.Error(), "node-one-secret")
	require.Less(t, time.Since(started), isolatedKernelStartTimeout, "an exited kernel is reported without waiting for the start timeout")
}

func TestRecordNodeCheckKeepsQualityAndDropsRemovedNodes(t *testing.T) {
	m := New(t.TempDir())
	t.Cleanup(m.Close)
	m.saved.Nodes = []map[string]any{{"name": "node-one"}, {"name": "DYNAMIC-two"}}
	latency, score, checked := int64(120), 88, time.Now().Unix()
	m.RecordNodeCheck("node-one", NodeCheck{CheckedAt: checked, LatencyStatus: "success", LatencyMs: &latency, IPAddress: "203.0.113.7", QualityStatus: "healthy", QualityScore: &score, QualityGrade: "B", QualitySummary: "ok", QualityChecked: &checked})
	m.RecordNodeCheck("node-one", NodeCheck{CheckedAt: checked + 1, LatencyStatus: "failed", LatencyMessage: "proxy connection failed"})
	m.RecordNodeCheck("node-missing", NodeCheck{CheckedAt: checked, LatencyStatus: "success"})
	m.RecordNodeCheck("DYNAMIC-two", NodeCheck{CheckedAt: checked, LatencyStatus: "success", LatencyMs: &latency})

	status := m.Status()
	require.Len(t, status.NodeStates, 2)
	first := status.NodeStates[0].Check
	require.NotNil(t, first)
	require.Equal(t, "failed", first.LatencyStatus)
	require.Nil(t, first.LatencyMs)
	require.Empty(t, first.IPAddress, "a failed connection check replaces the previous exit")
	require.Equal(t, "B", first.QualityGrade, "a connection-only check keeps the previous quality grade")
	require.Equal(t, 88, *first.QualityScore)
	require.Equal(t, checked, *first.QualityChecked)
	*first.QualityScore = 1
	require.Equal(t, 88, *m.Status().NodeStates[0].Check.QualityScore, "status exposes copies")
	encoded, err := json.Marshal(status)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "node-missing")

	m.mu.Lock()
	m.saved.Nodes = []map[string]any{{"name": "DYNAMIC-two"}}
	m.mu.Unlock()
	m.RecordNodeCheck("DYNAMIC-two", NodeCheck{CheckedAt: checked + 2, LatencyStatus: "failed"})
	m.mu.Lock()
	_, kept := m.nodeChecks["node-one"]
	m.mu.Unlock()
	require.False(t, kept, "results of removed nodes are dropped")
	require.Equal(t, "failed", m.Status().NodeStates[0].Check.LatencyStatus)
}

// The official kernel must accept the isolated configuration, enforce its
// credential and route a check through the selected node only. Traffic goes to
// a loopback upstream proxy; no real provider or public endpoint is contacted.
func TestProbeNodeWithOfficialKernel(t *testing.T) {
	m := officialKernelManager(t)
	var upstreamRequests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamRequests.Add(1)
		payload := `{"exit":"selected"}`
		if r.Method != http.MethodConnect {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, payload)
			return
		}
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "hijack unavailable", http.StatusInternalServerError)
			return
		}
		conn, buffer, err := hijacker.Hijack()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_, _ = buffer.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		if buffer.Flush() != nil {
			return
		}
		request, err := http.ReadRequest(buffer.Reader)
		if err != nil {
			return
		}
		_ = request.Body.Close()
		reply := &http.Response{StatusCode: http.StatusOK, ProtoMajor: 1, ProtoMinor: 1, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(payload)), ContentLength: int64(len(payload)), Close: true}
		_ = reply.Write(conn)
	}))
	defer upstream.Close()
	address, err := url.Parse(upstream.URL)
	require.NoError(t, err)
	port, err := strconv.Atoi(address.Port())
	require.NoError(t, err)
	m.saved = saved{
		Nodes:    []map[string]any{{"name": "node-selected", "type": "http", "server": address.Hostname(), "port": port}, {"name": "node-other", "type": "http", "server": "127.0.0.1", "port": 9}},
		Disabled: map[string]string{"node-selected": "failed"},
	}
	var body string
	var status int
	require.NoError(t, m.ProbeNode(t.Context(), "node-selected", func(ctx context.Context, proxyURL string) {
		proxy, parseErr := url.Parse(proxyURL)
		require.NoError(t, parseErr)
		transport := &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true}
		defer transport.CloseIdleConnections()
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:54321/probe", nil)
		require.NoError(t, reqErr)
		resp, doErr := (&http.Client{Transport: transport, Timeout: 10 * time.Second}).Do(req)
		require.NoError(t, doErr)
		defer func() { _ = resp.Body.Close() }()
		raw, _ := io.ReadAll(resp.Body)
		status, body = resp.StatusCode, string(raw)

		anonymous := *proxy
		anonymous.User = nil
		denied := &http.Transport{Proxy: http.ProxyURL(&anonymous), DisableKeepAlives: true}
		defer denied.CloseIdleConnections()
		req, reqErr = http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:54321/probe", nil)
		require.NoError(t, reqErr)
		resp, doErr = (&http.Client{Transport: denied, Timeout: 10 * time.Second}).Do(req)
		require.NoError(t, doErr)
		_ = resp.Body.Close()
		require.Equal(t, http.StatusProxyAuthRequired, resp.StatusCode, "the isolated listener requires its one-time credential")
	}))
	require.Equal(t, http.StatusOK, status)
	require.JSONEq(t, `{"exit":"selected"}`, body)
	require.EqualValues(t, 1, upstreamRequests.Load(), "only the authenticated request reaches the selected node")
	require.Equal(t, "failed", m.Status().NodeStates[0].State, "checks never recover or retire a node")
}

// officialKernelManager provides the official kernel: downloaded when
// MIHOMO_INSTALL_SMOKE=1, or copied from MIHOMO_KERNEL_BINARY for offline runs.
func officialKernelManager(t *testing.T) *Manager {
	t.Helper()
	binary := os.Getenv("MIHOMO_KERNEL_BINARY")
	if os.Getenv("MIHOMO_INSTALL_SMOKE") != "1" && binary == "" {
		t.Skip("opt-in official kernel: set MIHOMO_INSTALL_SMOKE=1 or MIHOMO_KERNEL_BINARY")
	}
	m := New(t.TempDir())
	t.Cleanup(m.Close)
	if binary != "" {
		raw, err := os.ReadFile(binary)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(m.dir, "mihomo"), raw, 0700))
		m.state.Installed = true
		return m
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	require.NoError(t, m.run(ctx, "install", saved{}))
	return m
}

func TestIsolatedKernelErrorsAreFixedText(t *testing.T) {
	m := New(filepath.Join(t.TempDir(), "missing"))
	t.Cleanup(m.Close)
	err := m.withIsolatedNode(t.Context(), "node-one", map[string]any{"name": "node-one", "password": "node-one-secret"}, func(*url.URL) { t.Fatal("use must not run") })
	require.EqualError(t, err, "cannot prepare isolated kernel")
}
