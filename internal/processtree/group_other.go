//go:build aix || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package processtree

func groupHasNoLiveMembers(int) (bool, error) { return false, nil }
