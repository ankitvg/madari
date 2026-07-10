//go:build aix || dragonfly || freebsd || netbsd || openbsd || solaris

package proctree

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

func listProcesses() ([]processInfo, error) {
	var psPath string
	for _, candidate := range []string{"/bin/ps", "/usr/bin/ps"} {
		if _, err := os.Stat(candidate); err == nil {
			psPath = candidate
			break
		}
	}
	if psPath == "" {
		var err error
		psPath, err = exec.LookPath("ps")
		if err != nil {
			return nil, fmt.Errorf("find ps for process enumeration: %w", err)
		}
	}
	payload, err := exec.Command(psPath, "-A", "-o", "pid=", "-o", "ppid=").Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, fmt.Errorf("enumerate processes with ps: %w: %s", err, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, fmt.Errorf("enumerate processes with ps: %w", err)
	}
	fields := strings.Fields(string(payload))
	if len(fields)%2 != 0 {
		return nil, fmt.Errorf("parse ps PID/PPID output %q", strings.TrimSpace(string(payload)))
	}
	processes := make([]processInfo, 0, len(fields)/2)
	for i := 0; i < len(fields); i += 2 {
		pid, err := strconv.Atoi(fields[i])
		if err != nil || pid <= 0 {
			return nil, fmt.Errorf("parse ps process id %q", fields[i])
		}
		ppid, err := strconv.Atoi(fields[i+1])
		if err != nil || ppid < 0 {
			return nil, fmt.Errorf("parse ps parent process id %q", fields[i+1])
		}
		processes = append(processes, processInfo{PID: pid, PPID: ppid})
	}
	return processes, nil
}
