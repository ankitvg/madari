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

func listProcessIDs() ([]int, error) {
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
	payload, err := exec.Command(psPath, "-A", "-o", "pid=").Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, fmt.Errorf("enumerate processes with ps: %w: %s", err, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, fmt.Errorf("enumerate processes with ps: %w", err)
	}
	fields := strings.Fields(string(payload))
	pids := make([]int, 0, len(fields))
	for _, field := range fields {
		pid, err := strconv.Atoi(field)
		if err != nil || pid <= 0 {
			return nil, fmt.Errorf("parse ps process id %q", field)
		}
		pids = append(pids, pid)
	}
	return pids, nil
}
