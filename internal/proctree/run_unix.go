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
	rootPID int
	closed  bool
}

type processInfo struct {
	PID       int
	PPID      int
	SessionID int
}

type descendantTracker struct {
	rootPID int
	tracked map[int]struct{}
}

// PPID containment is necessarily observational on portable Unix. A process
// that creates a new session, double-forks, and lets the intermediate parent
// exit entirely between snapshots can escape both the original SID and the
// observed ancestry graph. TreeTerminationCompleted therefore covers every
// same-session or PPID-linked descendant observed by these sweeps; it is not a
// claim about a fully escaped, never-observed daemon.

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
	return &unixController{rootPID: cmd.Process.Pid}, true, nil
}

func (c *unixController) terminate() error {
	if c == nil || c.closed {
		return nil
	}
	c.closed = true
	return terminateObservedTree(c.rootPID)
}

func (c *unixController) close() error {
	// Clean up same-session and still-observed descendants even after the
	// session leader exits normally, so an ordinary parent cannot leave an
	// observed background child behind.
	return c.terminate()
}

func terminateObservedTree(rootPID int) error {
	tracker := &descendantTracker{rootPID: rootPID, tracked: map[int]struct{}{rootPID: {}}}
	pids, observationErr := tracker.refresh()

	// A negative PID reaches the leader's original process group immediately.
	// The observed-PID sweep also reaches descendants that created a separate
	// process group or session but were still linked through PPID when observed.
	_ = ignoreMissingProcess(unix.Kill(-rootPID, unix.SIGTERM))
	_ = signalProcesses(pids, unix.SIGTERM)

	deadline := time.Now().Add(termGracePeriod)
	for time.Now().Before(deadline) {
		pids, err := tracker.refresh()
		if err != nil {
			observationErr = errors.Join(observationErr, err)
			break
		}
		if len(pids) == 0 {
			return observationErr
		}
		time.Sleep(killSweepDelay)
	}

	killed := map[int]struct{}{}
	var killErrs []error
	for sweep := 0; sweep < killSweepLimit; sweep++ {
		if err := ignoreMissingProcess(unix.Kill(-rootPID, unix.SIGKILL)); err != nil {
			killErrs = append(killErrs, fmt.Errorf("kill session leader group: %w", err))
		}
		pids, err := tracker.refresh()
		if err != nil {
			observationErr = errors.Join(observationErr, err)
			pids = tracker.pids()
		}
		if len(pids) == 0 {
			return errors.Join(observationErr, errors.Join(killErrs...))
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
		// One sweep with no newly discovered PID proves that every process still
		// in the observed containment set has already accepted SIGKILL.
		// Zombies may remain visible until their new parent reaps them, but they
		// cannot execute or fork and therefore do not make containment incomplete.
		if !newPID {
			return errors.Join(observationErr, errors.Join(killErrs...))
		}
		time.Sleep(killSweepDelay)
	}
	return errors.Join(observationErr, errors.Join(append(killErrs, fmt.Errorf("observed process tree rooted at %d kept creating processes during kill sweeps", rootPID))...))
}

func signalProcesses(pids []int, signal unix.Signal) error {
	var errs []error
	for _, pid := range pids {
		if err := ignoreMissingProcess(unix.Kill(pid, signal)); err != nil {
			errs = append(errs, fmt.Errorf("signal process %d: %w", pid, err))
		}
	}
	return errors.Join(errs...)
}

func (t *descendantTracker) refresh() ([]int, error) {
	all, err := listProcesses()
	if err != nil {
		return nil, err
	}
	present := make(map[int]processInfo, len(all))
	for _, process := range all {
		sid, err := unix.Getsid(process.PID)
		if err != nil {
			if errors.Is(err, unix.ESRCH) {
				continue
			}
			return nil, fmt.Errorf("get session for process %d: %w", process.PID, err)
		}
		process.SessionID = sid
		present[process.PID] = process
	}

	active := make(map[int]struct{}, len(t.tracked))
	for pid := range t.tracked {
		if _, exists := present[pid]; exists {
			active[pid] = struct{}{}
		}
	}
	for pid, process := range present {
		if pid == t.rootPID || process.SessionID == t.rootPID {
			active[pid] = struct{}{}
		}
	}
	for changed := true; changed; {
		changed = false
		for pid, process := range present {
			if _, exists := active[pid]; exists {
				continue
			}
			if _, parentObserved := active[process.PPID]; parentObserved {
				active[pid] = struct{}{}
				changed = true
			}
		}
	}
	t.tracked = active
	return t.pids(), nil
}

func (t *descendantTracker) pids() []int {
	pids := make([]int, 0, len(t.tracked))
	for pid := range t.tracked {
		pids = append(pids, pid)
	}
	// Descendants tend to have higher PIDs. Signal them before the leader to
	// reduce the window in which a still-running parent can fork again.
	sort.Sort(sort.Reverse(sort.IntSlice(pids)))
	return pids
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
