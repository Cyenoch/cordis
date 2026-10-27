//go:build !windows

package process

import "os"

func removeActivationDirectory(path string) error { return os.RemoveAll(path) }
