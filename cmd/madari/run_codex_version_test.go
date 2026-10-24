package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestParseCodexSemanticVersion(t *testing.T) {
	cases := []struct {
		output string
		want   string
	}{
		{"codex-cli 0.139.0", "0.139.0"},
		{"codex-cli 0.140.1-beta.2", "0.140.1-beta.2"},
		{"1.0.0", "1.0.0"},
	}
	for _, tc := range cases {
		got, err := parseCodexSemanticVersion(tc.output)
		if err != nil {
			t.Fatalf("parse %q: %v", tc.output, err)
		}
		if got.String() != tc.want {
			t.Fatalf("parse %q: got %s want %s", tc.output, got, tc.want)
		}
	}
	if _, err := parseCodexSemanticVersion("codex development build"); err == nil {
		t.Fatalf("expected unversioned build to fail closed")
	}
	minimum, _ := parseCodexSemanticVersion("0.139.0")
	boundaries := []struct {
		version string
		older   bool
	}{
		{"0.138.9", true},
		{"0.139.0-beta.1", true},
		{"0.139.0", false},
		{"0.140.0-beta.1", false},
	}
	for _, boundary := range boundaries {
		version, err := parseCodexSemanticVersion(boundary.version)
		if err != nil {
			t.Fatalf("parse boundary %s: %v", boundary.version, err)
		}
		if got := version.lessThan(minimum); got != boundary.older {
			t.Fatalf("boundary %s older=%t want %t", boundary.version, got, boundary.older)
		}
	}
}

func installVersionOnlyCodex(t *testing.T, version string) {
	t.Helper()
	binDir := t.TempDir()
	path := filepath.Join(binDir, "codex")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf 'codex-cli "+version+"\\n'\n"), 0o755); err != nil {
		t.Fatalf("write fake Codex: %v", err)
	}
	t.Setenv("PATH", binDir)
}

func TestValidateCodexPolicyRunVersionRejectsOlderCLI(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is Unix-specific")
	}
	installVersionOnlyCodex(t, "0.138.9")
	err := validateCodexPolicyRunVersion()
	if err == nil || !strings.Contains(err.Error(), "stable Codex CLI 0.139.0 or newer") {
		t.Fatalf("expected old Codex refusal, got: %v", err)
	}
}

func TestValidateCodexPolicyRunCompatibilityAcceptsNewerStableReleases(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is Unix-specific")
	}
	for _, version := range []string{"0.139.0", "0.140.0", "0.164.0", "1.0.0"} {
		t.Run(version, func(t *testing.T) {
			installVersionOnlyCodex(t, version)
			if err := validateCodexPolicyRunCompatibility(); err != nil {
				t.Fatalf("expected Codex %s to pass the version floor, got: %v", version, err)
			}
		})
	}
}

func TestValidateCodexPolicyRunCompatibilityRejectsPrereleases(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is Unix-specific")
	}
	installVersionOnlyCodex(t, "0.165.0-alpha.1")
	err := validateCodexPolicyRunCompatibility()
	if err == nil || !strings.Contains(err.Error(), "stable Codex CLI 0.139.0 or newer") {
		t.Fatalf("expected prerelease refusal, got: %v", err)
	}
}
