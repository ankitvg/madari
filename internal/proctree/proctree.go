package proctree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"
)

// Outcome is the bounded result of running one contained process tree.
type Outcome string

const (
	OutcomeSuccess     Outcome = "success"
	OutcomeFailure     Outcome = "failure"
	OutcomeTimeout     Outcome = "timeout"
	OutcomeCancelled   Outcome = "cancelled"
	OutcomeStartFailed Outcome = "start-failed"
)

// TerminationReason explains why Madari stopped a running process tree.
type TerminationReason string

const (
	TerminationReasonTimeout   TerminationReason = "timeout"
	TerminationReasonCancelled TerminationReason = "cancelled"
)

// TreeTermination records whether the platform containment boundary proved
// that termination reached the complete process tree.
type TreeTermination string

const (
	TreeTerminationCompleted  TreeTermination = "completed"
	TreeTerminationIncomplete TreeTermination = "incomplete"
)

// Exit is observed process-exit evidence. Code is -1 for a signal exit on
// Unix; Signal is empty when the platform did not report a signal.
type Exit struct {
	Code   int
	Signal string
}

// Termination describes an explicit timeout or cancellation attempt.
type Termination struct {
	Reason          TerminationReason
	TreeTermination TreeTermination
}

// Result contains only value evidence. Operational errors are returned
// separately by Run and are never embedded in this structure.
type Result struct {
	Outcome        Outcome
	StartedAt      time.Time
	FinishedAt     time.Time
	ProcessStarted bool
	Exit           *Exit
	Termination    *Termination
}

type treeController interface {
	terminate() error
	close() error
}

type waitResult struct {
	state *os.ProcessState
	err   error
}

const reapTimeout = 5 * time.Second

// Run starts cmd inside a platform process-tree containment boundary and waits
// for it to finish, for ctx to be cancelled, or for timeout to expire. Callers
// must provide a positive timeout. A non-zero child exit is OutcomeFailure and
// is returned as the ordinary *exec.ExitError from cmd.Wait.
func Run(ctx context.Context, cmd *exec.Cmd, timeout time.Duration) (result Result, retErr error) {
	result.StartedAt = time.Now().UTC()
	result.Outcome = OutcomeStartFailed
	defer func() {
		result.FinishedAt = time.Now().UTC()
	}()

	if ctx == nil {
		return result, fmt.Errorf("process context is required")
	}
	if cmd == nil {
		return result, fmt.Errorf("process command is required")
	}
	if timeout <= 0 {
		return result, fmt.Errorf("process timeout must be positive")
	}
	if err := ctx.Err(); err != nil {
		result.Outcome = OutcomeCancelled
		return result, err
	}

	controller, processStarted, err := startContained(cmd)
	result.ProcessStarted = processStarted
	if err != nil {
		return result, err
	}

	waitCh := make(chan waitResult, 1)
	go func() {
		err := cmd.Wait()
		waitCh <- waitResult{state: cmd.ProcessState, err: err}
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case waited := <-waitCh:
		return finishNaturally(result, controller, waited)
	case <-ctx.Done():
		if waited, ok := completedWait(waitCh); ok {
			return finishNaturally(result, controller, waited)
		}
		return finishTerminated(result, controller, waitCh, TerminationReasonCancelled, ctx.Err())
	case <-timer.C:
		if waited, ok := completedWait(waitCh); ok {
			return finishNaturally(result, controller, waited)
		}
		if err := ctx.Err(); err != nil {
			return finishTerminated(result, controller, waitCh, TerminationReasonCancelled, err)
		}
		return finishTerminated(result, controller, waitCh, TerminationReasonTimeout, context.DeadlineExceeded)
	}
}

func completedWait(waitCh <-chan waitResult) (waitResult, bool) {
	select {
	case waited := <-waitCh:
		return waited, true
	default:
		return waitResult{}, false
	}
}

func finishNaturally(result Result, controller treeController, waited waitResult) (Result, error) {
	result.Exit = observedExit(waited.state)
	cleanupErr := controller.close()
	if waited.err == nil && cleanupErr == nil {
		result.Outcome = OutcomeSuccess
		return result, nil
	}
	result.Outcome = OutcomeFailure
	if cleanupErr != nil {
		cleanupErr = fmt.Errorf("clean up contained process tree: %w", cleanupErr)
	}
	return result, errors.Join(waited.err, cleanupErr)
}

func finishTerminated(result Result, controller treeController, waitCh <-chan waitResult, reason TerminationReason, cause error) (Result, error) {
	result.Outcome = OutcomeTimeout
	if reason == TerminationReasonCancelled {
		result.Outcome = OutcomeCancelled
	}
	result.Termination = &Termination{
		Reason:          reason,
		TreeTermination: TreeTerminationCompleted,
	}

	terminationErr := controller.terminate()
	waited, waitErr := waitForReap(waitCh)
	if waitErr == nil {
		result.Exit = observedExit(waited.state)
	}
	closeErr := controller.close()
	if terminationErr != nil || waitErr != nil || closeErr != nil {
		result.Termination.TreeTermination = TreeTerminationIncomplete
	}
	if terminationErr != nil {
		terminationErr = fmt.Errorf("terminate contained process tree: %w", terminationErr)
	}
	if closeErr != nil {
		closeErr = fmt.Errorf("close process containment: %w", closeErr)
	}
	return result, errors.Join(cause, terminationErr, waitErr, closeErr)
}

func waitForReap(waitCh <-chan waitResult) (waitResult, error) {
	timer := time.NewTimer(reapTimeout)
	defer timer.Stop()
	select {
	case waited := <-waitCh:
		return waited, nil
	case <-timer.C:
		return waitResult{}, fmt.Errorf("process did not exit within %s after tree termination", reapTimeout)
	}
}

func observedExit(state *os.ProcessState) *Exit {
	if state == nil {
		return nil
	}
	return &Exit{Code: state.ExitCode(), Signal: processSignal(state)}
}
