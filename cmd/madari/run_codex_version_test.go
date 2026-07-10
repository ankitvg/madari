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

func TestValidateCodexPolicyRunVersionRejectsOlderCLI(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is Unix-specific")
	}
	binDir := t.TempDir()
	path := filepath.Join(binDir, "codex")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf 'codex-cli 0.138.9\\n'\n"), 0o755); err != nil {
		t.Fatalf("write fake Codex: %v", err)
	}
	t.Setenv("PATH", binDir)
	err := validateCodexPolicyRunVersion()
	if err == nil || !strings.Contains(err.Error(), "stable Codex CLI 0.139.x") {
		t.Fatalf("expected old Codex refusal, got: %v", err)
	}
}

func TestValidateCodexPolicyRunCompatibilityRejectsUnvalidatedNewerSeries(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is Unix-specific")
	}
	binDir := t.TempDir()
	path := filepath.Join(binDir, "codex")
	script := "#!/bin/sh\nprintf 'codex-cli 0.140.0\\n'\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake Codex: %v", err)
	}
	t.Setenv("PATH", binDir)
	err := validateCodexPolicyRunCompatibility()
	if err == nil || !strings.Contains(err.Error(), "stable Codex CLI 0.139.x") {
		t.Fatalf("expected unvalidated version refusal, got: %v", err)
	}
}
