//go:build windows

package access

import "golang.org/x/sys/windows"

func replaceNamespaceFile(source, target string) error {
	from, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}

// MoveFileEx writes through the replacement; Windows does not support POSIX directory fsync.
func syncNamespaceDirectory(string) error { return nil }
