package mihomo

import (
	"context"
	"encoding/json"
	"net/netip"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestManagedExposureRejectsPublicAndBroadSources(t *testing.T) {
	for _, e := range []ManagedExposure{
		{Listen: "0.0.0.0", Advertise: "0.0.0.0", AllowedCIDRs: []string{"10.0.0.0/8"}},
		{Listen: "127.0.0.1", Advertise: "127.0.0.1", AllowedCIDRs: []string{"127.0.0.0/8"}},
		{Listen: "8.8.8.8", Advertise: "8.8.8.8", AllowedCIDRs: []string{"10.0.0.0/8"}},
		{Listen: "10.0.0.2", Advertise: "10.0.0.3", AllowedCIDRs: []string{"10.0.0.0/8"}},
		{Listen: "10.0.0.2", Advertise: "10.0.0.2", AllowedCIDRs: []string{"0.0.0.0/0"}},
		{Listen: "10.0.0.2", Advertise: "10.0.0.2", AllowedCIDRs: []string{"10.0.0.0/7"}},
		{Listen: "10.0.0.2", Advertise: "10.0.0.2"},
	} {
		require.Error(t, validateManagedExposure(e))
	}
	require.NoError(t, validateManagedExposure(ManagedExposure{}))
	require.NoError(t, validateManagedExposure(ManagedExposure{Listen: "10.0.0.2", Advertise: "10.0.0.2", AllowedCIDRs: []string{"10.0.0.0/24"}}))
}

func TestManagedConstructorDoesNotStartInstalledKernel(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "mihomo"), []byte("fixture"), 0700))
	raw, _ := json.Marshal(saved{Nodes: []map[string]any{{"name": "fixture"}}, Secret: "fixture"})
	require.NoError(t, os.WriteFile(filepath.Join(dir, "settings.json"), raw, 0600))
	m := NewManaged(dir)
	defer m.Close()
	require.Nil(t, m.cmd)
	require.False(t, m.state.Busy)
	require.False(t, m.state.Running)
}

func TestManagedStaticImportIsOptIn(t *testing.T) {
	m := NewManaged(t.TempDir())
	defer m.Close()
	m.state.Installed = true
	m.state.Running = true
	_, err := m.BindStaticNodes(context.Background(), []string{"fixture"})
	require.ErrorContains(t, err, "configure a private listener")
}

func TestManagedListenerRequiresAuthenticationAndPrivateSources(t *testing.T) {
	m := NewManaged(t.TempDir())
	defer m.Close()
	s := saved{Exposure: ManagedExposure{Listen: "10.0.0.2", Advertise: "10.0.0.2", AllowedCIDRs: []string{"10.0.0.0/24"}}, Nodes: []map[string]any{{"name": "fixture"}}, StaticBindings: map[string]StaticBinding{"fixture": {Port: 32001, Enabled: true}}}
	_, err := m.config(s)
	require.ErrorContains(t, err, "require authentication")
	s.StaticBindings["fixture"] = StaticBinding{Port: 32001, Enabled: true, Username: "mh_fixture", Password: "random-secret"}
	raw, err := m.config(s)
	require.NoError(t, err)
	var cfg map[string]any
	require.NoError(t, json.Unmarshal(raw, &cfg))
	require.Equal(t, "127.0.0.1:9098", cfg["external-controller"])
	require.Equal(t, "127.0.0.1", cfg["bind-address"])
	require.Contains(t, cfg["lan-allowed-ips"], "10.0.0.0/24")
	listeners, ok := cfg["listeners"].([]any)
	require.True(t, ok)
	for _, value := range listeners {
		listener, ok := value.(map[string]any)
		require.True(t, ok)
		if listener["name"] == "STATIC-fixture" {
			require.Equal(t, "10.0.0.2", listener["listen"])
			require.NotEmpty(t, listener["users"])
		}
	}
}

func TestSubscriptionRejectsPrivateAndTranslationRanges(t *testing.T) {
	for _, raw := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "100.100.100.200", "::1", "::ffff:127.0.0.1", "2001:db8::1", "2002:7f00:1::1", "64:ff9b::7f00:1"} {
		require.False(t, publicSubscriptionIP(netip.MustParseAddr(raw)), raw)
	}
	require.True(t, publicSubscriptionIP(netip.MustParseAddr("8.8.8.8")))
	_, _, err := publicSubscriptionTarget(context.Background(), "http://127.0.0.1/latest/meta-data")
	require.Error(t, err)
}
