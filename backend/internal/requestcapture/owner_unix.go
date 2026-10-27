//go:build linux || darwin || freebsd || openbsd || netbsd || dragonfly

package requestcapture

import (
	"errors"
	"os"
	"syscall"
)

func lockCaptureOwner(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, errors.New("capture storage owner lock unavailable")
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, errors.New("capture storage is owned by another process; use a different stable slot")
	}
	return func() { _ = f.Close() }, nil
}
