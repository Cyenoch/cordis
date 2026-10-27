package packagehost

import (
	"errors"
	"fmt"
	"os"
)

func openRegular(root *os.Root, name string) (*os.File, error) {
	lstat := os.Lstat
	open := os.OpenFile
	if root != nil {
		lstat = root.Lstat
		open = root.OpenFile
	}
	info, err := lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("package file is not regular: %q", name)
	}
	f, err := open(name, os.O_RDONLY|regularOpenFlags, 0)
	if err != nil {
		return nil, err
	}
	info, err = f.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = fmt.Errorf("package file changed to nonregular: %q", name)
	}
	if err != nil {
		return nil, errors.Join(err, f.Close())
	}
	return f, nil
}
