package main

import (
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestControlWindowsDACLIsPrivateAtCreation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "connection.json")
	file, err := createControlFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := checkControlFile(file); err != nil {
		t.Fatalf("new file is exposed before writing: %v", err)
	}
	public, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := public.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
	if err := checkControlFile(file); err == nil {
		t.Fatal("world-readable DACL was accepted")
	}
}
