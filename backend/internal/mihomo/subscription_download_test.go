package mihomo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSubscriptionDownloadModesUseChosenRoute(t *testing.T) {
	const valid = "proxies:\n - {name: test, type: http, server: localhost, port: 8080}\n"
	for _, test := range []struct {
		name                  string
		mode                  SubscriptionDownloadMode
		body                  string
		wantProxy, wantDirect int32
		wantError             bool
	}{
		{"auto valid", SubscriptionDownloadAuto, valid, 1, 0, false},
		{"auto invalid YAML", SubscriptionDownloadAuto, "<html>blocked</html>", 1, 1, false},
		{"auto empty", SubscriptionDownloadAuto, "", 1, 1, false},
		{"proxy valid", SubscriptionDownloadProxy, valid, 1, 0, false},
		{"proxy invalid", SubscriptionDownloadProxy, "<html>blocked</html>", 1, 0, true},
		{"direct", SubscriptionDownloadDirect, valid, 0, 1, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var proxied, direct atomic.Int32
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { direct.Add(1); _, _ = w.Write([]byte(valid)) }))
			defer origin.Close()
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { proxied.Add(1); _, _ = w.Write([]byte(test.body)) }))
			defer proxy.Close()
			m := New(t.TempDir())
			defer m.Close()
			nodes, err := m.downloadSubscriptionNodes(context.Background(), origin.URL+"/?token=private", test.mode, proxy.URL)
			if test.wantError {
				require.Error(t, err)
				require.NotContains(t, err.Error(), "private")
			} else {
				require.NoError(t, err)
				require.Len(t, nodes, 1)
			}
			require.Equal(t, test.wantProxy, proxied.Load())
			require.Equal(t, test.wantDirect, direct.Load())
		})
	}
}

func TestSubscriptionProxyOnlyNeverFallsBackWithoutKernel(t *testing.T) {
	var calls atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer origin.Close()
	m := New(t.TempDir())
	defer m.Close()
	require.NoError(t, m.SetSubscriptionDownloadMode(t.Context(), SubscriptionDownloadProxy))
	_, _, err := m.fetchNodes(t.Context(), []string{origin.URL})
	require.ErrorContains(t, err, "proxy is not running")
	require.Zero(t, calls.Load())
	require.ErrorIs(t, m.SetSubscriptionDownloadMode(t.Context(), SubscriptionDownloadMode("invalid")), ErrSubscriptionDownloadMode)
	require.Zero(t, calls.Load())
}

func TestDedicatedDownloadModePersistsWithoutInstallingOrFetching(t *testing.T) {
	m := New(t.TempDir())
	defer m.Close()
	mode := SubscriptionDownloadDirect
	require.NoError(t, m.SetSubscriptionDownloadMode(t.Context(), mode))
	status := m.Status()
	require.Empty(t, status.Error)
	require.Equal(t, mode, status.DownloadMode)
	require.False(t, status.Installed)
	require.False(t, status.Running)
	raw, err := os.ReadFile(filepath.Join(m.dir, "settings.json"))
	require.NoError(t, err)
	var persisted saved
	require.NoError(t, json.Unmarshal(raw, &persisted))
	require.Equal(t, mode, persisted.DownloadMode)
	require.ErrorIs(t, m.SetSubscriptionDownloadMode(t.Context(), SubscriptionDownloadMode("invalid")), ErrSubscriptionDownloadMode)
	require.Equal(t, mode, m.Status().DownloadMode)
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	require.Error(t, m.SetSubscriptionDownloadMode(canceled, SubscriptionDownloadProxy))
	require.Equal(t, mode, m.Status().DownloadMode)
}

func TestSubscriptionManagerUsesDedicatedDownloadMode(t *testing.T) {
	var direct, proxied atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		direct.Add(1)
		_, _ = w.Write([]byte("proxies:\n - {name: direct, type: http, server: localhost, port: 8080}\n"))
	}))
	defer origin.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxied.Add(1)
		_, _ = w.Write([]byte("<html>not a subscription</html>"))
	}))
	defer proxy.Close()
	m := sourceTestManager(t)
	m.subscriptionProxyURL = proxy.URL
	require.NoError(t, m.SetSubscriptionDownloadMode(t.Context(), SubscriptionDownloadDirect))
	require.NoError(t, m.SubmitSourceManagement("subscription_add", []string{origin.URL}, nil, false, "Source"))
	status := waitSourceOperation(t, m)
	require.Empty(t, status.Error)
	require.Equal(t, int32(1), direct.Load())
	require.Zero(t, proxied.Load())
	require.Equal(t, "Source", status.SubscriptionItems[0].Label)
	require.NoError(t, m.SetSubscriptionDownloadMode(t.Context(), SubscriptionDownloadProxy))
	require.NoError(t, m.Submit("subscription_refresh/"+subscriptionID(origin.URL), nil, false))
	status = waitSourceOperation(t, m)
	require.NotEmpty(t, status.Error)
	require.Equal(t, SubscriptionDownloadProxy, status.DownloadMode)
	require.Equal(t, int32(1), direct.Load())
	require.Equal(t, int32(1), proxied.Load())
	require.Equal(t, 1, status.Nodes)
}
