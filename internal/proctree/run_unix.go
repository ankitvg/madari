//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package proctree

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const (
	termGracePeriod = 250 * time.Millisecond
	killSweepDelay  = 10 * time.Millisecond
	killSweepLimit  = 12
)

type unixController struct {
	sessionID int
	closed    bool
}

func startContained(cmd *exec.Cmd) (treeController, bool, error) {
	var attr syscall.SysProcAttr
	if cmd.SysProcAttr != nil {
		attr = *cmd.SysProcAttr
		if attr.Setpgid || attr.Foreground || attr.Setctty {
			return nil, false, fmt.Errorf("process command requests process-group or controlling-terminal settings incompatible with isolated session containment")
		}
	}
	// Setsid runs in the child before exec. If it cannot establish the new
	// session, os/exec reports Start failure and no uncontained child resumes.
	attr.Setsid = true
	cmd.SysProcAttr = &attr
	if err := cmd.Start(); err != nil {
		return nil, false, err
	}
	return &unixController{sessionID: cmd.Process.Pid}, true, nil
}

func (c *unixController) terminate() error {
	if c == nil || c.closed {
		return nil
	}
	c.closed = true
	return terminateSession(c.sessionID)
}

func (c *unixController) close() error {
	// Clean up same-session descendants even after the session leader exits
	// normally, so a successful parent cannot leave an unbounded background
	// child behind.
	return c.terminate()
}

func terminateSession(sessionID int) error {
	// A negative PID reaches the leader's original process group immediately.
	// The per-PID sweep below is what also reaches descendants that created a
	// separate process group while remaining in this session.
	_ = ignoreMissingProcess(unix.Kill(-sessionID, unix.SIGTERM))
	_, _ = signalSessionProcesses(sessionID, unix.SIGTERM)

	deadline := time.Now().Add(termGracePeriod)
	for time.Now().Before(deadline) {
		pids, err := sessionProcessIDs(sessionID)
		if err != nil {
			break
		}
		if len(pids) == 0 {
			return nil
		}
		time.Sleep(killSweepDelay)
	}

	killed := map[int]struct{}{}
	var killErrs []error
	for sweep := 0; sweep < killSweepLimit; sweep++ {
		if err := ignoreMissingProcess(unix.Kill(-sessionID, unix.SIGKILL)); err != nil {
			killErrs = append(killErrs, fmt.Errorf("kill session leader group: %w", err))
		}
		pids, err := sessionProcessIDs(sessionID)
		if err != nil {
			return errors.Join(append(killErrs, fmt.Errorf("enumerate session %d: %w", sessionID, err))...)
		}
		if len(pids) == 0 {
			return nil
		}

		newPID := false
		for _, pid := range pids {
			if _, seen := killed[pid]; !seen {
				newPID = true
			}
			if err := ignoreMissingProcess(unix.Kill(pid, unix.SIGKILL)); err != nil {
				killErrs = append(killErrs, fmt.Errorf("kill process %d: %w", pid, err))
				continue
			}
			killed[pid] = struct{}{}
		}
		// One sweep with no newly discovered PID proves that every process that
		// could still execute in the session has already accepted SIGKILL.
		// Zombies may remain visible until their new parent reaps them, but they
		// cannot execute or fork and therefore do not make containment incomplete.
		if !newPID {
			return errors.Join(killErrs...)
		}
		time.Sleep(killSweepDelay)
	}
	return errors.Join(append(killErrs, fmt.Errorf("session %d kept creating processes during kill sweeps", sessionID))...)
}

func signalSessionProcesses(sessionID int, signal unix.Signal) ([]int, error) {
	pids, err := sessionProcessIDs(sessionID)
	if err != nil {
		return nil, err
	}
	var errs []error
	for _, pid := range pids {
		if err := ignoreMissingProcess(unix.Kill(pid, signal)); err != nil {
			errs = append(errs, fmt.Errorf("signal process %d: %w", pid, err))
		}
	}
	return pids, errors.Join(errs...)
}

func sessionProcessIDs(sessionID int) ([]int, error) {
	all, err := listProcessIDs()
	if err != nil {
		return nil, err
	}
	pids := make([]int, 0)
	for _, pid := range all {
		sid, err := unix.Getsid(pid)
		if err != nil {
			if errors.Is(err, unix.ESRCH) {
				continue
			}
			return nil, fmt.Errorf("get session for process %d: %w", pid, err)
		}
		if sid == sessionID {
			pids = append(pids, pid)
		}
	}
	// Descendants tend to have higher PIDs. Signal them before the leader to
	// reduce the window in which a still-running parent can fork again.
	sort.Sort(sort.Reverse(sort.IntSlice(pids)))
	return pids, nil
}

func ignoreMissingProcess(err error) error {
	if err == nil || errors.Is(err, unix.ESRCH) {
		return nil
	}
	return err
}

func processSignal(state *os.ProcessState) string {
	status, ok := state.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() {
		return ""
	}
	switch status.Signal() {
	case syscall.SIGHUP:
		return "SIGHUP"
	case syscall.SIGINT:
		return "SIGINT"
	case syscall.SIGQUIT:
		return "SIGQUIT"
	case syscall.SIGABRT:
		return "SIGABRT"
	case syscall.SIGKILL:
		return "SIGKILL"
	case syscall.SIGPIPE:
		return "SIGPIPE"
	case syscall.SIGTERM:
		return "SIGTERM"
	case syscall.SIGUSR1:
		return "SIGUSR1"
	case syscall.SIGUSR2:
		return "SIGUSR2"
	default:
		return fmt.Sprintf("signal-%d", status.Signal())
	}
}
