package main

import (
	"encoding/binary"
	"errors"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

func createControlFile(path string) (*os.File, error) {
	name, err := unix.BytePtrFromString(path)
	if err != nil {
		return nil, err
	}
	// open_extended installs an empty non-inheriting ACL atomically. Clearing an
	// inherited ACL after creation cannot revoke another user's open read handle.
	security := struct {
		magic          uint32
		owner, group   [16]byte
		entries, flags uint32
	}{magic: 0x012cc16d, flags: 1 << 17} // KAUTH_FILESEC_MAGIC, KAUTH_ACL_NO_INHERIT
	fd, _, errno := unix.Syscall6(unix.SYS_OPEN_EXTENDED, uintptr(unsafe.Pointer(name)),
		uintptr(unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC), uintptr(os.Geteuid()), uintptr(os.Getegid()),
		0600, uintptr(unsafe.Pointer(&security)))
	if errno != 0 {
		return nil, &os.PathError{Op: "create private connection", Path: path, Err: errno}
	}
	return os.NewFile(fd, path), nil
}

func checkControlACL(file *os.File) error {
	attributes := unix.Attrlist{Bitmapcount: 5, Commonattr: unix.ATTR_CMN_EXTENDED_SECURITY}
	// Darwin supports at most 128 ACL entries (24 bytes each), plus filesec.
	var data [4096]byte
	_, _, errno := unix.Syscall6(unix.SYS_FGETATTRLIST, file.Fd(), uintptr(unsafe.Pointer(&attributes)),
		uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)), 0, 0)
	runtime.KeepAlive(file)
	if errno != 0 {
		return errno
	}
	size := int(binary.LittleEndian.Uint32(data[:4]))
	offset := 4 + int(int32(binary.LittleEndian.Uint32(data[4:8])))
	length := int(binary.LittleEndian.Uint32(data[8:12]))
	if length == 0 {
		return nil
	}
	if size < 12 || size > len(data) || offset < 12 || length < 44 || offset > size-length {
		return errors.New("invalid connection file security attributes")
	}
	// kauth_filesec has magic, owner/group UUIDs, then the ACL entry count.
	count := binary.LittleEndian.Uint32(data[offset+36 : offset+40])
	if count != 0 && count != ^uint32(0) {
		return errors.New("connection file has an extended ACL; choose a directory without inherited ACLs")
	}
	return nil
}
