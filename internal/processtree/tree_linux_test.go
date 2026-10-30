package processtree

import (
	"os/exec"
	"testing"
	"time"
)

func TestCloseWaitsForDescendantAfterLeaderExit(t *testing.T) {
	command := exec.Command("sh", "-c", "sleep 60 >/dev/null 2>&1 &")
	tree, err := Start(command)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := tree.Kill(); err != nil {
			t.Error(err)
		}
	}()
	if err := command.Wait(); err != nil {
		t.Fatal(err)
	}
	closed := make(chan error, 1)
	go func() { closed <- tree.Close() }()
	early := false
	select {
	case err := <-closed:
		early = true
		t.Errorf("Close completed while descendant was still running: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := tree.Kill(); err != nil {
		t.Fatal(err)
	}
	if !early {
		select {
		case err := <-closed:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Close did not finish after killing the descendant")
		}
	}
}
