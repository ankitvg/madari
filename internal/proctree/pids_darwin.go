//go:build darwin

package proctree

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func listProcesses() ([]processInfo, error) {
	processes, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, fmt.Errorf("list processes with sysctl: %w", err)
	}
	out := make([]processInfo, 0, len(processes))
	for _, process := range processes {
		if process.Proc.P_pid > 0 {
			out = append(out, processInfo{PID: int(process.Proc.P_pid), PPID: int(process.Eproc.Ppid)})
		}
	}
	return out, nil
}
