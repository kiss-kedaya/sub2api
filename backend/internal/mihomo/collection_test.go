package mihomo

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCollectionIsolatesLanesAndPersistsReservations(t *testing.T) {
	var mu sync.Mutex
	selected := map[string]string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/configs" {
			var payload map[string]string
			_ = json.NewDecoder(r.Body).Decode(&payload)
			mu.Lock()
			selected[r.URL.Path] = payload["name"]
			mu.Unlock()
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	m := New(t.TempDir())
	defer m.Close()
	m.controllerURL = server.URL
	m.state.Running = true
	m.saved = saved{UseOnce: true, Secret: "test", Nodes: []map[string]any{{"name": "one"}, {"name": "two"}, {"name": "disabled"}}, Disabled: map[string]string{"disabled": "disabled"}}
	c, err := BeginCollection(context.Background(), Endpoint)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	var wg sync.WaitGroup
	for lane := 0; lane < 2; lane++ {
		wg.Add(1)
		go func(lane int) { defer wg.Done(); _, _, _ = c.Next(context.Background(), lane) }(lane)
	}
	wg.Wait()
	mu.Lock()
	require.Len(t, selected, 2)
	require.NotEqual(t, selected["/proxies/CODEX-COLLECT-0"], selected["/proxies/CODEX-COLLECT-1"])
	mu.Unlock()
	data, err := os.ReadFile(filepath.Join(m.dir, "settings.json"))
	require.NoError(t, err)
	var snapshot saved
	require.NoError(t, json.Unmarshal(data, &snapshot))
	require.Equal(t, "used", snapshot.Disabled["one"])
	require.Equal(t, "used", snapshot.Disabled["two"])
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err = Lease(ctx, Endpoint)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.NoError(t, c.Close())
	require.NoError(t, c.Close())
	proxy, release, err := PinNode(context.Background(), "one")
	require.NoError(t, err)
	require.Equal(t, collectionProxy(0), proxy)
	release()
	release()
	_, _, err = PinNode(context.Background(), "disabled")
	require.Error(t, err)
}

func TestCollectionConfigRejectsUnselectedLanesAndHonorsCountryFilter(t *testing.T) {
	m := &Manager{}
	s := saved{Nodes: []map[string]any{{"name": "allowed"}, {"name": "blocked"}, {"name": "country-blocked"}}, Disabled: map[string]string{"blocked": "disabled"}, CountryFilter: CountryFilter{Mode: "exclude", Codes: []string{"HK"}}, Countries: map[string]CountryObservation{"allowed": {Code: "US"}, "country-blocked": {Code: "HK"}}}
	data, err := m.config(s)
	require.NoError(t, err)
	var cfg map[string]any
	require.NoError(t, json.Unmarshal(data, &cfg))
	listeners, ok := cfg["listeners"].([]any)
	require.True(t, ok)
	require.Len(t, listeners, defaultCollectLanes+1+AstraLanes)
	groups, ok := cfg["proxy-groups"].([]any)
	require.True(t, ok)
	for i, l := range listeners[:defaultCollectLanes] {
		listener, ok := l.(map[string]any)
		require.True(t, ok)
		require.Equal(t, "127.0.0.1", listener["listen"])
		require.EqualValues(t, collectPort+i, listener["port"])
		group, ok := groups[i+1].(map[string]any)
		require.True(t, ok)
		require.Equal(t, listener["proxy"], group["name"])
		require.Equal(t, []any{"REJECT", "allowed"}, group["proxies"])
	}
	c, err := BeginCollection(context.Background(), "http://127.0.0.1:9999")
	require.Error(t, err)
	require.Nil(t, c)
}

func TestCollectionResumesAcrossTasksAndRestart(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	dir := t.TempDir()
	m := New(dir)
	defer func() { m.Close() }()
	m.controllerURL = server.URL
	m.state.Running = true
	for i := 0; i < 1100; i++ {
		m.saved.Nodes = append(m.saved.Nodes, map[string]any{"name": fmt.Sprintf("node-%04d", i)})
	}
	data, err := json.Marshal(m.saved)
	require.NoError(t, err)
	require.NoError(t, atomicWrite(filepath.Join(dir, "settings.json"), data, 0600))
	seen := map[string]bool{}
	for task := 0; task < 11; task++ {
		c, err := BeginCollection(context.Background(), Endpoint)
		require.NoError(t, err)
		for attempt := 0; attempt < 100; attempt++ {
			node, _, err := c.Next(context.Background(), attempt%32)
			require.NoError(t, err)
			require.NotEmpty(t, node)
			require.False(t, seen[node], "repeated %s in task %d", node, task)
			seen[node] = true
		}
		require.NoError(t, c.Close())
		m.Close()
		m = New(dir)
		m.controllerURL = server.URL
		m.state.Running = true
	}
	require.Len(t, seen, 1100)
	c, err := BeginCollection(context.Background(), Endpoint)
	require.NoError(t, err)
	defer func() { _ = c.Close() }()
	node, _, err := c.Next(context.Background(), 0)
	require.NoError(t, err)
	require.Equal(t, "node-0000", node, "wrap only after traversing the pool")
}

func TestCollectionProvisionsRequestedLanesAndRestoresConfig(t *testing.T) {
	var configs []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/configs" {
			var payload map[string]string
			_ = json.NewDecoder(r.Body).Decode(&payload)
			var config map[string]any
			_ = json.Unmarshal([]byte(payload["payload"]), &config)
			configs = append(configs, config)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	m := New(t.TempDir())
	defer m.Close()
	m.controllerURL = server.URL
	m.state.Running = true
	for i := 0; i < 128; i++ {
		m.saved.Nodes = append(m.saved.Nodes, map[string]any{"name": fmt.Sprintf("node-%03d", i)})
	}
	c, err := BeginCollection(context.Background(), Endpoint, 128)
	require.NoError(t, err)
	defer func() { _ = c.Close() }()
	require.Equal(t, 128, c.LaneCount())
	require.Len(t, configs, 1)
	listeners, ok := configs[0]["listeners"].([]any)
	require.True(t, ok)
	ports := map[int]bool{}
	for _, item := range listeners {
		listener, ok := item.(map[string]any)
		require.True(t, ok)
		portValue, ok := listener["port"].(float64)
		require.True(t, ok)
		port := int(portValue)
		require.False(t, ports[port], "listener port collision: %d", port)
		ports[port] = true
		require.Equal(t, "127.0.0.1", listener["listen"])
	}
	seen := sync.Map{}
	var wg sync.WaitGroup
	for lane := 0; lane < c.LaneCount(); lane++ {
		wg.Go(func() {
			node, proxy, err := c.Next(context.Background(), lane)
			assert.NoError(t, err)
			assert.Equal(t, fmt.Sprintf("http://127.0.0.1:%d", c.ports[lane]), proxy)
			_, loaded := seen.LoadOrStore(node, true)
			assert.False(t, loaded, "duplicate node %s", node)
		})
	}
	wg.Wait()
	node, _, err := c.Next(context.Background(), 0)
	require.NoError(t, err)
	require.Empty(t, node, "no wrap within the same collection")
	require.NoError(t, c.Close())
	require.NoError(t, c.Close())
	require.Len(t, configs, 2)
	require.Len(t, configs[1]["listeners"], defaultCollectLanes+128+AstraLanes)
	_, _, err = c.Next(context.Background(), 0)
	require.ErrorContains(t, err, "closed")
}

func TestCollectionFiltersResumedPoolAndCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer server.Close()
	m := New(t.TempDir())
	defer m.Close()
	m.controllerURL = server.URL
	m.state.Running = true
	m.saved = saved{
		CollectionLastNode: "last", UseOnce: true,
		Nodes:         []map[string]any{{"name": "first"}, {"name": "last"}, {"name": "blocked"}, {"name": "excluded"}, {"name": "next"}},
		Disabled:      map[string]string{"last": "used", "blocked": "disabled"},
		CountryFilter: CountryFilter{Mode: "exclude", Codes: []string{"HK"}},
		Countries:     map[string]CountryObservation{"excluded": {Code: "HK"}, "first": {Code: "US"}, "next": {Code: "US"}},
	}
	c, err := BeginCollection(context.Background(), Endpoint, 100_000)
	require.NoError(t, err)
	defer func() { _ = c.Close() }()
	require.Equal(t, 2, c.LaneCount(), "allocate only useful lanes")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = c.Next(ctx, 0)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, "last", m.saved.CollectionLastNode)
	for _, want := range []string{"next", "first"} {
		node, _, err := c.Next(context.Background(), 0)
		require.NoError(t, err)
		require.Equal(t, want, node)
		require.Equal(t, "used", m.saved.Disabled[node])
	}
}

func TestCollectionFailedProvisionRestoresAndReleasesGate(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	m := New(t.TempDir())
	defer m.Close()
	m.controllerURL = server.URL
	m.state.Running = true
	for i := 0; i < 64; i++ {
		m.saved.Nodes = append(m.saved.Nodes, map[string]any{"name": fmt.Sprintf("node-%d", i)})
	}
	_, err := BeginCollection(context.Background(), Endpoint, 64)
	require.ErrorContains(t, err, "provision")
	require.Equal(t, 2, calls, "failed reload must be followed by cleanup")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	c, err := BeginCollection(ctx, Endpoint, 2)
	require.NoError(t, err)
	require.NoError(t, c.Close())
}
