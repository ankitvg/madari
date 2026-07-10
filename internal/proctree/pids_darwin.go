//go:build darwin

package proctree

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func listProcessIDs() ([]int, error) {
	processes, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, fmt.Errorf("list processes with sysctl: %w", err)
	}
	pids := make([]int, 0, len(processes))
	for _, process := range processes {
		if process.Proc.P_pid > 0 {
			pids = append(pids, int(process.Proc.P_pid))
		}
	}
	return pids, nil
}
