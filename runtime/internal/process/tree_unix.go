//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package process

import (
	"errors"
	"fmt"
	"os/exec"
	"syscall"
)

type processTree struct{ pid int }

func startTree(cmd *exec.Cmd) (*processTree, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &processTree{pid: cmd.Process.Pid}, nil
}
func (p *processTree) signal(signal syscall.Signal) error {
	err := syscall.Kill(-p.pid, signal)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	if errors.Is(err, syscall.EPERM) {
		empty, probeErr := groupHasNoLiveMembers(p.pid)
		if probeErr != nil {
			return errors.Join(err, probeErr)
		}
		if empty {
			return nil
		}
	}
	if err != nil {
		return fmt.Errorf("signal process group %d with %d: %w", p.pid, signal, err)
	}
	return nil
}
func (p *processTree) terminate() error { return p.signal(syscall.SIGTERM) }
func (p *processTree) kill() error      { return p.signal(syscall.SIGKILL) }
func (p *processTree) close() error     { return nil }
func (p *processTree) expectedExit(err *exec.ExitError) bool {
	status, ok := err.Sys().(syscall.WaitStatus)
	return ok && status.Signaled() && (status.Signal() == syscall.SIGTERM || status.Signal() == syscall.SIGKILL)
}
