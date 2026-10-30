//go:build !darwin && !windows

package main

import "os"

func createControlFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
}

func checkControlACL(*os.File) error { return nil }
