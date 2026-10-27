//go:build windows

package requestcapture

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func lockCaptureOwner(path string) (func(), error) {
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("symlink capture owner lock")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, errors.New("capture storage owner lock unavailable")
	}
	overlap := &windows.Overlapped{}
	if err = windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, overlap); err != nil {
		_ = f.Close()
		return nil, errors.New("capture storage is owned by another process; use a different stable slot")
	}
	return func() { _ = f.Close() }, nil
}
