//go:build !unix && !windows

package access

import "errors"

func replaceNamespaceFile(string, string) error {
	return errors.New("durable namespace replacement unsupported on this platform")
}

func syncNamespaceDirectory(string) error {
	return errors.New("durable namespace synchronization unsupported on this platform")
}
