//go:build integration && windows

package main

import (
	"os/exec"
	"testing"
)

// dumpStacksOnCancel leaves cmd as it is: Windows has no SIGQUIT.
func dumpStacksOnCancel(*testing.T, *exec.Cmd) {}
