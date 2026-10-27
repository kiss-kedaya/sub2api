//go:build !linux

package mihomo

import (
	"context"
	"errors"
	"os"
	"os/exec"
)

func lockHostConfiguration(context.Context, string) (func(), error) {
	return nil, errors.New("managed kernel requires Linux")
}
func lockHostOwner(string) (*os.File, func(), error) {
	return nil, nil, errors.New("managed kernel requires Linux")
}
func configureManagedChild(*exec.Cmd, *os.File) {}
func terminateManagedKernel(string, int) error  { return errors.New("managed kernel requires Linux") }
