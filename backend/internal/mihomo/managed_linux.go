//go:build linux

package mihomo

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func lockFile(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, errors.New("managed lock unavailable")
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, errors.New("managed kernel is owned by another process")
	}
	return f, nil
}

func lockHostConfiguration(ctx context.Context, path string) (func(), error) {
	for {
		f, err := lockFile(path)
		if err == nil {
			return func() { _ = f.Close() }, nil
		}
		select {
		case <-ctx.Done():
			return nil, errors.New("managed configuration is busy")
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func lockHostOwner(dir string) (*os.File, func(), error) {
	if err := safeManagedSupervisor(); err != nil {
		return nil, nil, err
	}
	f, err := lockFile(filepath.Join(dir, "owner.lock"))
	if err != nil {
		return nil, nil, err
	}
	return f, func() { _ = f.Close() }, nil
}

func configureManagedChild(cmd *exec.Cmd, owner *os.File) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	// The independent kernel inherits the open-file-description lock. It stays
	// held even when the originating application exits during a rolling update.
	cmd.ExtraFiles = []*os.File{owner}
}

func terminateManagedKernel(dir string, pid int) error {
	actual, err := os.Readlink(filepath.Join("/proc", strconv.Itoa(pid), "exe"))
	expected, absErr := filepath.Abs(filepath.Join(dir, "mihomo"))
	if err != nil || absErr != nil || actual != expected {
		return errors.New("managed process identity does not match")
	}
	return syscall.Kill(pid, syscall.SIGTERM)
}

func safeManagedSupervisor() error {
	data, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return errors.New("cannot verify supervisor isolation")
	}
	unit := ""
	for _, line := range strings.Split(string(data), "\n") {
		for _, part := range strings.Split(line, "/") {
			if strings.HasSuffix(part, ".service") {
				unit = part
			}
		}
	}
	if unit == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	mode, err := exec.CommandContext(ctx, "systemctl", "show", "--property=KillMode", "--value", unit).Output()
	if err != nil || strings.TrimSpace(string(mode)) != "process" {
		return errors.New("managed kernel requires an independent supervisor or application KillMode=process before start")
	}
	return nil
}
