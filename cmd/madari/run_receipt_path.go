package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/ankitvg/madari/internal/clients/syncshared"
)

func resolveRunReceiptPath(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("receipt path is required")
	}
	expanded, err := syncshared.ExpandHome(value)
	if err != nil {
		return "", err
	}
	absolute, err := filepath.Abs(filepath.Clean(expanded))
	if err != nil {
		return "", fmt.Errorf("resolve receipt path: %w", err)
	}
	return filepath.Clean(absolute), nil
}
