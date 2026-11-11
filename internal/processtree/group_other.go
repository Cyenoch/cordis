//go:build aix || dragonfly || freebsd || netbsd || openbsd || solaris

package processtree

func groupHasNoLiveMembers(int) (bool, error) { return false, nil }

// awaitExit reports that this system offers no way to see an exit without
// reaping the process.
func awaitExit(int) bool { return false }
