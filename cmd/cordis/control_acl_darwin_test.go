package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestControlCreationDoesNotInheritDarwinACL(t *testing.T) {
	s, _ := testControl(t)
	directory := t.TempDir()
	if output, err := exec.Command("/bin/chmod", "+a", "everyone allow read,readsecurity,writesecurity,file_inherit", directory).CombinedOutput(); err != nil {
		t.Fatalf("create inheritable ACL: %v %s", err, output)
	}
	path := filepath.Join(directory, "connection.json")
	file, err := createControlFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := checkControlFile(file); err != nil {
		t.Fatalf("new empty file already exposes a handle through its inherited ACL: %v", err)
	}
	server, err := startControl(t.Context(), s.host, filepath.Join(directory, "session.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer server.close()
	if _, err := readControlConnection(server.path); err != nil {
		t.Fatal(err)
	}
	inherited := filepath.Join(directory, "inherited.json")
	if err := os.WriteFile(inherited, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readControlConnection(inherited); err == nil {
		t.Fatal("client accepted a connection file with inherited ACL")
	}
}
