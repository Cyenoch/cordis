//go:build unix

package packagehost

import "golang.org/x/sys/unix"

// Nonblocking open prevents a raced-in FIFO from holding the Store mutex forever.
const regularOpenFlags = unix.O_NONBLOCK | unix.O_NOFOLLOW
