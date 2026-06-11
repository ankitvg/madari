package clients

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// CommandPathError describes why a manifest command path cannot run. Code is
// a stable machine-readable identifier (doctor reports it verbatim).
type CommandPathError struct {
	Code    string
	Message string
}

func (e *CommandPathError) Error() string {
	return e.Message
}

// ValidateCommandPath is the single command-validity check shared by sync
// filtering, doctor diagnostics, drift plans, and command resolution — these
// must never disagree about what is runnable.
func ValidateCommandPath(path string) *CommandPathError {
	if !filepath.IsAbs(path) {
		return &CommandPathError{Code: "command_not_absolute", Message: "command path must be absolute"}
	}
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &CommandPathError{Code: "command_missing", Message: "command path does not exist"}
		}
		return &CommandPathError{Code: "command_stat_error", Message: fmt.Sprintf("unable to inspect command path: %v", err)}
	}
	if info.IsDir() {
		return &CommandPathError{Code: "command_is_directory", Message: "command path is a directory"}
	}
	if runtime.GOOS != "windows" && info.Mode()&0o111 == 0 {
		return &CommandPathError{Code: "command_not_executable", Message: "command path is not executable"}
	}
	return nil
}
