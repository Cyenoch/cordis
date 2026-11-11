//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package processtree

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"syscall"
	"time"
)

// Tree is a process and the group it leads. The group is addressed by the
// leader's id, which the system may hand to another process once the leader
// has been reaped; Wait ends the group before that and the tree signals
// nobody afterwards.
type Tree struct {
	pid int

	mu      sync.Mutex
	settled bool // the group was ended before its leader was reaped
}

func Start(cmd *exec.Cmd) (*Tree, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &Tree{pid: cmd.Process.Pid}, nil
}

// Wait waits for the leader to exit and reaps it. Where the system can tell
// that the leader has exited without reaping it, what is left of the group
// is ended first, while the leader's id still stands for this group.
func (p *Tree) Wait(cmd *exec.Cmd) error {
	exited := awaitExit(p.pid)
	p.mu.Lock()
	defer p.mu.Unlock()
	if exited {
		err := syscall.Kill(-p.pid, syscall.SIGKILL)
		if errors.Is(err, syscall.EPERM) && runtime.GOOS == "darwin" {
			// What Darwin answers when only zombies are left in the group.
			if empty, probeErr := groupHasNoLiveMembers(p.pid); probeErr == nil && empty {
				err = nil
			}
		}
		p.settled = err == nil || errors.Is(err, syscall.ESRCH)
	}
	return cmd.Wait()
}

func (p *Tree) signal(signal syscall.Signal) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.settled {
		return nil
	}
	err := syscall.Kill(-p.pid, signal)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	if errors.Is(err, syscall.EPERM) && runtime.GOOS == "darwin" {
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
func (p *Tree) Close() error {
	if runtime.GOOS != "linux" {
		return nil
	}
	p.mu.Lock()
	settled := p.settled
	p.mu.Unlock()
	if settled {
		// Killed before the leader was reaped; its id says nothing now.
		return nil
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		empty, err := groupHasNoLiveMembers(p.pid)
		if err != nil {
			return fmt.Errorf("query process group drain: %w", err)
		}
		if empty {
			return nil
		}
		if !time.Now().Before(deadline) {
			return errors.New("process group still has live members after termination")
		}
		time.Sleep(time.Millisecond)
	}
}
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
