//go:build linux

package mihomo

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestManagedConfigurationLocksAcrossApplicationInstances(t *testing.T) {
	dir := t.TempDir()
	a := NewManaged(dir)
	b := NewManaged(dir)
	defer a.Close()
	defer b.Close()
	require.NoError(t, a.acquire(context.Background()))
	ctx, cancel := context.WithTimeout(context.Background(), 70*time.Millisecond)
	defer cancel()
	require.Error(t, b.acquire(ctx))
	a.release()
	require.NoError(t, b.acquire(context.Background()))
	b.release()
}

func TestManagedStaleApplicationCannotOverwriteSettings(t *testing.T) {
	dir := t.TempDir()
	m := NewManaged(dir)
	defer m.Close()
	raw, _ := json.Marshal(saved{DownloadMode: SubscriptionDownloadDirect})
	require.NoError(t, os.WriteFile(filepath.Join(dir, "settings.json"), raw, 0600))
	require.ErrorContains(t, m.acquire(context.Background()), "changed in another instance")
	require.Equal(t, SubscriptionDownloadDirect, m.saved.DownloadMode)
	require.NoError(t, m.acquire(context.Background()))
	m.release()
}

func TestManagedCloseKeepsKernelProcessSeparate(t *testing.T) {
	m := NewManaged(t.TempDir())
	cmd := exec.Command("/bin/sleep", "30")
	require.NoError(t, cmd.Start())
	m.cmd = cmd
	m.done = make(chan struct{})
	go func() { _ = cmd.Wait(); close(m.done) }()
	t.Cleanup(func() { _ = cmd.Process.Kill(); <-m.done })
	m.state.Running = true
	m.Close()
	require.NoError(t, cmd.Process.Signal(syscall.Signal(0)), "rolling application shutdown killed its proxy child")
}
