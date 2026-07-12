//go:build linux

package proctree

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

func listProcesses() ([]processInfo, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, fmt.Errorf("read /proc: %w", err)
	}
	processes := make([]processInfo, 0, len(entries))
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}
		payload, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read process %d stat: %w", pid, err)
		}
		// comm is parenthesized and may contain spaces or parentheses. The last
		// ')' is followed by state, PPID, process group, and session fields.
		closeParen := strings.LastIndexByte(string(payload), ')')
		if closeParen < 0 {
			return nil, fmt.Errorf("parse process %d stat: missing command terminator", pid)
		}
		fields := strings.Fields(string(payload[closeParen+1:]))
		if len(fields) < 2 {
			return nil, fmt.Errorf("parse process %d stat: missing PPID", pid)
		}
		ppid, err := strconv.Atoi(fields[1])
		if err != nil {
			return nil, fmt.Errorf("parse process %d PPID %q: %w", pid, fields[1], err)
		}
		processes = append(processes, processInfo{PID: pid, PPID: ppid})
	}
	return processes, nil
}
