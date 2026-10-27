//go:build aix || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package process

func groupHasNoLiveMembers(int) (bool, error) { return false, nil }
