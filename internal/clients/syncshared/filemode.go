//go:build !windows

package syncshared

import "os"

func prepareFileBeforeWrite(_ string, _ os.FileMode) error {
	return nil
}

func applyFileMode(path string, mode os.FileMode) error {
	return os.Chmod(path, mode)
}
