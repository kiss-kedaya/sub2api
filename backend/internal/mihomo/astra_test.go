package mihomo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAstraNodesRespectCountryStateAndDynamicBoundaries(t *testing.T) {
	s := saved{Nodes: []map[string]any{{"name": "sg", "server": "one"}, {"name": "hk"}, {"name": "failed"}, {"name": "used"}, {"name": "DYNAMIC-one"}}, Disabled: map[string]string{"failed": "failed", "used": "used"}, CountryFilter: CountryFilter{Mode: "exclude", Codes: []string{"HK"}, AllowUnknown: true}, Countries: map[string]CountryObservation{"sg": {Code: "SG"}, "hk": {Code: "HK"}}, NodeNames: map[string]string{"sg": "Singapore"}}
	nodes := astraNodes(s)
	require.Len(t, nodes, 2)
	require.Equal(t, "SG", nodes[0].Country)
	require.Equal(t, "Singapore", nodes[0].Name)
	require.Equal(t, "DYNAMIC-one", nodes[1].ID)
	old := nodes[0].Identity
	s.Nodes[0]["server"] = "changed"
	require.NotEqual(t, old, astraNodes(s)[0].Identity)
	m := New(t.TempDir())
	defer m.Close()
	m.state.Running = true
	m.saved = s
	m.saved.UseOnce = true
	_, err := AstraNodes()
	require.ErrorContains(t, err, "use_once")
}
func TestAstraNodeLeasesIsolateTrafficAndFenceReload(t *testing.T) {
	var mu sync.Mutex
	selected := map[string]string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NotContains(t, r.URL.Path, "CODEX-COLLECT")
		if strings.HasPrefix(r.URL.Path, "/proxies/") {
			var value map[string]string
			require.NoError(t, json.NewDecoder(r.Body).Decode(&value))
			mu.Lock()
			selected[r.URL.Path] = value["name"]
			mu.Unlock()
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	m := New(t.TempDir())
	defer m.Close()
	m.controllerURL = server.URL
	m.state.Running = true
	m.saved = saved{Secret: "fixture", Nodes: []map[string]any{{"name": "one"}, {"name": "two"}}}
	nodes := astraNodes(m.saved)
	first, releaseFirst, err := m.pinAstraNode(t.Context(), nodes[0])
	require.NoError(t, err)
	second, releaseSecond, err := m.pinAstraNode(t.Context(), nodes[1])
	require.NoError(t, err)
	t.Cleanup(releaseFirst)
	t.Cleanup(releaseSecond)
	require.NotEqual(t, first, second)
	mu.Lock()
	require.Equal(t, "one", selected["/proxies/ASTRA-PIN-0"])
	require.Equal(t, "two", selected["/proxies/ASTRA-PIN-1"])
	mu.Unlock()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, m.reload(ctx, []byte(`{}`), "fixture"), context.DeadlineExceeded)
	releaseFirst()
	releaseFirst()
	releaseSecond()
	require.NoError(t, m.reload(t.Context(), []byte(`{}`), "fixture"))
	require.Len(t, m.astraLanes, AstraLanes)
	mu.Lock()
	require.Equal(t, "REJECT", selected["/proxies/ASTRA-PIN-0"])
	require.Equal(t, "REJECT", selected["/proxies/ASTRA-PIN-1"])
	mu.Unlock()
	m.saved.Disabled = map[string]string{"one": "disabled"}
	_, _, err = m.pinAstraNode(t.Context(), nodes[0])
	require.ErrorContains(t, err, "node_unavailable")
	require.Len(t, m.astraLanes, AstraLanes)
	ctx, cancel = context.WithCancel(t.Context())
	cancel()
	_, _, err = m.pinAstraNode(ctx, nodes[1])
	require.ErrorIs(t, err, context.Canceled)
}
func TestAstraListenersArePrivateAndInitiallyReject(t *testing.T) {
	m := &Manager{}
	raw, err := m.config(saved{Nodes: []map[string]any{{"name": "fixed"}, {"name": "DYNAMIC-one"}}})
	require.NoError(t, err)
	var cfg struct {
		Listeners []struct {
			Listen, Proxy string
			Port          int
		}
		Groups []struct {
			Name    string
			Proxies []string
		} `json:"proxy-groups"`
	}
	require.NoError(t, json.Unmarshal(raw, &cfg))
	require.Len(t, cfg.Listeners, defaultCollectLanes+len(m.bpsPorts)+AstraLanes)
	for i := 0; i < AstraLanes; i++ {
		l := cfg.Listeners[defaultCollectLanes+len(m.bpsPorts)+i]
		require.Equal(t, "127.0.0.1", l.Listen)
		require.Equal(t, astraPort+i, l.Port)
		require.Equal(t, astraGroup(i), l.Proxy)
		g := cfg.Groups[1+defaultCollectLanes+i]
		require.Equal(t, []string{"REJECT", "fixed", "DYNAMIC-one"}, g.Proxies)
	}
}
