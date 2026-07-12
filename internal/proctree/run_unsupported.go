//go:build !windows && !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris

package proctree

import (
	"fmt"
	"os"
	"os/exec"
)

type unsupportedController struct{}

func startContained(*exec.Cmd) (treeController, bool, error) {
	return nil, false, fmt.Errorf("process-tree containment is not supported on this platform")
}

func (*unsupportedController) terminate() error { return nil }
func (*unsupportedController) close() error     { return nil }

func processSignal(*os.ProcessState) string { return "" }
