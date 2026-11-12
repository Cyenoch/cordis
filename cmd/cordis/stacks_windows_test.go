//go:build integration && windows

package main

import "os/exec"

// dumpStacksOnCancel leaves cmd as it is: Windows has no SIGQUIT.
func dumpStacksOnCancel(*exec.Cmd) {}
