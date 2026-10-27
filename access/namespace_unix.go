//go:build unix

package access

import (
	"errors"
	"os"
)

func replaceNamespaceFile(source, target string) error { return os.Rename(source, target) }

func syncNamespaceDirectory(name string) error {
	directory, err := os.Open(name)
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}
