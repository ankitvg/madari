//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package proctree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestRunTimeoutKillsSeparateProcessGroupDescendant(t *testing.T) {
	info := runUnixDescendantTimeout(t, "setpgid")
	if info.childPGID != info.childPID {
		t.Fatalf("helper child did not create a separate process group: pid=%d pgid=%d", info.childPID, info.childPGID)
	}
	if info.childSID != info.parentPID {
		t.Fatalf("helper child escaped or never joined the contained session: child sid=%d parent pid=%d", info.childSID, info.parentPID)
	}
	assertProcessCannotExecute(t, info.childPID, "separate-process-group child")
}

func TestRunTimeoutKillsSeparateSessionDescendant(t *testing.T) {
	info := runUnixDescendantTimeout(t, "setsid")
	if info.childPGID != info.childPID || info.childSID != info.childPID {
		t.Fatalf("helper child did not create a separate session: pid=%d pgid=%d sid=%d", info.childPID, info.childPGID, info.childSID)
	}
	if info.childSID == info.parentPID {
		t.Fatalf("setsid helper unexpectedly remained in parent session %d", info.parentPID)
	}
	assertProcessCannotExecute(t, info.childPID, "separate-session child")
}

type unixDescendantInfo struct {
	childPID  int
	childPGID int
	childSID  int
	parentPID int
}

func runUnixDescendantTimeout(t *testing.T, containment string) unixDescendantInfo {
	t.Helper()
	tempDir := t.TempDir()
	infoPath := filepath.Join(tempDir, "process-info")
	readyPath := filepath.Join(tempDir, "child-ready")
	cmd := exec.Command(os.Args[0], "-test.run=^TestProctreeUnixTreeHelper$")
	cmd.Env = append(os.Environ(),
		"PROCTREE_UNIX_HELPER=parent",
		"PROCTREE_UNIX_INFO="+infoPath,
		"PROCTREE_UNIX_READY="+readyPath,
		"PROCTREE_UNIX_CHILD_CONTAINMENT="+containment,
	)

	result, err := Run(context.Background(), cmd, time.Second)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected timeout error, got: %v", err)
	}
	if result.Outcome != OutcomeTimeout || result.Termination == nil || result.Termination.TreeTermination != TreeTerminationCompleted {
		t.Fatalf("unexpected timeout result: %#v", result)
	}

	payload, readErr := os.ReadFile(infoPath)
	if readErr != nil {
		t.Fatalf("read descendant process info: %v", readErr)
	}
	fields := strings.Fields(string(payload))
	if len(fields) != 4 {
		t.Fatalf("unexpected descendant process info %q", payload)
	}
	values := make([]int, len(fields))
	for i, field := range fields {
		values[i], readErr = strconv.Atoi(field)
		if readErr != nil {
			t.Fatalf("parse descendant process info %q: %v", payload, readErr)
		}
	}
	return unixDescendantInfo{childPID: values[0], childPGID: values[1], childSID: values[2], parentPID: values[3]}
}

func assertProcessCannotExecute(t *testing.T, childPID int, label string) {
	t.Helper()
	defer func() { _ = unix.Kill(childPID, unix.SIGKILL) }()
	deadline := time.Now().Add(3 * time.Second)
	for processCanExecute(childPID) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if processCanExecute(childPID) {
		t.Fatalf("%s %d survived process-tree timeout", label, childPID)
	}
}

func TestRunRejectsConflictingProcessGroupSettings(t *testing.T) {
	cmd := helperCommand(t, "success")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	result, err := Run(context.Background(), cmd, time.Second)
	if err == nil || !strings.Contains(err.Error(), "incompatible with isolated session containment") {
		t.Fatalf("expected incompatible process-group error, got: %v", err)
	}
	if result.Outcome != OutcomeStartFailed || result.ProcessStarted || result.Termination != nil {
		t.Fatalf("conflicting process-group settings should fail closed: %#v", result)
	}
}

func TestProctreeUnixTreeHelper(t *testing.T) {
	role := os.Getenv("PROCTREE_UNIX_HELPER")
	if role == "" {
		return
	}
	if role == "child" {
		signalIgnoreTERM()
		if err := os.WriteFile(os.Getenv("PROCTREE_UNIX_READY"), []byte("ready\n"), 0o600); err != nil {
			os.Exit(81)
		}
		for {
			time.Sleep(time.Second)
		}
	}

	child := exec.Command(os.Args[0], "-test.run=^TestProctreeUnixTreeHelper$")
	child.Env = append(os.Environ(),
		"PROCTREE_UNIX_HELPER=child",
		"PROCTREE_UNIX_READY="+os.Getenv("PROCTREE_UNIX_READY"),
	)
	switch os.Getenv("PROCTREE_UNIX_CHILD_CONTAINMENT") {
	case "setpgid":
		child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	case "setsid":
		child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	default:
		os.Exit(87)
	}
	if err := child.Start(); err != nil {
		os.Exit(82)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(os.Getenv("PROCTREE_UNIX_READY")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = child.Process.Kill()
			os.Exit(83)
		}
		time.Sleep(10 * time.Millisecond)
	}
	pgid, err := unix.Getpgid(child.Process.Pid)
	if err != nil {
		_ = child.Process.Kill()
		os.Exit(84)
	}
	sid, err := unix.Getsid(child.Process.Pid)
	if err != nil {
		_ = child.Process.Kill()
		os.Exit(85)
	}
	info := fmt.Sprintf("%d %d %d %d\n", child.Process.Pid, pgid, sid, os.Getpid())
	if err := os.WriteFile(os.Getenv("PROCTREE_UNIX_INFO"), []byte(info), 0o600); err != nil {
		_ = child.Process.Kill()
		os.Exit(86)
	}
	for {
		time.Sleep(time.Second)
	}
}

func signalIgnoreTERM() {
	signal.Ignore(syscall.SIGTERM)
}

func processCanExecute(pid int) bool {
	err := unix.Kill(pid, 0)
	if errors.Is(err, unix.ESRCH) {
		return false
	}
	if err != nil {
		return true
	}
	psPath := "/bin/ps"
	if _, err := os.Stat(psPath); err != nil {
		psPath = "/usr/bin/ps"
	}
	payload, err := exec.Command(psPath, "-p", strconv.Itoa(pid), "-o", "state=").Output()
	if err != nil {
		return false
	}
	return !strings.HasPrefix(strings.TrimSpace(string(payload)), "Z")
}
