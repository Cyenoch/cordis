//go:build integration && unix

package packagehost

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestFIFOArchiveDoesNotBlockStore(t *testing.T) {
	t.Parallel()
	s := openFixture(t, t.TempDir(), TrustPolicy{})
	name := filepath.Join(t.TempDir(), "input.zip")
	if err := unix.Mkfifo(name, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := s.Install(ctx, name); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("FIFO accepted as archive")
		}
	case <-ctx.Done():
		// Release a regressed blocking open so the test never leaks its goroutine.
		fd, err := unix.Open(name, unix.O_RDWR|unix.O_NONBLOCK, 0600)
		if err != nil {
			t.Fatal(err)
		}
		defer unix.Close(fd)
		<-done
		t.Fatal("FIFO held the Store after cancellation")
	}
	if _, err := s.Installed(); err != nil {
		t.Fatal(err)
	}
}
