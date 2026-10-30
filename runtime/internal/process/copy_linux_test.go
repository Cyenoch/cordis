package process

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

func TestConcurrentExecutableCopiesCanStart(t *testing.T) {
	source, err := exec.LookPath("true")
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	var workers sync.WaitGroup
	failures := make(chan error, 16)
	for worker := range 16 {
		workers.Go(func() {
			for attempt := range 32 {
				target := filepath.Join(directory, fmt.Sprintf("plugin-%d-%d", worker, attempt))
				err := copyExecutable(source, target)
				if err == nil {
					command := exec.CommandContext(t.Context(), target)
					command.Args[0] = "true"
					err = command.Run()
				}
				if err != nil {
					failures <- fmt.Errorf("start private executable: %w", err)
					return
				}
			}
		})
	}
	workers.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
}
