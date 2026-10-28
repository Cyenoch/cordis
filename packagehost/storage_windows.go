//go:build windows

package packagehost

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func acquireLock(name string) (*os.File, error) {
	info, err := os.Lstat(name)
	if err == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("storage lock is a link")
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	file, err := os.OpenFile(name, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	var overlapped windows.Overlapped
	if err = windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlapped); err != nil {
		return nil, errors.Join(fmt.Errorf("storage already locked: %w", err), file.Close())
	}
	return file, nil
}
func releaseLock(file *os.File) error {
	var overlapped windows.Overlapped
	return errors.Join(windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &overlapped), file.Close())
}
func replaceFile(source, target string) error {
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

// Windows has no portable directory fsync. Every durable replacement uses
// MOVEFILE_WRITE_THROUGH; also flush the current root file when it exists.
func syncDir(name string) error {
	file, err := os.OpenFile(filepath.Join(name, "catalog.json"), os.O_RDWR, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return errors.Join(file.Sync(), file.Close())
}
func checkLinks(file *os.File, info os.FileInfo) error {
	var data windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &data); err != nil {
		return err
	}
	if data.NumberOfLinks > 1 {
		return fmt.Errorf("hard-linked package file %q", file.Name())
	}
	return nil
}
