//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package main

import (
	"os"
	"reflect"
	"syscall"
	"testing"
)

func TestRunExecutionSignalsIncludeTerminalDisconnects(t *testing.T) {
	want := []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT}
	if got := runExecutionSignals(); !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected bounded-run signals: got %#v want %#v", got, want)
	}
}
