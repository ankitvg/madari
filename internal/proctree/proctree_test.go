package proctree

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestRunSuccess(t *testing.T) {
	result, err := Run(context.Background(), helperCommand(t, "success"), 5*time.Second)
	if err != nil {
		t.Fatalf("run successful helper: %v", err)
	}
	if result.Outcome != OutcomeSuccess || !result.ProcessStarted {
		t.Fatalf("unexpected success result: %#v", result)
	}
	if result.Exit == nil || result.Exit.Code != 0 || result.Exit.Signal != "" {
		t.Fatalf("unexpected success exit: %#v", result.Exit)
	}
	if result.Termination != nil {
		t.Fatalf("natural exit should not have termination evidence: %#v", result.Termination)
	}
	assertResultTimes(t, result)
}

func TestRunFailureKeepsRawErrorSeparate(t *testing.T) {
	result, err := Run(context.Background(), helperCommand(t, "failure"), 5*time.Second)
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected raw exec.ExitError, got: %v", err)
	}
	if result.Outcome != OutcomeFailure || !result.ProcessStarted {
		t.Fatalf("unexpected failure result: %#v", result)
	}
	if result.Exit == nil || result.Exit.Code != 7 {
		t.Fatalf("unexpected failure exit: %#v", result.Exit)
	}
	if result.Termination != nil {
		t.Fatalf("natural failure should not have termination evidence: %#v", result.Termination)
	}
	assertResultTimes(t, result)
}

func TestRunTimeout(t *testing.T) {
	result, err := Run(context.Background(), helperCommand(t, "wait"), 100*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline error, got: %v", err)
	}
	if result.Outcome != OutcomeTimeout || !result.ProcessStarted {
		t.Fatalf("unexpected timeout result: %#v", result)
	}
	if result.Termination == nil || result.Termination.Reason != TerminationReasonTimeout || result.Termination.TreeTermination != TreeTerminationCompleted {
		t.Fatalf("unexpected timeout termination: %#v", result.Termination)
	}
	if result.Exit == nil {
		t.Fatalf("timeout should observe the reaped leader exit: %#v", result)
	}
	assertResultTimes(t, result)
}

func TestRunCancellation(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "ready")
	cmd := helperCommand(t, "ready-and-wait")
	cmd.Env = append(cmd.Env, "PROCTREE_READY="+ready)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	type response struct {
		result Result
		err    error
	}
	done := make(chan response, 1)
	go func() {
		result, err := Run(ctx, cmd, 5*time.Second)
		done <- response{result: result, err: err}
	}()
	waitForFile(t, ready, 3*time.Second)
	cancel()

	select {
	case got := <-done:
		if !errors.Is(got.err, context.Canceled) {
			t.Fatalf("expected cancellation error, got: %v", got.err)
		}
		if got.result.Outcome != OutcomeCancelled || !got.result.ProcessStarted {
			t.Fatalf("unexpected cancellation result: %#v", got.result)
		}
		if got.result.Termination == nil || got.result.Termination.Reason != TerminationReasonCancelled || got.result.Termination.TreeTermination != TreeTerminationCompleted {
			t.Fatalf("unexpected cancellation termination: %#v", got.result.Termination)
		}
		assertResultTimes(t, got.result)
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled process tree did not return")
	}
}

func TestRunRejectsInvalidInputsBeforeStarting(t *testing.T) {
	result, err := Run(context.Background(), nil, time.Second)
	if err == nil || result.Outcome != OutcomeStartFailed || result.ProcessStarted {
		t.Fatalf("nil command should fail before start: result=%#v err=%v", result, err)
	}
	result, err = Run(context.Background(), helperCommand(t, "success"), 0)
	if err == nil || result.Outcome != OutcomeStartFailed || result.ProcessStarted {
		t.Fatalf("zero timeout should fail before start: result=%#v err=%v", result, err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	result, err = Run(cancelled, helperCommand(t, "success"), time.Second)
	if !errors.Is(err, context.Canceled) || result.Outcome != OutcomeCancelled || result.ProcessStarted || result.Termination != nil {
		t.Fatalf("pre-cancelled context should not start: result=%#v err=%v", result, err)
	}
}

func TestProctreeHelperProcess(t *testing.T) {
	if os.Getenv("PROCTREE_HELPER") != "1" {
		return
	}
	switch os.Getenv("PROCTREE_HELPER_ROLE") {
	case "success":
		return
	case "failure":
		os.Exit(7)
	case "ready-and-wait":
		if err := os.WriteFile(os.Getenv("PROCTREE_READY"), []byte("ready\n"), 0o600); err != nil {
			os.Exit(91)
		}
		fallthrough
	case "wait":
		time.Sleep(30 * time.Second)
		return
	default:
		os.Exit(92)
	}
}

func helperCommand(t *testing.T, role string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestProctreeHelperProcess$")
	cmd.Env = append(os.Environ(), "PROCTREE_HELPER=1", "PROCTREE_HELPER_ROLE="+role)
	return cmd
}

func waitForFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

func assertResultTimes(t *testing.T, result Result) {
	t.Helper()
	if result.StartedAt.IsZero() || result.FinishedAt.IsZero() || result.FinishedAt.Before(result.StartedAt) {
		t.Fatalf("invalid result timestamps: started=%s finished=%s", result.StartedAt, result.FinishedAt)
	}
}
