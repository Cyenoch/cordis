package main

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestAtomicOutputKeepsOldFileWhenRenameIsDenied(t *testing.T) {
	path := filepath.Join(t.TempDir(), "api.go")
	old := "package previous\n"
	if err := os.WriteFile(path, []byte(old), 0644); err != nil {
		t.Fatal(err)
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(handle)
	if err := atomicOutput(path, []byte("package replacement\n")); err == nil {
		t.Fatal("replaced a file whose open handle denies deletion")
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != old {
		t.Fatalf("failed rename changed old content: %q, %v", got, err)
	}
}
