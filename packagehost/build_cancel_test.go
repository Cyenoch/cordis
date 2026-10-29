//go:build integration

package packagehost

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestCanceledBuildReapsCompilerDescendants(t *testing.T) {
	dir := t.TempDir()
	toolSource := `package main
import ("fmt"; "net"; "os"; "os/exec"; "strings")
func main() {
 if len(os.Args) > 2 && strings.HasPrefix(os.Args[2], "-V=") {
  cmd := exec.Command(os.Args[1], os.Args[2:]...)
  cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
  if err := cmd.Run(); err != nil { panic(err) }; return
 }
 child := len(os.Args) > 1 && os.Args[1] == "child"
 listener, err := net.Listen("tcp", "127.0.0.1:0")
 if err != nil { panic(err) }
 marker := os.Getenv("CORDIS_BUILD_READY")
 if child { marker += ".child" } else {
  cmd := exec.Command(os.Args[0], "child")
  cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
  if err := cmd.Start(); err != nil { panic(err) }
 }
 if err := os.WriteFile(marker, []byte(listener.Addr().String()), 0600); err != nil { panic(err) }
 for { c, err := listener.Accept(); if err != nil { fmt.Fprintln(os.Stderr, err); return }; c.Close() }
}`
	writeAuthorFile(t, dir, "tool.go", toolSource)
	tool := filepath.Join(t.TempDir(), "buildtool")
	if runtime.GOOS == "windows" {
		tool += ".exe"
	}
	build := exec.CommandContext(t.Context(), "go", "build", "-o", tool, filepath.Join(dir, "tool.go"))
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build compiler gate: %v\n%s", err, output)
	}
	writeAuthorFile(t, dir, "go.mod", "module canceled-build\n\ngo 1.27.3\n")
	writeAuthorFile(t, dir, "tool.go", "package main\nfunc main() {}\n")
	ready := filepath.Join(t.TempDir(), "ready")
	t.Setenv("CORDIS_BUILD_READY", ready)
	temporary := t.TempDir()
	t.Setenv("GOTMPDIR", temporary)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := runGo(ctx, dir, BuildOptions{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH},
			"build", "-toolexec", `"`+filepath.ToSlash(tool)+`"`, ".")
		done <- err
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("build descendants blocked cancellation")
		}
	})
	addresses := []string{}
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for _, path := range []string{ready, ready + ".child"} {
		for {
			raw, err := os.ReadFile(path)
			if err == nil && strings.Contains(string(raw), ":") {
				addresses = append(addresses, string(raw))
				break
			}
			select {
			case err := <-done:
				t.Fatalf("go build stopped before compiler gate: %v", err)
			case <-timer.C:
				t.Fatal("compiler gate did not start")
			case <-ticker.C:
			}
		}
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("build cancellation lost cause: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("inherited compiler pipes prevented build cancellation")
	}
	for _, address := range addresses {
		connection, err := net.DialTimeout("tcp", address, time.Second)
		if err == nil {
			connection.Close()
			t.Fatalf("compiler descendant still owns %s after cancellation", address)
		}
	}
	files, err := os.ReadDir(temporary)
	if err != nil || len(files) != 0 {
		t.Fatalf("canceled build retained its temporary workspace: %v %v", files, err)
	}
}
