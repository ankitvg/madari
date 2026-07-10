package executionreceipt

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/ankitvg/madari/internal/clients/syncshared"
)

// Write validates and atomically replaces path with an owner-only V1 receipt.
func Write(path string, receipt Receipt) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("execution receipt path is required")
	}
	payload, err := Marshal(receipt)
	if err != nil {
		return err
	}
	if err := syncshared.WriteFileAtomically(filepath.Clean(path), payload, 0o600); err != nil {
		return fmt.Errorf("write execution receipt %q: %w", path, err)
	}
	return nil
}
