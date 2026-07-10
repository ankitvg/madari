package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/ankitvg/madari/internal/launch"
)

const minimumCodexRunVersion = "0.139.0"

var codexSemanticVersionPattern = regexp.MustCompile(`([0-9]+)\.([0-9]+)\.([0-9]+)(-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?`)

type codexSemanticVersion struct {
	major      int
	minor      int
	patch      int
	prerelease string
}

func validateCodexRunCompatibility() error {
	return validateCodexRunVersion()
}

func validateCodexRunVersion() error {
	_, err := inspectCodexRunClient(codexPlatformBaseline())
	return err
}

func inspectCodexRunClient(baseline map[string]string) (launch.ClientInput, error) {
	path, err := exec.LookPath("codex")
	if err != nil {
		return launch.ClientInput{}, fmt.Errorf("codex executable not found in PATH; install a stable Codex CLI 0.139.x release (minimum %s)", minimumCodexRunVersion)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return launch.ClientInput{}, fmt.Errorf("resolve Codex executable path: %w", err)
	}
	digestBefore, err := hashFile(path)
	if err != nil {
		return launch.ClientInput{}, fmt.Errorf("hash Codex executable before version probe: %w", err)
	}
	probeRoot, err := os.MkdirTemp("", "madari-codex-version-*")
	if err != nil {
		return launch.ClientInput{}, fmt.Errorf("create isolated Codex version probe: %w", err)
	}
	defer os.RemoveAll(probeRoot)
	probeEnv := cloneEnvMap(baseline)
	if probeEnv == nil {
		probeEnv = map[string]string{}
	}
	probeHome := filepath.Join(probeRoot, "home")
	probeCodexHome := filepath.Join(probeRoot, "codex-home")
	probeTemp := filepath.Join(probeRoot, "tmp")
	for _, dir := range []string{probeHome, probeCodexHome, probeTemp} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return launch.ClientInput{}, fmt.Errorf("prepare isolated Codex version probe: %w", err)
		}
	}
	for key, value := range map[string]string{
		"HOME": probeHome, "USERPROFILE": probeHome, "CODEX_HOME": probeCodexHome,
		"TMPDIR": probeTemp, "TEMP": probeTemp, "TMP": probeTemp,
	} {
		probeEnv[key] = value
	}
	if runtime.GOOS == "windows" {
		probeEnv["APPDATA"] = filepath.Join(probeHome, "AppData", "Roaming")
		probeEnv["LOCALAPPDATA"] = filepath.Join(probeHome, "AppData", "Local")
	}
	cmd := exec.Command(path, "--version")
	cmd.Dir = probeRoot
	cmd.Env = envMapList(probeEnv)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return launch.ClientInput{}, fmt.Errorf("inspect Codex version for bounded run: %w", err)
	}
	current, err := parseCodexSemanticVersion(string(output))
	if err != nil {
		return launch.ClientInput{}, fmt.Errorf("cannot verify Codex bounded-run support from version output: %w", err)
	}
	minimum, _ := parseCodexSemanticVersion(minimumCodexRunVersion)
	if current.lessThan(minimum) || current.major != 0 || current.minor != 139 || current.prerelease != "" {
		return launch.ClientInput{}, fmt.Errorf("Codex %s is outside the validated bounded-run range; install a stable Codex CLI 0.139.x release (minimum %s)", current, minimumCodexRunVersion)
	}
	digest, err := hashFile(path)
	if err != nil {
		return launch.ClientInput{}, fmt.Errorf("hash Codex executable after version probe: %w", err)
	}
	if digest != digestBefore {
		return launch.ClientInput{}, fmt.Errorf("Codex executable changed during the isolated version probe")
	}
	return launch.ClientInput{Path: filepath.Clean(path), Version: current.String(), BinarySHA256: digest}, nil
}

func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	h := sha256.New()
	if _, err := io.Copy(h, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
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
