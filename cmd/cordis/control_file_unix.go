//go:build !windows

package main

import (
	"errors"
	"os"
	"syscall"
)

func openControlFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
}

func checkControlFile(file *os.File) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 ||
		stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 {
		return errors.New("connection file must be private, owned by the current user, and not linked")
	}
	return checkControlACL(file)
}
