//go:build !linux && !darwin && !freebsd && !openbsd && !netbsd && !dragonfly && !windows

package requestcapture

import "errors"

func lockCaptureOwner(string) (func(), error) {
	return nil, errors.New("capture ownership is unsupported on this operating system")
}
