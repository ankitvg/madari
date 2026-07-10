package main

import (
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

const minimumCodexPolicyRunVersion = "0.139.0"

var codexSemanticVersionPattern = regexp.MustCompile(`([0-9]+)\.([0-9]+)\.([0-9]+)(-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?`)

type codexSemanticVersion struct {
	major      int
	minor      int
	patch      int
	prerelease string
}

func validateCodexPolicyRunCompatibility() error {
	return validateCodexPolicyRunVersion()
}

func validateCodexPolicyRunVersion() error {
	path, err := exec.LookPath("codex")
	if err != nil {
		return fmt.Errorf("codex executable not found in PATH; install a stable Codex CLI 0.139.x release (minimum %s) for capability policy runs", minimumCodexPolicyRunVersion)
	}
	output, err := exec.Command(path, "--version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("inspect Codex version for capability policy run: %w", err)
	}
	current, err := parseCodexSemanticVersion(string(output))
	if err != nil {
		return fmt.Errorf("cannot verify Codex capability policy support from version output %q: %w", strings.TrimSpace(string(output)), err)
	}
	minimum, _ := parseCodexSemanticVersion(minimumCodexPolicyRunVersion)
	if current.lessThan(minimum) || current.major != 0 || current.minor != 139 || current.prerelease != "" {
		return fmt.Errorf("Codex %s is outside the validated capability policy run range; install a stable Codex CLI 0.139.x release (minimum %s)", current, minimumCodexPolicyRunVersion)
	}
	return nil
}

func parseCodexSemanticVersion(output string) (codexSemanticVersion, error) {
	match := codexSemanticVersionPattern.FindStringSubmatch(strings.TrimSpace(output))
	if len(match) != 5 {
		return codexSemanticVersion{}, fmt.Errorf("expected MAJOR.MINOR.PATCH")
	}
	parts := [3]int{}
	for i := range parts {
		value, err := strconv.Atoi(match[i+1])
		if err != nil {
			return codexSemanticVersion{}, fmt.Errorf("parse version component %q: %w", match[i+1], err)
		}
		parts[i] = value
	}
	return codexSemanticVersion{major: parts[0], minor: parts[1], patch: parts[2], prerelease: match[4]}, nil
}

func (v codexSemanticVersion) lessThan(other codexSemanticVersion) bool {
	if v.major != other.major {
		return v.major < other.major
	}
	if v.minor != other.minor {
		return v.minor < other.minor
	}
	if v.patch != other.patch {
		return v.patch < other.patch
	}
	return v.prerelease != "" && other.prerelease == ""
}

func (v codexSemanticVersion) String() string {
	return fmt.Sprintf("%d.%d.%d%s", v.major, v.minor, v.patch, v.prerelease)
}
