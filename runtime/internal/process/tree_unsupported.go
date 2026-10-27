//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris && !windows

package process

import (
	"errors"
	"os/exec"
)

type processTree struct{}

func startTree(*exec.Cmd) (*processTree, error) {
	return nil, errors.New("managed plugin processes are unsupported on this operating system")
}
func (*processTree) terminate() error                  { return nil }
func (*processTree) kill() error                       { return nil }
func (*processTree) close() error                      { return nil }
func (*processTree) expectedExit(*exec.ExitError) bool { return false }
