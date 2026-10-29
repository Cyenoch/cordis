//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package processtree

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

type Tree struct{ pid int }

func Start(cmd *exec.Cmd) (*Tree, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &Tree{pid: cmd.Process.Pid}, nil
}
func (p *Tree) signal(signal syscall.Signal) error {
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
func (p *Tree) Terminate() error { return p.signal(syscall.SIGTERM) }
func (p *Tree) Kill() error      { return p.signal(syscall.SIGKILL) }
func (p *Tree) Close() error     { return nil }
func (p *Tree) ExpectedExit(err *exec.ExitError) bool {
	status, ok := err.Sys().(syscall.WaitStatus)
	return ok && status.Signaled() && (status.Signal() == syscall.SIGTERM || status.Signal() == syscall.SIGKILL)
}

func (*Tree) ExitSignal(state *os.ProcessState) string {
	status, ok := state.Sys().(syscall.WaitStatus)
	if ok && status.Signaled() {
		return status.Signal().String()
	}
	return ""
}
