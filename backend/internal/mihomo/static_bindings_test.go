package mihomo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func staticTestManager(t *testing.T) *Manager {
	t.Helper()
	m := New(t.TempDir())
	t.Cleanup(m.Close)
	require.NoError(t, os.WriteFile(filepath.Join(m.dir, "mihomo"), []byte("#!/bin/sh\nexit 0\n"), 0700))
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	t.Cleanup(controller.Close)
	m.controllerURL = controller.URL
	m.state.Installed, m.state.Running = true, true
	m.saved = saved{Secret: "fixture", Nodes: []map[string]any{
		{"name": "node-one", "type": "ss", "server": "127.0.0.1", "port": 1, "password": "fixture"},
		{"name": "DYNAMIC-two", "type": "http", "server": "127.0.0.1", "port": 2},
	}, NodeNames: map[string]string{"node-one": "订阅节点"}}
	return m
}

func staticTargets(t *testing.T, m *Manager) map[int]string {
	t.Helper()
	b, err := m.config(m.saved)
	require.NoError(t, err)
	var cfg struct {
		Listeners []struct {
			Name   string `json:"name"`
			Listen string `json:"listen"`
			Port   int    `json:"port"`
			Proxy  string `json:"proxy"`
		} `json:"listeners"`
	}
	require.NoError(t, json.Unmarshal(b, &cfg))
	result := map[int]string{}
	for _, listener := range cfg.Listeners {
		if listener.Port >= staticNodePortStart && listener.Port < staticNodePortEnd {
			require.Equal(t, "127.0.0.1", listener.Listen)
			result[listener.Port] = listener.Proxy
		}
	}
	return result
}

func TestStaticNodeBindingsPersistDeduplicateAndNeverRotate(t *testing.T) {
	m := staticTestManager(t)
	bindings, err := m.BindStaticNodes(t.Context(), []string{"node-one", "DYNAMIC-two", "node-one"})
	require.NoError(t, err)
	require.Len(t, bindings, 2)
	require.Equal(t, "订阅节点", bindings[0].DisplayName)
	require.NotEqual(t, bindings[0].Port, bindings[1].Port)
	targets := staticTargets(t, m)
	require.Equal(t, "node-one", targets[bindings[0].Port])
	require.Equal(t, "DYNAMIC-two", targets[bindings[1].Port])
	require.True(t, m.Status().NodeStates[0].StaticProxy)
	again, err := m.BindStaticNodes(t.Context(), []string{"node-one"})
	require.NoError(t, err)
	require.Equal(t, bindings[0].Port, again[0].Port)
	require.False(t, again[0].New)
	require.NoError(t, os.Remove(filepath.Join(m.dir, "mihomo")))
	restarted := New(m.dir)
	t.Cleanup(restarted.Close)
	require.Equal(t, targets, staticTargets(t, restarted))
	info, err := os.Stat(filepath.Join(m.dir, "settings.json"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), info.Mode().Perm())
}

func TestStaticNodeBindingsRejectUnavailableAndDoNotReassignPorts(t *testing.T) {
	m := staticTestManager(t)
	bindings, err := m.BindStaticNodes(t.Context(), []string{"node-one", "DYNAMIC-two"})
	require.NoError(t, err)
	for _, state := range []string{"disabled", "failed", "used"} {
		m.saved.Disabled = map[string]string{"node-one": state}
		require.Equal(t, "REJECT", staticTargets(t, m)[bindings[0].Port])
		_, err := m.BindStaticNodes(t.Context(), []string{"node-one"})
		require.Error(t, err)
	}
	m.saved.Disabled = nil
	m.saved.Nodes = m.saved.Nodes[1:]
	require.Equal(t, "REJECT", staticTargets(t, m)[bindings[0].Port])
	require.NoError(t, m.DeactivateStaticNodes(t.Context(), []string{"node-one"}))
	require.NotContains(t, staticTargets(t, m), bindings[0].Port)
	m.saved.Nodes = append(m.saved.Nodes, map[string]any{"name": "node-new", "type": "http", "server": "127.0.0.1", "port": 3})
	newBindings, err := m.BindStaticNodes(t.Context(), []string{"node-new"})
	require.NoError(t, err)
	require.NotEqual(t, bindings[0].Port, newBindings[0].Port)
	require.Equal(t, "DYNAMIC-two", staticTargets(t, m)[bindings[1].Port])
}

func TestStaticNodeBindingsValidateWholeBatchAndRollbackRejectedConfig(t *testing.T) {
	m := staticTestManager(t)
	_, err := m.BindStaticNodes(t.Context(), []string{"node-one", "missing"})
	require.ErrorIs(t, err, ErrUnknownNode)
	require.Empty(t, m.saved.StaticBindings)
	m.saved.CountryFilter = CountryFilter{Mode: "exclude", Codes: []string{"HK"}}
	m.saved.Countries = map[string]CountryObservation{"node-one": {Code: "HK"}}
	_, err = m.BindStaticNodes(t.Context(), []string{"node-one"})
	require.Error(t, err)
	m.saved.CountryFilter = CountryFilter{}
	require.NoError(t, os.WriteFile(filepath.Join(m.dir, "mihomo"), []byte("#!/bin/sh\nexit 1\n"), 0700))
	_, err = m.BindStaticNodes(t.Context(), []string{"node-one"})
	require.ErrorContains(t, err, "kernel rejected")
	require.Empty(t, m.saved.StaticBindings)
	require.False(t, m.Status().Busy)
}

func TestStaticNodeBindingsRejectInvalidSavedPorts(t *testing.T) {
	m := staticTestManager(t)
	for _, port := range []int{3101, 9098, staticNodePortEnd} {
		m.saved.StaticBindings = map[string]StaticBinding{"node-one": {Port: port, Enabled: true}}
		_, err := m.config(m.saved)
		require.Error(t, err, fmt.Sprintf("port %d", port))
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := m.BindStaticNodes(ctx, []string{"missing"})
	require.Error(t, err)
}

// Official Mihomo routes only through two loopback mock upstreams. There are
// no model calls, real provider requests or production changes in this test.
func TestStaticNodeBindingsWithOfficialKernel(t *testing.T) {
	m := officialKernelManager(t)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	nodes := make([]map[string]any, 0, 2)
	for i, name := range []string{"node-selected", "DYNAMIC-selected"} {
		label := fmt.Sprintf("exit-%d", i)
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodConnect {
				_, _ = io.WriteString(w, label)
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
			request, err := http.ReadRequest(buffer.Reader)
			if err != nil {
				return
			}
			_ = request.Body.Close()
			reply := &http.Response{StatusCode: http.StatusOK, ProtoMajor: 1, ProtoMinor: 1, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(label)), ContentLength: int64(len(label)), Close: true}
			_ = reply.Write(conn)
		}))
		t.Cleanup(upstream.Close)
		address, err := url.Parse(upstream.URL)
		require.NoError(t, err)
		port, err := strconv.Atoi(address.Port())
		require.NoError(t, err)
		nodes = append(nodes, map[string]any{"name": name, "type": "http", "server": address.Hostname(), "port": port})
	}
	require.NoError(t, m.run(ctx, "start", saved{Nodes: nodes, UseOnce: true}))
	bindings, err := m.BindStaticNodes(ctx, []string{"node-selected", "DYNAMIC-selected"})
	require.NoError(t, err)
	read := func(binding StaticNodeBinding) (int, string, error) {
		proxy, err := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", binding.Port))
		if err != nil {
			return 0, "", err
		}
		transport := &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true}
		defer transport.CloseIdleConnections()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:54321/static-check", nil)
		if err != nil {
			return 0, "", err
		}
		resp, err := (&http.Client{Transport: transport, Timeout: 5 * time.Second}).Do(req)
		if err != nil {
			return 0, "", err
		}
		defer func() { _ = resp.Body.Close() }()
		body, err := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body), err
	}
	check := func() {
		for i, binding := range bindings {
			code, body, err := read(binding)
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, code, body)
			require.Equal(t, fmt.Sprintf("exit-%d", i), body)
		}
	}
	check()
	m.stop()
	require.NoError(t, m.run(ctx, "start", m.saved))
	check()
	require.NoError(t, m.run(ctx, "disable/node-selected", m.saved))
	code, body, err := read(bindings[0])
	if err == nil {
		require.NotEqual(t, http.StatusOK, code)
		require.NotContains(t, body, "exit-1")
	}
	code, body, err = read(bindings[1])
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, "exit-1", body)
	require.NoError(t, m.DeactivateStaticNodes(ctx, []string{"node-selected"}))
	_, _, err = read(bindings[0])
	require.Error(t, err)
}
