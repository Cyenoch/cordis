//go:build !windows

package main

import "os"

func renameOutput(source, target string) error { return os.Rename(source, target) }
