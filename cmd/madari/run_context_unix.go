//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

func runExecutionContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), runExecutionSignals()...)
}

func runExecutionSignals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT}
}
