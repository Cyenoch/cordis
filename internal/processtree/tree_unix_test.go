//go:build darwin || linux

package processtree

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A leader that exits leaves a descendant behind in its group. Wait ends the
// descendant before it reaps the leader, while the leader's id still stands
// for the group, and the tree signals nobody after that: the id may have
// been handed to another process.
func TestWaitEndsTheGroupBeforeReapingItsLeader(t *testing.T) {
	record := filepath.Join(t.TempDir(), "descendant")
	command := exec.Command("sh", "-c", `sleep 60 >/dev/null 2>&1 & echo $! > "$0"`, record)
	tree, err := Start(command)
	if err != nil {
		t.Fatal(err)
	}
	if err := tree.Wait(command); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	descendant, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	// Gone, or a zombie waiting for init; either way it no longer runs.
	deadline := time.Now().Add(10 * time.Second)
	for running(descendant) {
		if time.Now().After(deadline) {
			t.Fatal("the descendant outlived the leader's reaping")
		}
		time.Sleep(time.Millisecond)
	}
	if !tree.settled {
		t.Fatal("the tree may still signal a group whose id is free to be reused")
	}
	if err := errors.Join(tree.Terminate(), tree.Kill(), tree.Close()); err != nil {
		t.Fatalf("a settled tree: %v", err)
	}
}

// running reports whether pid is a process that still executes.
func running(pid int) bool {
	if err := syscall.Kill(pid, 0); err != nil {
		return false
	}
	state, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	return err == nil && !strings.HasPrefix(strings.TrimSpace(string(state)), "Z") && strings.TrimSpace(string(state)) != ""
}
