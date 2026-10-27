package process

import (
	"golang.org/x/sys/unix"
	"time"
)

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
