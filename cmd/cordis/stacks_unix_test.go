//go:build integration && unix

package main

import (
	"os/exec"
	"syscall"
)

// dumpStacksOnCancel runs cmd in its own process group and, when its
// context ends, sends the group SIGQUIT: every Go process in it prints its
// goroutines to the output the test reports.
func dumpStacksOnCancel(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGQUIT) }
}
