//go:build integration && unix

package main

import (
	"bytes"
	"os/exec"
	"strconv"
	"syscall"
	"testing"
)

// dumpStacksOnCancel runs cmd in its own process group and, when its
// context ends, logs the state of every process in the group and sends the
// group SIGQUIT: each Go process that reached main prints its goroutines to
// the output the test reports. The logged state covers a process that
// never got that far and so prints nothing.
func dumpStacksOnCancel(t *testing.T, cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		group := strconv.Itoa(cmd.Process.Pid)
		listing, err := exec.Command("ps", "-A", "-o", "pgid=,pid=,ppid=,stat=,etime=,command=").Output()
		if err != nil {
			t.Logf("ps: %v", err)
		}
		var members []byte
		for line := range bytes.Lines(listing) {
			if fields := bytes.Fields(line); len(fields) > 0 && string(fields[0]) == group {
				members = append(members, line...)
			}
		}
		t.Logf("process group %s when canceled (pgid pid ppid stat etime command):\n%s", group, members)
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGQUIT)
	}
}
