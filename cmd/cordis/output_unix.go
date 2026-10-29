//go:build !windows

package main

import (
	"os"
	"os/signal"
	"syscall"
)

func renameOutput(source, target string) error { return os.Rename(source, target) }

func ignoreBrokenPipe() { signal.Ignore(syscall.SIGPIPE) }
