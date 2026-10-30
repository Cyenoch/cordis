package processtree

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

func groupHasNoLiveMembers(group int) (bool, error) {
	if err := syscall.Kill(-group, 0); errors.Is(err, syscall.ESRCH) {
		return true, nil
	} else if err != nil {
		return false, err
	}
	mounts, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return false, err
	}
	visible := false
	for _, line := range strings.Split(string(mounts), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 || fields[1] != "/proc" || fields[2] != "proc" {
			continue
		}
		visible = true
		for _, option := range strings.Split(fields[3], ",") {
			if strings.HasPrefix(option, "hidepid=") && option != "hidepid=0" && option != "hidepid=off" {
				return false, fmt.Errorf("cannot confirm process group drain with %s", option)
			}
		}
	}
	if !visible {
		return false, errors.New("cannot confirm /proc process visibility")
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		current, err := syscall.Getpgid(pid)
		if errors.Is(err, syscall.ESRCH) {
			continue
		}
		if err != nil {
			return false, err
		}
		if current != group {
			continue
		}
		data, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "stat"))
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ESRCH) {
			continue
		}
		if err != nil {
			return false, err
		}
		stat := string(data)
		fields := strings.Fields(stat[strings.LastIndexByte(stat, ')')+1:])
		if len(fields) < 18 {
			return false, fmt.Errorf("missing process state for %d", pid)
		}
		threads, err := strconv.Atoi(fields[17])
		if err != nil {
			return false, fmt.Errorf("read thread count for %d: %w", pid, err)
		}
		// An orphan zombie may await the container's init indefinitely, but it
		// has released its resources once the remaining threads have exited.
		if (fields[0] != "Z" && fields[0] != "X") || threads > 1 {
			return false, nil
		}
	}
	return true, nil
}
