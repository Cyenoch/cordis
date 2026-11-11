package processtree

import (
	"errors"
	"time"

	"golang.org/x/sys/unix"
)

// awaitExit blocks until the process has exited, leaving it unreaped.
func awaitExit(pid int) bool {
	queue, err := unix.Kqueue()
	if err != nil {
		return false
	}
	defer unix.Close(queue)
	watch := []unix.Kevent_t{{Ident: uint64(pid), Filter: unix.EVFILT_PROC, Flags: unix.EV_ADD | unix.EV_ONESHOT, Fflags: unix.NOTE_EXIT}}
	events := make([]unix.Kevent_t, 1)
	for {
		n, err := unix.Kevent(queue, watch, events, nil)
		switch {
		case errors.Is(err, unix.EINTR):
			watch = nil // registered already; only wait
		case errors.Is(err, unix.ESRCH):
			return true // exited before the watch was set
		case err != nil:
			return false
		case n > 0 && events[0].Flags&unix.EV_ERROR != 0:
			return unix.Errno(events[0].Data) == unix.ESRCH
		case n > 0:
			return true
		}
	}
}

func groupHasNoLiveMembers(pid int) (bool, error) {
	deadline := time.Now().Add(100 * time.Millisecond)
	for {
		members, err := unix.SysctlKinfoProcSlice("kern.proc.pgrp", pid)
		if err != nil {
			return false, err
		}
		alive := false
		for _, member := range members {
			// SZOMB=5. Do not hide EPERM while any live member remains.
			if member.Proc.P_stat != 5 {
				alive = true
				break
			}
		}
		if !alive {
			return true, nil
		}
		if !time.Now().Before(deadline) {
			return false, nil
		}
		time.Sleep(time.Millisecond)
	}
}
