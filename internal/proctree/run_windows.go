//go:build windows

package proctree

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type windowsController struct {
	job    windows.Handle
	closed bool
}

func startContained(cmd *exec.Cmd) (treeController, bool, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, false, fmt.Errorf("create process job: %w", err)
	}
	closeJob := true
	defer func() {
		if closeJob {
			_ = windows.CloseHandle(job)
		}
	}()

	var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limits)),
		uint32(unsafe.Sizeof(limits)),
	); err != nil {
		return nil, false, fmt.Errorf("configure kill-on-close process job: %w", err)
	}

	var attr syscall.SysProcAttr
	if cmd.SysProcAttr != nil {
		attr = *cmd.SysProcAttr
	}
	// Put the child in a separate console process group as well as the Job.
	// That keeps a Ctrl+C handled by Madari from racing a direct console signal
	// to the child; cancellation then follows the single Job-wide kill path.
	attr.CreationFlags |= windows.CREATE_SUSPENDED | windows.CREATE_NEW_PROCESS_GROUP
	cmd.SysProcAttr = &attr
	if err := cmd.Start(); err != nil {
		return nil, false, err
	}

	assigned := false
	var assignErr error
	if err := cmd.Process.WithHandle(func(handle uintptr) {
		assignErr = windows.AssignProcessToJobObject(job, windows.Handle(handle))
	}); err != nil {
		closeJob = false
		return nil, false, abortSuspendedProcess(cmd, job, false, fmt.Errorf("access suspended process handle: %w", err))
	}
	if assignErr != nil {
		closeJob = false
		return nil, false, abortSuspendedProcess(cmd, job, false, fmt.Errorf("assign suspended process to kill-on-close job: %w", assignErr))
	}
	assigned = true
	if err := resumeSuspendedProcess(uint32(cmd.Process.Pid)); err != nil {
		closeJob = false
		return nil, false, abortSuspendedProcess(cmd, job, assigned, err)
	}

	closeJob = false
	return &windowsController{job: job}, true, nil
}

func abortSuspendedProcess(cmd *exec.Cmd, job windows.Handle, assigned bool, cause error) error {
	var cleanupErr error
	if assigned {
		cleanupErr = windows.CloseHandle(job)
		if cleanupErr != nil {
			cleanupErr = errors.Join(cleanupErr, windows.TerminateJobObject(job, 1))
		}
	} else if cmd.Process != nil {
		cleanupErr = errors.Join(cmd.Process.Kill(), windows.CloseHandle(job))
	}
	if cleanupErr != nil && cmd.Process != nil {
		cleanupErr = errors.Join(cleanupErr, cmd.Process.Kill())
	}
	if cmd.Process != nil {
		if err := cmd.Wait(); err != nil {
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) {
				cleanupErr = errors.Join(cleanupErr, err)
			}
		}
	}
	if cleanupErr != nil {
		cleanupErr = fmt.Errorf("stop suspended uncontained process: %w", cleanupErr)
	}
	return errors.Join(cause, cleanupErr)
}

func resumeSuspendedProcess(processID uint32) error {
	deadline := time.Now().Add(250 * time.Millisecond)
	for {
		snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
		if err != nil {
			return fmt.Errorf("snapshot suspended process threads: %w", err)
		}
		threadID, findErr := findProcessThread(snapshot, processID)
		closeErr := windows.CloseHandle(snapshot)
		if findErr == nil && closeErr == nil && threadID != 0 {
			thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, threadID)
			if err != nil {
				return fmt.Errorf("open suspended process thread: %w", err)
			}
			for {
				previous, resumeErr := windows.ResumeThread(thread)
				if resumeErr != nil {
					_ = windows.CloseHandle(thread)
					return fmt.Errorf("resume contained process thread: %w", resumeErr)
				}
				if previous <= 1 {
					break
				}
			}
			// Containment is already established and the child has resumed. A
			// thread-handle close failure must not misreport that code never ran;
			// the Job handle remains the authoritative lifetime boundary.
			_ = windows.CloseHandle(thread)
			return nil
		}
		if closeErr != nil {
			return fmt.Errorf("close process thread snapshot: %w", closeErr)
		}
		if findErr != nil {
			return findErr
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("suspended process %d has no resumable primary thread", processID)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func findProcessThread(snapshot windows.Handle, processID uint32) (uint32, error) {
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	err := windows.Thread32First(snapshot, &entry)
	if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("enumerate suspended process threads: %w", err)
	}
	for {
		if entry.OwnerProcessID == processID {
			return entry.ThreadID, nil
		}
		entry.Size = uint32(unsafe.Sizeof(windows.ThreadEntry32{}))
		err = windows.Thread32Next(snapshot, &entry)
		if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
			return 0, nil
		}
		if err != nil {
			return 0, fmt.Errorf("enumerate suspended process threads: %w", err)
		}
	}
}

func (c *windowsController) terminate() error {
	if c == nil || c.closed {
		return nil
	}
	terminateErr := windows.TerminateJobObject(c.job, 1)
	closeErr := c.close()
	// A successful close is sufficient even if the explicit termination call
	// raced with process exit: KILL_ON_JOB_CLOSE is the authoritative fallback.
	if closeErr == nil {
		return nil
	}
	return errors.Join(terminateErr, closeErr)
}

func (c *windowsController) close() error {
	if c == nil || c.closed {
		return nil
	}
	c.closed = true
	// KILL_ON_JOB_CLOSE makes closing this one handle an atomic tree-wide kill;
	// descendants cannot escape by creating their own process group.
	return windows.CloseHandle(c.job)
}

func processSignal(*os.ProcessState) string {
	return ""
}
