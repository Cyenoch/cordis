package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseArchivePreservesContentsPermissionsAndExistingOutput(t *testing.T) {
	for _, extension := range []string{".zip", ".tar.gz"} {
		t.Run(extension, func(t *testing.T) {
			source := t.TempDir()
			for _, name := range []string{"cordis", "LICENSE", "RELEASE.txt"} {
				if err := os.WriteFile(filepath.Join(source, name), []byte(name), 0600); err != nil {
					t.Fatal(err)
				}
			}
			dest := filepath.Join(t.TempDir(), "release"+extension)
			if err := archiveFiles(dest, source, []string{"cordis", "LICENSE", "RELEASE.txt"}); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(dest)
			if err != nil {
				t.Fatal(err)
			}
			if err := archiveFiles(dest, source, []string{"cordis"}); !os.IsExist(err) {
				t.Fatalf("existing release was not rejected: %v", err)
			}
			after, err := os.ReadFile(dest)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("existing release overwritten")
			}
			check := func(name string, mode os.FileMode, reader io.Reader) {
				t.Helper()
				content, err := io.ReadAll(reader)
				if err != nil || string(content) != name {
					t.Fatalf("archive content mismatch: %s %v", name, err)
				}
				if name == "cordis" && mode.Perm() != 0755 {
					t.Fatalf("CLI lost executable mode: %v", mode)
				}
			}
			count := 0
			if extension == ".zip" {
				z, err := zip.OpenReader(dest)
				if err != nil {
					t.Fatal(err)
				}
				defer z.Close()
				for _, file := range z.File {
					reader, err := file.Open()
					if err != nil {
						t.Fatal(err)
					}
					check(file.Name, file.Mode(), reader)
					if err := reader.Close(); err != nil {
						t.Fatal(err)
					}
					count++
				}
			} else {
				stream, err := gzip.NewReader(bytes.NewReader(before))
				if err != nil {
					t.Fatal(err)
				}
				defer stream.Close()
				reader := tar.NewReader(stream)
				for {
					header, err := reader.Next()
					if err == io.EOF {
						break
					}
					if err != nil {
						t.Fatal(err)
					}
					check(header.Name, os.FileMode(header.Mode), reader)
					count++
				}
			}
			if count != 3 {
				t.Fatalf("missing release contents: %d", count)
			}
		})
	}
}

func TestReleaseArchiveFailureRemovesPartialOutput(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "release.tar.gz")
	if err := archiveFiles(dest, t.TempDir(), []string{"missing"}); err == nil {
		t.Fatal("missing source accepted")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("partial archive retained: %v", err)
	}
	if err := build("v1.0.0 -X unexpected", "unused"); err == nil || strings.Contains(err.Error(), "build") {
		t.Fatalf("invalid version not rejected before build: %v", err)
	}
}
