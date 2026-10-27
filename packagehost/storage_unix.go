//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package packagehost

import (
	"errors"
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func acquireLock(name string) (*os.File, error) {
	fd, err := unix.Open(name, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, fmt.Errorf("open storage lock: %w", err)
	}
	file := os.NewFile(uintptr(fd), name)
	if err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return nil, errors.Join(fmt.Errorf("storage already locked: %w", err), file.Close())
	}
	return file, nil
}
func releaseLock(file *os.File) error {
	return errors.Join(unix.Flock(int(file.Fd()), unix.LOCK_UN), file.Close())
}
func syncDir(name string) (err error) {
	file, err := os.Open(name)
	if err != nil {
		return err
	}
	return errors.Join(file.Sync(), file.Close())
}
func replaceFile(source, target string) error { return os.Rename(source, target) }
func checkLinks(file *os.File, info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("cannot determine package file link count")
	}
	if stat.Nlink > 1 {
		return fmt.Errorf("hard-linked package file %q", file.Name())
	}
	return nil
}
