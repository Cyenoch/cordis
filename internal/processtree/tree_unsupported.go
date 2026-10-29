//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris && !windows

package processtree

import (
	"errors"
	"os"
	"os/exec"
)

type Tree struct{}

func Start(*exec.Cmd) (*Tree, error) {
	return nil, errors.New("managed process trees are unsupported on this operating system")
}
func (*Tree) Terminate() error                   { return nil }
func (*Tree) Kill() error                        { return nil }
func (*Tree) Close() error                       { return nil }
func (*Tree) ExpectedExit(*exec.ExitError) bool  { return false }
func (*Tree) ExitSignal(*os.ProcessState) string { return "" }
