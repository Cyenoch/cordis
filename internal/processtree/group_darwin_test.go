package processtree

import (
	"os/exec"
	"testing"
	"time"
)

// A lost exit event is recovered by spotting the zombie: an exited, unreaped
// process counts as exited, a running one does not.
func TestExitedSeesTheZombie(t *testing.T) {
	running := exec.Command("sleep", "10")
	finished := exec.Command("true")
	for _, cmd := range []*exec.Cmd{running, finished} {
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = running.Process.Kill(); _ = running.Wait(); _ = finished.Wait() })
	deadline := time.Now().Add(5 * time.Second)
	for !exited(finished.Process.Pid) {
		if time.Now().After(deadline) {
			t.Fatal("an exited process was not seen as exited")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if exited(running.Process.Pid) {
		t.Fatal("a running process was seen as exited")
	}
}
