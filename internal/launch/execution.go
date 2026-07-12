package launch

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ankitvg/madari/internal/clients/syncshared"
)

const (
	AmbientEnvDeny               = "deny"
	SandboxReadOnly              = "read-only"
	CredentialExposureRunProcess = "run-process"
	DefaultMaxDuration           = 15 * time.Minute
)

type Prepared struct {
	env       []string
	overrides []string
}

func normalizeExecutionConfig(value ExecutionConfig) (ExecutionConfig, error) {
	if value.AmbientEnv == "" {
		value.AmbientEnv = AmbientEnvDeny
	}
	if value.Sandbox == "" {
		value.Sandbox = SandboxReadOnly
	}
	if value.MaxDuration == 0 {
		value.MaxDuration = DefaultMaxDuration
	}
	if value.CredentialExposure == "" {
		value.CredentialExposure = CredentialExposureRunProcess
	}
	if value.AmbientEnv != AmbientEnvDeny {
		return ExecutionConfig{}, fmt.Errorf("unsupported ambient environment policy %q", value.AmbientEnv)
	}
	if value.Sandbox != SandboxReadOnly {
		return ExecutionConfig{}, fmt.Errorf("unsupported sandbox policy %q", value.Sandbox)
	}
	if value.MaxDuration <= 0 {
		return ExecutionConfig{}, fmt.Errorf("max duration must be positive")
	}
	if value.CredentialExposure != CredentialExposureRunProcess {
		return ExecutionConfig{}, fmt.Errorf("unsupported credential exposure policy %q", value.CredentialExposure)
	}
	return value, nil
}

func (a *Artifact) Prepare(runRoot string) (*Prepared, error) {
	if a == nil {
		return nil, fmt.Errorf("immutable launch artifact is required")
	}
	runRoot = filepath.Clean(runRoot)
	isolatedHome := filepath.Join(runRoot, "home")
	isolatedCodexHome := filepath.Join(runRoot, "codex-home")
	isolatedTemp := filepath.Join(runRoot, "tmp")
	for _, path := range []string{isolatedHome, isolatedCodexHome, isolatedTemp} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return nil, fmt.Errorf("create isolated run path: %w", err)
		}
	}
	if len(a.client.Auth) > 0 {
		if err := syncshared.WriteFileAtomically(filepath.Join(isolatedCodexHome, "auth.json"), a.client.Auth, 0o600); err != nil {
			return nil, fmt.Errorf("materialize frozen Codex auth: %w", err)
		}
	}

	processEnv := normalizedEnvironmentMap(a.baselineEnv)
	if processEnv == nil {
		processEnv = map[string]string{}
	}
	for key, value := range a.declaredEnv {
		processEnv[canonicalEnvironmentKey(key)] = value
	}
	for key, value := range isolatedEnvironment(isolatedHome, isolatedCodexHome, isolatedTemp) {
		processEnv[canonicalEnvironmentKey(key)] = value
	}

	shellEnv := normalizedEnvironmentMap(a.baselineEnv)
	if shellEnv == nil {
		shellEnv = map[string]string{}
	}
	for key, value := range isolatedEnvironment(isolatedHome, "", isolatedTemp) {
		if key != "CODEX_HOME" {
			shellEnv[key] = value
		}
	}
	overrides := append([]string(nil), a.codexOverrides...)
	overrides = append(overrides, shellEnvironmentPolicyOverride(shellEnv), "allow_login_shell=false")
	return &Prepared{env: environmentList(processEnv), overrides: overrides}, nil
}

func normalizedEnvironmentMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[canonicalEnvironmentKey(key)] = value
	}
	return out
}

func canonicalEnvironmentKey(key string) string {
	key = strings.TrimSpace(key)
	if os.PathSeparator == '\\' {
		return strings.ToUpper(key)
	}
	return key
}

func (p *Prepared) Env() []string {
	if p == nil {
		return []string{}
	}
	return append([]string(nil), p.env...)
}

func (p *Prepared) CodexOverrides() []string {
	if p == nil {
		return []string{}
	}
	return append([]string(nil), p.overrides...)
}

func (a *Artifact) VerifyClientBinary() error {
	if a == nil || a.client.Path == "" || a.client.BinarySHA256 == "" {
		return fmt.Errorf("compiled Codex client identity is missing")
	}
	file, err := os.Open(a.client.Path)
	if err != nil {
		return fmt.Errorf("open compiled Codex client: %w", err)
	}
	defer file.Close()
	h := sha256.New()
	if _, err := io.Copy(h, file); err != nil {
		return fmt.Errorf("hash compiled Codex client: %w", err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != a.client.BinarySHA256 {
		return fmt.Errorf("Codex executable changed after launch compilation")
	}
	return nil
}

func isolatedEnvironment(home, codexHome, temp string) map[string]string {
	out := map[string]string{
		"HOME": home, "USERPROFILE": home,
		"TMPDIR": temp, "TEMP": temp, "TMP": temp,
	}
	if codexHome != "" {
		out["CODEX_HOME"] = codexHome
	}
	if os.PathSeparator == '\\' {
		out["APPDATA"] = filepath.Join(home, "AppData", "Roaming")
		out["LOCALAPPDATA"] = filepath.Join(home, "AppData", "Local")
	}
	return out
}

func environmentList(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+values[key])
	}
	return out
}

func shellEnvironmentPolicyOverride(values map[string]string) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return "shell_environment_policy={ inherit = \"none\", ignore_default_excludes = false, include_only = " +
		tomlStringArray(keys) + ", set = " + tomlInlineStringMap(values) + ", experimental_use_profile = false }"
}

func containsEnvironmentKey(env []string, key string) bool {
	prefix := key + "="
	for _, value := range env {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}
