package mihomo

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCollectionWithOfficialKernel(t *testing.T) {
	if os.Getenv("MIHOMO_INSTALL_SMOKE") != "1" {
		t.Skip("opt-in official kernel download")
	}
	fake := func(name string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodConnect {
				_, _ = io.WriteString(w, name)
				return
			}
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				http.Error(w, "hijack unavailable", 500)
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
			req, err := http.ReadRequest(buffer.Reader)
			if err != nil {
				return
			}
			_ = req.Body.Close()
			resp := &http.Response{StatusCode: 200, ProtoMajor: 1, ProtoMinor: 1, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(name)), ContentLength: int64(len(name)), Close: true}
			_ = resp.Write(conn)
		}))
	}
	first := fake("first")
	defer first.Close()
	second := fake("second")
	defer second.Close()
	node := func(name, address string) map[string]any {
		u, err := url.Parse(address)
		require.NoError(t, err)
		port, err := strconv.Atoi(u.Port())
		require.NoError(t, err)
		return map[string]any{"name": name, "type": "http", "server": u.Hostname(), "port": port}
	}
	m := New(t.TempDir())
	defer m.Close()
	// The opt-in smoke test downloads a release asset before local probes.
	m.client.Timeout = 90 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	require.NoError(t, m.run(ctx, "install", saved{}))
	require.NoError(t, m.run(ctx, "start", saved{UseOnce: true, Nodes: []map[string]any{node("first", first.URL), node("second", second.URL)}}))
	c, err := BeginCollection(ctx, Endpoint)
	require.NoError(t, err)
	defer func() { _ = c.Close() }()
	n1, p1, err := c.Next(ctx, 0)
	require.NoError(t, err)
	n2, p2, err := c.Next(ctx, 1)
	require.NoError(t, err)
	fetch := func(proxy string) string {
		u, err := url.Parse(proxy)
		require.NoError(t, err)
		transport := &http.Transport{Proxy: http.ProxyURL(u)}
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:54321/probe", nil)
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		return string(body)
	}
	require.Equal(t, n1, fetch(p1))
	require.Equal(t, n2, fetch(p2))
	require.Equal(t, n1, fetch(p1))
	require.NoError(t, c.Close())
	// The simulated upstream only serves local HTTP, not public TLS.
	m.bpsProbe = func(context.Context, string) error { return nil }
	m.warmBPSPool(ctx, 2)
	// BPS binds sessions to immutable node listeners, independent of harvest use-once.
	bps1, done1, err := AcquireBPSSession(ctx, "account:1/thread:first")
	require.NoError(t, err)
	defer done1()
	bps2, done2, err := AcquireBPSSession(ctx, "account:1/thread:second")
	require.NoError(t, err)
	defer done2()
	require.NotEqual(t, bps1, bps2)
	firstExit, secondExit := fetch(bps1), fetch(bps2)
	require.NotEqual(t, firstExit, secondExit)
	again, done3, err := AcquireBPSSession(ctx, "account:1/thread:first")
	require.NoError(t, err)
	require.Equal(t, bps1, again)
	require.Equal(t, firstExit, fetch(again))
	done3()
	proxy, release, err := PinNode(ctx, n1)
	require.NoError(t, err)
	defer release()
	require.Equal(t, n1, fetch(proxy))
	release()
	// Exercise the directed adapter against the real selector/listener API,
	// with local simulated exits in both reusable and use-once modes.
	for _, useOnce := range []bool{false, true} {
		require.NoError(t, m.run(ctx, "start", saved{Secret: m.saved.Secret, UseOnce: useOnce, Nodes: []map[string]any{node("first", first.URL), node("second", second.URL)}}))
		sidecar, err := LoadDirectedSidecar("", Endpoint)
		require.NoError(t, err)
		nodes, err := sidecar.Directory(ctx)
		require.NoError(t, err)
		for _, selected := range nodes {
			end, err := sidecar.Acquire(ctx, selected)
			require.NoError(t, err)
			require.Equal(t, selected.ID, fetch(sidecar.ProxyURL))
			require.NoError(t, sidecar.Confirm(ctx, selected))
			end()
		}
		if useOnce {
			_, err := sidecar.Directory(ctx)
			require.ErrorContains(t, err, "no eligible")
		}
	}
	// Config reloads and harvest selector changes must preserve the BPS exits.
	require.Equal(t, firstExit, fetch(bps1))
	require.Equal(t, secondExit, fetch(bps2))

	// Exercise more than the old 32 lanes with the official kernel. Every
	// simulated proxy returns its own identity; no external probes are sent.
	expanded := saved{Secret: m.saved.Secret, Nodes: []map[string]any{node("first", first.URL), node("second", second.URL)}}
	for i := 2; i < 128; i++ {
		name := fmt.Sprintf("exit-%03d", i)
		exit := fake(name)
		defer exit.Close()
		expanded.Nodes = append(expanded.Nodes, node(name, exit.URL))
	}
	require.NoError(t, m.run(ctx, "start", expanded))
	large, err := BeginCollection(ctx, Endpoint, 128)
	require.NoError(t, err)
	defer func() { _ = large.Close() }()
	require.Equal(t, 128, large.LaneCount())
	for lane := 0; lane < large.LaneCount(); lane++ {
		selected, proxy, err := large.Next(ctx, lane)
		require.NoError(t, err)
		require.Equal(t, selected, fetch(proxy), "lane %d", lane)
	}
	require.NoError(t, large.Close())
	for _, port := range large.ports[defaultCollectLanes:] {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
		if conn != nil {
			_ = conn.Close()
		}
		require.Error(t, err, "temporary listener must be removed: %d", port)
	}
	// Existing BPS ports and bindings survive both collection reloads.
	require.Equal(t, firstExit, fetch(bps1))
	require.Equal(t, secondExit, fetch(bps2))

}
